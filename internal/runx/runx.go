// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package runx is the shared subprocess kill and output-cap plumbing.
//
// Every external binary gauntlet launches (git, gh, a usage probe, a dsh
// config dump, the indexer) gets its own process group so a deadline kill
// takes children with it, a WaitDelay so a grandchild holding the pipes
// cannot park the caller, and — when the output is captured — a cap so a
// hostile listing cannot fill RAM.
package runx

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Writer keeps at most Limit bytes, then discards the rest so a pipe does
// not back-pressure the child into a hang. Hit is set once the cap is
// exceeded, so the caller can refuse a truncated listing.
type Writer struct {
	buf   bytes.Buffer
	Limit int
	Hit   bool
}

func (w *Writer) Write(p []byte) (int, error) {
	if w.Limit <= 0 {
		return w.buf.Write(p)
	}
	if w.Hit {
		return len(p), nil
	}
	room := w.Limit - w.buf.Len()
	if len(p) <= room {
		return w.buf.Write(p)
	}
	if room > 0 {
		_, _ = w.buf.Write(p[:room])
	}
	w.Hit = true
	return len(p), nil
}

func (w *Writer) Bytes() []byte  { return w.buf.Bytes() }
func (w *Writer) String() string { return w.buf.String() }

// Guard puts cmd in its own process group, kills the group on Cancel, and
// bounds how long Wait may sit on output pipes a grandchild still holds.
func Guard(cmd *exec.Cmd, wait time.Duration) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = wait
}

// Bound is Guard plus capped stdout and stderr. The caller still runs cmd.
func Bound(cmd *exec.Cmd, limit int, wait time.Duration) (out, errOut *Writer) {
	Guard(cmd, wait)
	out = &Writer{Limit: limit}
	errOut = &Writer{Limit: limit}
	cmd.Stdout, cmd.Stderr = out, errOut
	return out, errOut
}

// userinfoRe matches the userinfo of a URL (the "alice:token@" in
// https://alice:token@host/...), including ssh:// and git:// spellings git
// prints. git@host:path SSH syntax has no "://", so it is left alone.
var userinfoRe = regexp.MustCompile(`(?i)((?:https?|ssh|git|ftps?)://)[^/?#\s"]*@`)

// RedactUserinfo strips URL userinfo from s so a credential-bearing remote
// does not land in an error string. Idempotent; strings with no "://" are
// returned unchanged.
func RedactUserinfo(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	return userinfoRe.ReplaceAllString(s, "$1")
}

// FirstLine is the first line of s with userinfo stripped, for error text
// that must not carry a credential or a second line of noise.
func FirstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return RedactUserinfo(line)
}

// CleanPATH filters a PATH string to absolute directories only, dropping empty
// and relative segments so subprocesses never resolve binaries from the
// working directory.
func CleanPATH(raw string) string {
	keep := make([]string, 0, 16)
	for _, dir := range filepath.SplitList(raw) {
		if dir != "" && filepath.IsAbs(dir) {
			keep = append(keep, dir)
		}
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

// AbsPATH returns the current environment's PATH filtered to absolute directories only.
func AbsPATH() string {
	return CleanPATH(os.Getenv("PATH"))
}

// AbsPATHEnv returns the current process environment with PATH replaced by AbsPATH.
func AbsPATHEnv() []string {
	env := os.Environ()
	abs := AbsPATH()
	out := make([]string, 0, len(env)+1)
	seen := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			out = append(out, "PATH="+abs)
			seen = true
			continue
		}
		out = append(out, kv)
	}
	if !seen {
		out = append(out, "PATH="+abs)
	}
	return out
}

// LookPath searches for an executable binary named name across absolute PATH
// directories, returning its absolute path or "" if not found. If name already
// contains a path separator or is absolute, it is returned if it is a regular
// executable file, or "" otherwise.
func LookPath(name string) string {
	if filepath.IsAbs(name) || strings.ContainsRune(name, os.PathSeparator) {
		if fi, err := os.Stat(name); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return name
		}
		return ""
	}
	for _, dir := range filepath.SplitList(AbsPATH()) {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// ShQuote returns a shell-escaped representation of s suitable for use as a single
// argument in POSIX shells (sh, bash, zsh), enclosed in single quotes with interior
// single quotes escaped as '\”.
func ShQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
