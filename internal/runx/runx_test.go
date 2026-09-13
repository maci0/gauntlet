// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runx

import "testing"

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
