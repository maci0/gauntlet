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
	"errors"
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
// launched through this package takes this bound unless it has a reason to
// name a shorter one, so one unreapable helper costs every caller the same
// bound rather than each spelling its own.
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
// when the group is already gone. It returns the last failure so a caller that
// reports a clean timeout can tell the difference between a child that died
// from one nothing managed to signal; a subtree that outlived its kill is not
// something the caller learns from the exit status.
func KillGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err == nil {
		return nil
	}
	return cmd.Process.Signal(sig)
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
//
// A cancel that could not signal anything is reported to Wait rather than
// swallowed: the caller is about to name a timeout, and a tree that survived
// the kill is the one case where that name would be wrong. A process that
// exited between the deadline and the signal is not a failure, so
// os.ErrProcessDone stays nil.
func Guard(cmd *exec.Cmd, wait time.Duration) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := KillGroup(cmd, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
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

var (
	// secretAssignRe is a NAME=value or "NAME": "value" pair whose name says
	// the value is a credential. The value is a run of characters a shell or a
	// JSON body would carry one in, so an export line, an env dump, and a
	// config error all match.
	secretAssignRe = regexp.MustCompile(
		`(?i)\b([A-Z0-9_]*(?:API_?KEY|SECRET|TOKEN|PASSWORD|PASSWD|CREDENTIALS?)[A-Z0-9_]*)"?` +
			`\s*[:=]\s*"?([^\s",;']+)("?)`)
	// secretPrefixRe is a credential recognized by its own fixed prefix, with
	// no name to key off. The prefixes are the published formats of the tokens
	// a coding agent holds; each is long and the character after it is
	// specific enough that ordinary prose and identifiers do not match.
	secretPrefixRe = regexp.MustCompile(
		`\b(?:sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}` +
			`|glpat-[A-Za-z0-9_-]{16,}|xox[bp]-[A-Za-z0-9-]{16,}|AKIA[0-9A-Z]{16})\b`)

	// secretFlagRe is a credential passed the way a command line passes one:
	// as the value after a flag whose name says what it carries. The
	// assignment rule above needs NAME=value, which is the spelling an export
	// line and a JSON body use, so on a persisted argv it caught "--api-key=…"
	// and missed "--api-key …" -- the form a shell actually produces, and the
	// one every agent CLI documents.
	//
	// The flag names are enumerated rather than pattern-matched. The open
	// spelling ("--anything-TOKEN …") is what this file's other patterns use,
	// and it is wrong here: gauntlet itself has --token-budget, whose value is
	// a count, and redacting it would cost the run's record its meaning for
	// nothing. A fixed list of the names that carry a credential cannot pick
	// up the next budget flag.
	secretFlagRe = regexp.MustCompile(
		`(?i)(-{1,2}(?:api[-_]?key|auth[-_]?token|access[-_]?token|bearer[-_]?token|` +
			`bot[-_]?token|auth|credential|credentials|password|passwd|secret|token))\s+` +
			`([^\s"']+)`)

	// secretAuthRe is a credential in the one spelling the header carries it:
	// a name, a scheme, and an opaque value. The value of an Authorization
	// header is whatever the issuer minted -- a JWT, a session id, an opaque
	// random string -- so secretPrefixRe cannot recognize it by shape and
	// secretAssignRe cannot reach it, because the scheme sits between the
	// name and the value ("Authorization: Bearer <token>"). The header name is
	// the only thing that says what it is, and a rejected request is reported
	// by printing the request, which is the line that reaches an error string,
	// a report, and the run journal.
	//
	// The scheme is required, which is what the grammar says: a header's value
	// is `auth-scheme [ 1*SP ( token68 / #auth-param ) ]`, so there is no
	// spelling that carries a credential without one. Requiring it is also
	// what keeps a bare word from being taken for one -- "authorization:
	// required" in a status line and "Authorization: Negotiate" naming the
	// scheme of a challenge are both left whole. A scheme with no value after
	// it does not match either, so a challenge that names only its scheme
	// reads as it was written.
	secretAuthRe = regexp.MustCompile(
		`(?i)\b((?:proxy-)?authorization)\s*[:=]\s*` +
			`[A-Za-z][A-Za-z0-9+._-]{0,15}\s+([^\s"',;]+)`)
)

// secretValueMin is the shortest value RedactSecrets replaces. A shorter
// one is a flag, a placeholder, or a path fragment rather than a credential,
// and redacting those would cost the note its meaning for nothing.
const secretValueMin = 8

// Redacted is what a credential is replaced with. It carries no shape any
// pattern here matches, so redaction is idempotent.
const Redacted = "[redacted]"

// RedactSecrets replaces credentials in s: a value assigned to a name that says
// it is one, the value after a flag that names a credential, the value of an
// Authorization header, a token recognized by the fixed prefix its issuer
// gives it, and the userinfo of a URL.
//
// It is for text that came out of a child process, and for the command line a
// run journals. Every launch gauntlet makes runs an agent or a helper with the
// operator's credentials in its environment, and a rejected key is reported by
// printing it: the line then reaches an error string, a report, and the run
// journal, all of which outlive the run and are read by people who are not the
// operator. RedactUserinfo covers the one shape git prints, and cannot see
// this one.
func RedactSecrets(s string) string {
	// A URL carrying a password is a credential whoever wrote the line, and the
	// text reaching this function is not always the single line FirstLine has
	// already cleaned: a persisted argv is one argument, not one line. The
	// userinfo runs to the closing "@", so the scheme, the host, and the path
	// around it are kept and only the part that authenticates is not.
	s = userinfoRe.ReplaceAllString(s, "$1")
	s = secretFlagRe.ReplaceAllStringFunc(s, func(m string) string {
		g := secretFlagRe.FindStringSubmatchIndex(m)
		// g[4]:g[5] is the value, the second group; the flag name and the
		// whitespace between them are kept, so the line reads as the command
		// it was rather than losing the option that carried the key.
		if g == nil || g[5]-g[4] < secretValueMin {
			return m
		}
		return m[:g[4]] + Redacted
	})
	s = secretAssignRe.ReplaceAllStringFunc(s, func(m string) string {
		g := secretAssignRe.FindStringSubmatchIndex(m)
		if g == nil || g[5]-g[4] < secretValueMin {
			return m
		}
		// Everything but the value is kept (the colon, the equals, the
		// quotes): this rewrites a credential out of the line, not the line's
		// shape.
		return m[:g[4]] + Redacted + m[g[6]:g[7]]
	})
	s = secretAuthRe.ReplaceAllStringFunc(s, func(m string) string {
		g := secretAuthRe.FindStringSubmatchIndex(m)
		// g[4]:g[5] is the credential, the second group; the header name and
		// the scheme in front of it are kept, so the line still reads as the
		// request that was rejected rather than as a bare "[redacted]".
		if g == nil || g[5]-g[4] < secretValueMin {
			return m
		}
		return m[:g[4]] + Redacted
	})
	return secretPrefixRe.ReplaceAllString(s, Redacted)
}

// FirstLine is the first line of s with credentials stripped, for error text
// that must not carry a secret or a second line of noise. Every piece of child
// output that becomes an error string passes through it.
func FirstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return RedactSecrets(RedactUserinfo(line))
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

// EnvIn is AbsPATHEnv for a child whose working directory is dir, with PWD
// naming dir. os/exec updates PWD itself only when Cmd.Env is nil, so a child
// handed an explicit environment inherits this process's PWD, and a runtime
// that resolves its project from PWD before getcwd (opencode does) then works
// in the operator's checkout instead of the worktree it was launched in. A dir
// that cannot be made absolute drops PWD, which leaves the child on getcwd.
func EnvIn(dir string) []string {
	env := AbsPATHEnv()
	if dir == "" {
		return env
	}
	out := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PWD=") {
			out = append(out, kv)
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		out = append(out, "PWD="+abs)
	}
	return out
}

// executable reports whether path names a regular file with an execute bit,
// which is the one definition of "runnable" every candidate here answers to.
func executable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// LookPath searches for an executable binary named name across the absolute
// directories of the current PATH, returning its absolute path or "" if not
// found.
func LookPath(name string) string {
	return LookPathIn(AbsPATH(), name)
}

// LookPathIn is LookPath over an explicit PATH, for a caller that memoizes the
// answer under the PATH it read. Looking the name up against the ambient PATH
// instead leaves the key and the value taken from two different reads of a
// mutable input: a PATH that changed in between files an answer computed for
// one machine under the key of another.
//
// If name already contains a path separator or is absolute, it is returned if it
// is a regular executable file, or "" otherwise.
func LookPathIn(path, name string) string {
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
	for _, dir := range filepath.SplitList(path) {
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
