// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package runx is the shared subprocess kill and output-cap plumbing.
//
// Every external binary gauntlet launches (git, gh, a usage probe, a dsh
// config dump, the indexer) gets its own process group so a deadline kill
// takes children with it, a WaitDelay so a grandchild holding the pipes
// cannot park the caller, and, when the output is captured, a cap so a
// hostile listing cannot fill RAM.
package runx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// WaitGrace is how long Wait may sit on output pipes a grandchild of the
// killed child still holds before Guard and Bound give up on it. Every child
// launched through this package is passed this, so one unreapable helper
// costs every caller the same bound rather than each spelling its own.
const WaitGrace = 10 * time.Second

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

// KillGroup signals the whole process group, falling back to the process itself
// when the group is already gone.
func KillGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err == nil {
		return
	}
	_ = cmd.Process.Signal(sig)
}

// Outcome classifies a failed run so a deadline kill does not read like a
// crash. A child the caller's own deadline killed returns "signal: killed" or
// "exit status 128", which no operator can tell apart from a genuine failure
// and which makes the commonest cause (a hung remote, a wedged binary)
// unreadable. Naming the deadline is what turns that into a diagnosable
// report, and wrapping both errors keeps errors.Is(err,
// context.DeadlineExceeded) working for a caller that classifies. Returns nil
// for a successful run.
func Outcome(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", ctxErr, err)
	}
	return err
}

// Guard puts cmd in its own process group, kills the group on Cancel, and
// bounds how long Wait may sit on output pipes a grandchild still holds.
func Guard(cmd *exec.Cmd, wait time.Duration) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		KillGroup(cmd, syscall.SIGKILL)
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

// defaultPathDirs are the absolute directories a process with no PATH falls
// back to. The list names both claimed platforms' prefixes rather than one of
// them: the Linux default alone (/usr/local/bin:/usr/bin:/bin) finds no
// Homebrew install on Apple Silicon, where the prefix is /opt/homebrew/bin,
// so a lookup would report nothing on a fully stocked Mac.
var defaultPathDirs = []string{
	"/opt/homebrew/bin", // Homebrew, Apple Silicon
	"/usr/local/bin",    // Homebrew, Intel; also the Linux default
	"/usr/bin",
	"/bin",
	"/opt/homebrew/sbin", // tools that install their helpers here
	"/usr/local/sbin",
	"/usr/sbin",
	"/sbin",
}

// defaultPath is the substitute for an empty PATH. launchd, systemd, and
// `env -i` all hand a program none, and searching nothing then reports every
// agent, and git itself, missing on a machine where all of them are
// installed. $HOME/.local/bin leads because that is where this project's own
// install target and the README put the binary. Directories that do not exist
// are dropped, so the result names only real absolute paths and a chdir cannot
// change what it means.
func defaultPath() string {
	dirs := make([]string, 0, len(defaultPathDirs)+1)
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	for _, dir := range defaultPathDirs {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

// AbsPATH returns the environment's PATH filtered to absolute directories
// only, or the system prefixes when PATH is unset or empty.
//
// The fallback lives here rather than in one caller because PATH is resolved
// for every executable this process launches, and the rule has to be the same
// for all of them: with PATH empty and the fallback applied to agents alone,
// a box that launchd started finds the agent CLI and then fails every run with
// "git is not available", which reads as a broken install rather than a
// missing variable.
func AbsPATH() string {
	raw := os.Getenv("PATH")
	if raw == "" {
		raw = defaultPath()
	}
	return CleanPATH(raw)
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

// executable reports whether path names a regular file with an execute bit,
// which is the one definition of "runnable" every candidate here answers to.
func executable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// LookPath searches for an executable binary named name across absolute PATH
// directories, returning its absolute path or "" if not found. If name already
// contains a path separator or is absolute, it is returned if it is a regular
// executable file, or "" otherwise.
func LookPath(name string) string {
	if filepath.IsAbs(name) {
		if executable(name) {
			return name
		}
		return ""
	}
	if strings.ContainsAny(name, "/\\") {
		abs, err := filepath.Abs(name)
		if err != nil {
			return ""
		}
		if executable(abs) {
			return abs
		}
		return ""
	}
	for _, dir := range filepath.SplitList(AbsPATH()) {
		if p := filepath.Join(dir, name); executable(p) {
			return p
		}
	}
	return ""
}

// ShQuote returns a shell-escaped representation of s suitable for use as a single
// argument in POSIX shells (sh, bash, zsh), enclosed in single quotes with interior
// single quotes escaped.
func ShQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
