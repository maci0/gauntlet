// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runx

import (
	"context"
	"os/exec"
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
	// Cancel before Start should not error or panic
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
