// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A symlink at the last component is refused on both sides, and the refusal
// names the path: a bare errno leaves an operator with nothing to act on.
func TestRefusesASymlinkAndNamesThePath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		open func(string) error
	}{
		{"OpenRead", func(p string) error { f, _, err := OpenRead(p); return closeErr(f, err) }},
		{"Append", func(p string) error { f, _, err := Append(p, 0o600); return closeErr(f, err) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.open(link)
			if err == nil {
				t.Fatalf("%s followed the symlink at %s", tc.name, link)
			}
			if !strings.Contains(err.Error(), link) {
				t.Fatalf("%s refused without naming the path: %v", tc.name, err)
			}
		})
	}
	// The refusal is what the point of it is: the target is untouched.
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "secret\n" {
		t.Fatalf("the refused write landed in the link's target: %q", body)
	}
}

// A FIFO at the path is refused rather than blocking the caller forever. Both
// opens clear O_NONBLOCK to survive it, and the call has to come back at all:
// a run that hangs on a committed node is a run that never reports.
func TestRefusesAFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	for _, tc := range []struct {
		name string
		open func(string) error
	}{
		{"OpenRead", func(p string) error { f, _, err := OpenRead(p); return closeErr(f, err) }},
		{"Append", func(p string) error { f, _, err := Append(p, 0o600); return closeErr(f, err) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() { done <- tc.open(fifo) }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("%s opened the FIFO at %s", tc.name, fifo)
				}
				if !strings.Contains(err.Error(), fifo) {
					t.Fatalf("%s refused without naming the path: %v", tc.name, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s blocked on the FIFO at %s", tc.name, fifo)
			}
		})
	}
}

// A hardlink is a real regular file and a legitimate way to share one exclude
// between worktrees, so it is read rather than refused.
func TestAcceptsAHardlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("shared\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "hardlink")
	if err := os.Link(target, link); err != nil {
		t.Skipf("link: %v", err)
	}
	f, fi, err := OpenRead(link)
	if err != nil {
		t.Fatalf("OpenRead refused a hardlink: %v", err)
	}
	defer f.Close()
	if !fi.Mode().IsRegular() {
		t.Fatalf("OpenRead returned a %s descriptor", fi.Mode().Type())
	}
	body := make([]byte, fi.Size())
	if _, err := f.Read(body); err != nil {
		t.Fatal(err)
	}
	if string(body) != "shared\n" {
		t.Fatalf("read %q through the hardlink", body)
	}
}

// Append creates the file at perm when it does not exist and appends to it
// when it does, and never truncates: the exclude is a shared file the caller
// adds to.
func TestAppendCreatesAndAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exclude")

	f, _, err := Append(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("one\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("created at %#o, not 0600", fi.Mode().Perm())
	}

	f, _, err = Append(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("two\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "one\ntwo\n" {
		t.Fatalf("append wrote %q", body)
	}
}

// A path that does not exist reports the open, not the stat: there is nothing
// at the path, and the caller asked to open something that was not there.
func TestOpenReadReportsAMissingFile(t *testing.T) {
	_, _, err := OpenRead(filepath.Join(t.TempDir(), "absent"))
	if !os.IsNotExist(err) {
		t.Fatalf("OpenRead on a missing path returned %v", err)
	}
}

// closeErr closes a descriptor an opener handed back with an error, and folds
// the close failure in, so a test that only wants the open's verdict does not
// leak one.
func closeErr(f *os.File, err error) error {
	if f == nil {
		return err
	}
	return errors.Join(err, f.Close())
}
