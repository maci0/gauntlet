// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWriterKeepsThenDiscards(t *testing.T) {
	w := &Writer{Limit: 8}
	n, err := w.Write([]byte("hello world"))
	if err != nil || n != 11 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if !w.Hit || w.String() != "hello wo" {
		t.Fatalf("got %q hit=%v", w.String(), w.Hit)
	}
	n, err = w.Write([]byte("more"))
	if err != nil || n != 4 || w.String() != "hello wo" {
		t.Fatalf("discarded write: %d %v %q", n, err, w.String())
	}
}

func TestRedactUserinfo(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"fatal: not a git repository", "fatal: not a git repository"},
		{"https://github.com/owner/repo.git", "https://github.com/owner/repo.git"},
		{"git@github.com:owner/repo.git", "git@github.com:owner/repo.git"},
		{
			"fatal: unable to access 'https://alice:it's-secret@example.test/repo.git/': 403",
			"fatal: unable to access 'https://example.test/repo.git/': 403",
		},
		{
			"https://alice:secret@part@example.test",
			"https://example.test",
		},
		{
			"https://example.test/path/alice@example.test",
			"https://example.test/path/alice@example.test",
		},
		{
			"https://example.test?email=alice@example.test",
			"https://example.test?email=alice@example.test",
		},
		{
			"https://example.test#alice@example.test",
			"https://example.test#alice@example.test",
		},
		{
			"https://alice:s3cret@github.com/owner/repo.git",
			"https://github.com/owner/repo.git",
		},
		{
			"HTTPS://ALICE:S3CRET@github.com/owner/repo.git",
			"HTTPS://github.com/owner/repo.git",
		},
		{
			"https://alice@github.com/owner/repo.git",
			"https://github.com/owner/repo.git",
		},
		{
			"ssh://alice@github.com/owner/repo.git",
			"ssh://github.com/owner/repo.git",
		},
		{
			"fatal: unable to access 'https://alice:s3cret@github.com/owner/repo.git/': 403",
			"fatal: unable to access 'https://github.com/owner/repo.git/': 403",
		},
		{
			"https://alice:s3cret@github.com/owner/repo.git and https://bob:other@example.test/x.git",
			"https://github.com/owner/repo.git and https://example.test/x.git",
		},
	}
	for _, c := range cases {
		if got := RedactUserinfo(c.in); got != c.want {
			t.Errorf("RedactUserinfo(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := RedactUserinfo(c.want); got != c.want {
			t.Errorf("RedactUserinfo is not idempotent on %q: got %q", c.want, got)
		}
	}
}

func TestFirstLineStripsUserinfo(t *testing.T) {
	in := "https://alice:s3cret@github.com/owner/repo.git\nmore\n"
	if got := FirstLine(in); got != "https://github.com/owner/repo.git" {
		t.Fatalf("FirstLine = %q", got)
	}
}

func TestRedactSecrets(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"fatal: not a git repository", "fatal: not a git repository"},
		{
			"Error: invalid request: Incorrect API key provided: sk-ant-api03-AbCdEf0123456789",
			"Error: invalid request: Incorrect API key provided: " + Redacted,
		},
		{
			"ANTHROPIC_API_KEY=sk-ant-api03-AbCdEf0123456789",
			"ANTHROPIC_API_KEY=" + Redacted,
		},
		{
			`{"client_secret": "abcd1234efgh5678"}`,
			`{"client_secret": "` + Redacted + `"}`,
		},
		{"gh auth login --with-token <<< ghp_0123456789abcdefABCDEF", "gh auth login --with-token <<< " + Redacted},
		{"github_pat_11ABCDEFG0abcdefghijkl_0123456789abcdefghijklmnopqrstuvwxyz0123456789", Redacted},
		{"AKIAIOSFODNN7EXAMPLE", Redacted},
		// Too short to be a credential, or named by a flag rather than a
		// secret-bearing variable: redacting these would cost the note its
		// meaning and leak nothing.
		{"--token-budget=1000000", "--token-budget=1000000"},
		{"--max-tokens=4096", "--max-tokens=4096"},
		{"GITHUB_TOKEN=<redacted>", "GITHUB_TOKEN=" + Redacted},
		{"token=x", "token=x"},
	}
	for _, c := range cases {
		if got := RedactSecrets(c.in); got != c.want {
			t.Errorf("RedactSecrets(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := RedactSecrets(c.want); got != c.want {
			t.Errorf("RedactSecrets is not idempotent on %q: got %q", c.want, got)
		}
	}
}

func TestFirstLineRedactsCredentialsAndStopsAtTheNewline(t *testing.T) {
	in := "authentication failed: ANTHROPIC_API_KEY=sk-ant-api03-AbCdEf0123456789\nmore\n"
	want := "authentication failed: ANTHROPIC_API_KEY=" + Redacted
	if got := FirstLine(in); got != want {
		t.Fatalf("FirstLine = %q, want %q", got, want)
	}
}

func TestCleanPATH(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{":/usr/bin::./bin:bin:/usr/local/bin:", "/usr/bin:/usr/local/bin"},
		{"/bin", "/bin"},
		{"relative/path:.:", ""},
	}
	for _, c := range cases {
		if got := CleanPATH(c.in); got != c.want {
			t.Errorf("CleanPATH(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWriterUnlimitedAndBytes(t *testing.T) {
	w := &Writer{Limit: 0}
	n, err := w.Write([]byte("hello world"))
	if err != nil || n != 11 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if w.Hit || string(w.Bytes()) != "hello world" || w.String() != "hello world" {
		t.Fatalf("unlimited write: hit=%v, bytes=%q", w.Hit, string(w.Bytes()))
	}
	n, err = w.Write([]byte(" more"))
	if err != nil || n != 5 || string(w.Bytes()) != "hello world more" {
		t.Fatalf("second write: %d %v %q", n, err, string(w.Bytes()))
	}
}

func TestGuard(t *testing.T) {
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "sleep", "5")
	wait := 250 * time.Millisecond
	Guard(cmd, wait)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("Guard must set Setpgid = true")
	}
	if cmd.WaitDelay != wait {
		t.Fatalf("WaitDelay = %v, want %v", cmd.WaitDelay, wait)
	}
	if cmd.Cancel == nil {
		t.Fatal("Guard must set Cancel func")
	}
	if err := cmd.Cancel(); err != nil {
		t.Fatalf("Cancel before start = %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Cancel(); err != nil {
		t.Fatalf("Cancel running process = %v", err)
	}
	_ = cmd.Wait()

	// A zero or negative PID must not reach syscall.Kill: the group id is
	// negated before the call, so -0 and --1 name the caller's own process
	// group and would kill this test binary. The only way to see that is to
	// leave a real child running and check it afterwards, since Cancel
	// returns nil either way.
	victim := exec.Command("sleep", "30")
	victim.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := victim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = victim.Process.Kill()
		_ = victim.Wait()
	})

	for _, pid := range []int{0, -1} {
		dummy := exec.Command("sleep", "5")
		Guard(dummy, wait)
		dummy.Process = &os.Process{Pid: pid}
		if err := dummy.Cancel(); err != nil {
			t.Fatalf("Cancel with pid %d = %v", pid, err)
		}
		if err := victim.Process.Signal(syscall.Signal(0)); err != nil {
			t.Fatalf("Cancel with pid %d signalled the caller's process group: %v", pid, err)
		}
	}
}

func TestBound(t *testing.T) {
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "sh", "-c", "printf '1234567890extra'; printf 'abcdefghijextra' >&2")
	out, errOut := Bound(cmd, 10, time.Second)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !out.Hit || out.String() != "1234567890" || string(out.Bytes()) != "1234567890" {
		t.Fatalf("out: hit=%v str=%q bytes=%q", out.Hit, out.String(), string(out.Bytes()))
	}
	if !errOut.Hit || errOut.String() != "abcdefghij" || string(errOut.Bytes()) != "abcdefghij" {
		t.Fatalf("errOut: hit=%v str=%q bytes=%q", errOut.Hit, errOut.String(), string(errOut.Bytes()))
	}
}

func TestAbsPATH(t *testing.T) {
	t.Setenv("PATH", ":/usr/bin::./local:/bin:")
	got := AbsPATH()
	want := "/usr/bin:/bin"
	if got != want {
		t.Fatalf("AbsPATH = %q, want %q", got, want)
	}
}

func TestAbsPATHFallsBackWhenUnset(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")

	got := AbsPATH()
	if !strings.HasPrefix(got, bin+string(os.PathListSeparator)) {
		t.Fatalf("AbsPATH with an empty PATH = %q, want it to lead with the $HOME fallback %q", got, bin)
	}
	// The fallback is a PATH like any other, so it holds only real absolute
	// directories: a relative or empty segment would resolve in the tree a
	// review chdirs into.
	for _, dir := range filepath.SplitList(got) {
		if dir == "" || !filepath.IsAbs(dir) {
			t.Fatalf("fallback carries the non-absolute segment %q", dir)
		}
	}
}

func TestLookPathFindsGitWithoutPATH(t *testing.T) {
	// git is what every run needs first, and with no PATH to search it was
	// the one executable the empty-PATH fallback did not reach: a box that
	// launchd or systemd started found its agent CLI and then failed with
	// "git is not available".
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("PATH", "")
	if got := LookPath("git"); got == "" {
		t.Fatal("LookPath(git) with an empty PATH = \"\", want the system prefix to be searched")
	}
}

func TestAbsPATHEnv(t *testing.T) {
	t.Setenv("PATH", ":/usr/bin::./local:/bin:")
	env := AbsPATHEnv()
	var got string
	found := false
	for _, kv := range env {
		if after, ok := strings.CutPrefix(kv, "PATH="); ok {
			got = after
			found = true
			break
		}
	}
	if !found {
		t.Fatal("PATH not found in AbsPATHEnv output")
	}
	want := "/usr/bin:/bin"
	if got != want {
		t.Fatalf("PATH in AbsPATHEnv = %q, want %q", got, want)
	}
}

func TestLookPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "mytool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := LookPath("mytool"); got != bin {
		t.Fatalf("LookPath(mytool) = %q, want %q", got, bin)
	}
	if got := LookPath(bin); got != bin {
		t.Fatalf("LookPath(%q) = %q, want %q", bin, got, bin)
	}
	if got := LookPath("nonexistent"); got != "" {
		t.Fatalf("LookPath(nonexistent) = %q, want empty", got)
	}

	// A bare "./name" has to reach the relative branch: filepath.Join
	// cleans it to "name", which is the plain PATH lookup asserted above and
	// would leave the absolute-path conversion untested.
	relBin := "./mytool"
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if got := LookPath(relBin); got != bin {
		t.Fatalf("LookPath(%q) = %q, want absolute %q", relBin, got, bin)
	}
}

// A path that names a directory or a file without an execute bit is not
// runnable, and LookPath says so with an empty result rather than handing the
// caller something exec will fail on later.
func TestLookPathRejectsWhatItCannotRun(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(plain, []byte("not a program\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "bin")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{dir, sub, plain, "./notes.txt", filepath.Join(sub, "..", "notes.txt")} {
		if got := LookPath(name); got != "" {
			t.Errorf("LookPath(%q) = %q, want empty", name, got)
		}
	}
}

// A caller that memoizes an answer under the PATH it read has to look the name
// up on that PATH: LookPathIn is the only entry point that resolves against the
// string it is handed rather than against whatever the environment says now.
func TestLookPathInUsesTheGivenPATH(t *testing.T) {
	given := t.TempDir()
	other := t.TempDir()
	for _, dir := range []string{given, other} {
		bin := filepath.Join(dir, "mytool")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", other)
	want := filepath.Join(given, "mytool")
	if got := LookPathIn(given, "mytool"); got != want {
		t.Fatalf("LookPathIn(%q, mytool) = %q, want %q", given, got, want)
	}
	if got := LookPathIn(given, "nonexistent"); got != "" {
		t.Fatalf("LookPathIn over an empty directory = %q, want empty", got)
	}
	if got := LookPath("mytool"); got != filepath.Join(other, "mytool") {
		t.Fatalf("LookPath reads PATH=other: = %q, want %q", got, filepath.Join(other, "mytool"))
	}
}

func TestShQuote(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "''"},
		{"simple", "'simple'"},
		{"has spaces", "'has spaces'"},
		{"it's working", `'it'\''s working'`},
		{"$(touch /tmp/pwn)", `'$(touch /tmp/pwn)'`},
		{"`rm -rf /`", "'`rm -rf /`'"},
		{`"double quotes"`, `'"double quotes"'`},
		{"multi'quote'test", `'multi'\''quote'\''test'`},
	}
	for _, tc := range cases {
		if got := ShQuote(tc.in); got != tc.want {
			t.Errorf("ShQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOutcomeNamesTheDeadline(t *testing.T) {
	if err := Outcome(context.Background(), nil); err != nil {
		t.Fatalf("a successful run reported %v", err)
	}
	// A child that failed on its own keeps its own error, untouched.
	own := errors.New("exit status 128")
	if got := Outcome(context.Background(), own); !errors.Is(got, own) {
		t.Fatalf("Outcome = %v, want the child's own error", got)
	}
	// A child the deadline killed must not read as a crash: "signal: killed"
	// says nothing, and the commonest cause is a hung remote.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	killed := errors.New("signal: killed")
	got := Outcome(ctx, killed)
	if !errors.Is(got, context.Canceled) {
		t.Errorf("Outcome = %v, want it to report the cancel", got)
	}
	if !errors.Is(got, killed) {
		t.Errorf("Outcome = %v, want it to keep the child's error", got)
	}
	if !strings.Contains(got.Error(), "context canceled") {
		t.Errorf("Outcome = %q, want the cancel named in the text", got)
	}
}

func TestKillGroup(t *testing.T) {
	KillGroup(nil, syscall.SIGKILL)
	KillGroup(&exec.Cmd{}, syscall.SIGKILL)
	KillGroup(&exec.Cmd{Process: &os.Process{Pid: 0}}, syscall.SIGKILL)
	KillGroup(&exec.Cmd{Process: &os.Process{Pid: -1}}, syscall.SIGKILL)

	// Same contract as the Cancel case above, observed the only way it can
	// be: a live child in its own group must survive a call that names a
	// caller-scoped pid.
	victim := exec.Command("sleep", "30")
	victim.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := victim.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = victim.Process.Kill()
		_ = victim.Wait()
	}()
	for _, pid := range []int{0, -1} {
		KillGroup(&exec.Cmd{Process: &os.Process{Pid: pid}}, syscall.SIGKILL)
		if err := victim.Process.Signal(syscall.Signal(0)); err != nil {
			t.Fatalf("KillGroup with pid %d signalled the caller's process group: %v", pid, err)
		}
	}

	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	KillGroup(cmd, syscall.SIGKILL)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected killed process to return exit error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("process was not terminated by KillGroup")
	}
}
