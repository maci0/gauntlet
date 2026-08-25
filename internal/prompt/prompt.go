// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package prompt owns the review prompts: the bundled set (compiled into the
// binary), any *-review.md the target tree carries, and the composition that
// turns one into an auto-fix instruction for an agent.
package prompt

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode"
)

//go:embed prompts/*.md
var bundled embed.FS

// maxBytes caps a prompt file. A single argv string over ~128 KB (Linux
// MAX_ARG_STRLEN) already fails at exec with E2BIG, so no real prompt
// approaches this; the cap only stops a hostile multi-GB *-review.md from
// being buffered into RAM before that.
const maxBytes = 1 << 20

// Origin says where a review's text came from.
type Origin uint8

const (
	Bundled Origin = iota // compiled into the binary
	Project               // a *-review.md found in the reviewed tree
	Dir                   // a file in an explicit --prompt-dir
)

// Review is one review prompt.
type Review struct {
	Name   string // file stem, e.g. "sec-review"
	Path   string // on-disk path; empty for Bundled
	Origin Origin
}

// IsProject reports whether the prompt came from the reviewed tree.
func (r Review) IsProject() bool { return r.Origin == Project }

// Body returns the prompt text.
func (r Review) Body() (string, error) {
	if r.Origin == Bundled {
		b, err := bundled.ReadFile("prompts/" + r.Name + ".md")
		return string(b), err
	}
	return readNoFollow(r.Path)
}

// Desc is the prompt's first "Your goal" line, stripped to its predicate. It
// is display text from a possibly untrusted file, so it is sanitized.
func (r Review) Desc() string {
	body, err := r.Body()
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "Your goal") {
			line = sanitize(line)
			line = strings.TrimPrefix(line, "Your goal is to ")
			line = strings.TrimPrefix(line, "Your goal is ")
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// Set is an ordered, name-indexed collection of reviews.
type Set struct {
	Names  []string // sorted
	byName map[string]Review
}

// Get returns the review with this name.
func (s Set) Get(name string) (Review, bool) {
	r, ok := s.byName[name]
	return r, ok
}

// Len is the number of reviews available.
func (s Set) Len() int { return len(s.Names) }

// ProjectNames lists only the reviews that came from the reviewed tree.
func (s Set) ProjectNames() []string {
	var out []string
	for _, n := range s.Names {
		if s.byName[n].IsProject() {
			out = append(out, n)
		}
	}
	return out
}

// bundledNames lists the reviews compiled into the binary.
func bundledNames() []string {
	entries, err := fs.Glob(bundled, "prompts/*-review.md")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(filepath.Base(e), ".md"))
	}
	sort.Strings(out)
	return out
}

// readNoFollow reads a regular file, refusing symlinks at open time.
//
// Checking for a symlink and then reading by path is two lookups: the file can
// be swapped in between, which is how out-of-tree content would reach a
// permission-bypassed agent. O_NOFOLLOW closes that window; O_NONBLOCK plus
// the stat keep a planted FIFO or device from blocking the open forever. The
// size cap bounds a hostile oversized prompt.
//
// Hardlinks are not refused: a package manager legitimately hardlinks prompts
// from its cache, so a link count above one is normal.
func readNoFollow(path string) (string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", &os.PathError{Op: "read", Path: path, Err: errors.New("not a regular file")}
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxBytes {
		return "", fmt.Errorf("prompt exceeds %d bytes: %s", maxBytes, path)
	}
	return string(data), nil
}

// sanitize strips control and formatting characters from untrusted display
// text: every Unicode Cc control (C0, C1, DEL) and Cf format character (bidi
// overrides, zero widths, joiners, interlinear and tag characters), all of
// which can drive or spoof a terminal or carry invisible text.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' {
			return r
		}
		if !isPrintable(r) {
			return -1
		}
		return r
	}, s)
}

func isPrintable(r rune) bool {
	return !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r)
}
