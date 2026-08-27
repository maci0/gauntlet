// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package prompt owns the review prompts: the bundled set (compiled into the
// binary), any *-review.md the target tree carries, and the composition that
// turns one into an auto-fix instruction for an agent.
package prompt

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
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
		return stripBOM(string(b)), err
	}
	b, err := readNoFollow(r.Path)
	return stripBOM(b), err
}

// Fingerprint is the stable identity of one review's effective text: the
// SHA-256 of the body exactly as read, hex-encoded. Compose wraps a body in
// rules and a suffix compiled into the binary, which the run's own version
// line already identifies; the body is the part that can change underneath
// them, and project prompts and --prompt-dir files carry no other version
// than this. Journaling it per launch ties an agent's output to the exact
// words that produced it even after the file has moved on.
func Fingerprint(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// stripBOM removes a leading UTF-8 byte-order mark. The BOM declares the
// encoding; it is not content. Editors on Windows write one in front of every
// file, and left in place it hides a prompt's first line from the prefix
// match that extracts its description.
func stripBOM(s string) string {
	return strings.TrimPrefix(s, "\xef\xbb\xbf")
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

// Signal tokens a review may declare, so the file-signal suggester can
// propose it. A project's own review is otherwise unreachable there: the
// built-in rules only know built-in names.
//
//	Signals: ext:.zig, name:build.zig, path:src/plugins, mark:comptime
//
// A prompt found in a reviewed tree is untrusted input, so the line is parsed
// strictly: known kinds, a restricted value charset, bounded counts and
// lengths. Anything else in the line is dropped rather than guessed at.
const (
	signalPrefix = "Signals:"
	signalMax    = 12 // tokens honored per review
	signalValMax = 40 // runes per value
)

// signalKinds are the observations a review may key on: a file extension, a
// base name, a path fragment, and a substring found in source.
var signalKinds = map[string]bool{"ext": true, "name": true, "path": true, "mark": true}

// signalValueRe is the charset a value may use: what file names, paths, and
// short code markers are made of, and nothing that could carry structure.
var signalValueRe = regexp.MustCompile(`^[\p{L}\p{M}\p{N}._/+-]+$`)

// Signals returns the file signals this review declares, lowercased, as
// "kind:value" tokens. An undeclared or unparseable line yields none, which
// leaves the review to the built-in rules.
func (r Review) Signals() []string {
	body, err := r.Body()
	if err != nil {
		return nil
	}
	var out []string
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, signalPrefix) {
			continue
		}
		for field := range strings.SplitSeq(strings.TrimPrefix(line, signalPrefix), ",") {
			kind, value, ok := strings.Cut(strings.TrimSpace(field), ":")
			// Values name files a tree carries, and macOS hands out NFD
			// spellings while an author types NFC into the Signals: line;
			// normalizing here mirrors what discovery does to prompt stems
			// (see nfc) so the two forms meet byte-exactly.
			kind = strings.ToLower(strings.TrimSpace(nfc(kind)))
			value = strings.ToLower(strings.TrimSpace(nfc(value)))
			switch {
			case !ok, !signalKinds[kind], value == "":
			case utf8.RuneCountInString(value) > signalValMax:
			case !signalValueRe.MatchString(value):
			default:
				out = append(out, kind+":"+value)
			}
			if len(out) == signalMax {
				return out
			}
		}
		return out
	}
	return nil
}

// Set is an ordered, name-indexed collection of reviews.
type Set struct {
	Names  []string // sorted
	byName map[string]Review
}

// Get returns the review with this name. The query is normalized the same way
// discovery normalized its keys, so a name typed in one Unicode form finds a
// review stored under another.
func (s Set) Get(name string) (Review, bool) {
	r, ok := s.byName[nfc(name)]
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

// BundledNames lists the reviews compiled into the binary, sorted. It is the
// catalog every other bundled-review list is checked against: doctor's tool
// table and the review sets must cover exactly these names.
func BundledNames() []string {
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

// nfc is the review-name normalization form. Names are identity: they key
// discovery maps, --reviews selection, suggest matching, and branch slugs.
// Storing every discovered name as NFC and normalizing every name that enters
// from text typed by a user or emitted by an agent makes those comparisons
// byte-exact again for the same word spelled in different forms: a project
// file created on macOS carries an NFD stem (decomposed é) while a shell or
// keyboard produces NFC, and without this one of the two would not match.
func nfc(s string) string {
	return norm.NFC.String(s)
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
