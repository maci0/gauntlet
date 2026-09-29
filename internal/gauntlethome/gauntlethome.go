// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gauntlethome resolves the root of gauntlet's state tree: the
// directory holding the run journal, the hot-reload handoff files, and
// agents.json. Every consumer of that root resolves it here, so two
// copies of the rule cannot drift apart.
package gauntlethome

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Dir returns the state root and whether it rests on a usable HOME.
//
// Precedence: GAUNTLET_HOME when set to anything non-empty, else $HOME/.gauntlet.
// A GAUNTLET_HOME that is not already absolute is resolved against the
// working directory once, here, so every later read of the root agrees no
// matter where in the process it happens.
//
// The boolean is false whenever neither source yields a usable root:
// GAUNTLET_HOME unset, empty after expansion, or naming something that is not
// a directory, and no usable HOME either. A root that does not exist yet is
// still a usable root: the journal creates it on first write. A root that
// exists but cannot be stat-ed, behind a permission this process does not hold
// or a symlink loop, is not: it is neither absent nor a directory, and a
// definitions file read from there would let the reviewed tree define its own
// agents, which is what the boolean exists to refuse. Dir then degrades to
// ".gauntlet" beside the working directory, acceptable for the journal (nothing
// it writes is load-bearing) and refused for anything carrying executable argv.
func Dir() (string, bool) {
	if h := strings.TrimSpace(os.Getenv("GAUNTLET_HOME")); h != "" {
		if exp, err := ExpandPath(h); err == nil && strings.TrimSpace(exp) != "" {
			switch fi, err := os.Stat(exp); {
			case err != nil && !os.IsNotExist(err):
				return ".gauntlet", false
			case err == nil && !fi.IsDir():
				return ".gauntlet", false
			}
			return absolute(exp), true
		}
		return ".gauntlet", false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".gauntlet", false
	}
	return filepath.Join(home, ".gauntlet"), true
}

// StateDir is the state root's "state" subdirectory, where the hot-reload
// handoff files live. The layout of the tree belongs here beside the resolver
// of its root, so a caller writing a handoff does not reach through the
// journal for a path the journal does not own.
//
// An unusable root yields the empty string rather than Dir's relative
// fallback: a handoff is load-bearing (a successor without one restarts the
// run), so it must fail to be written instead of landing in a .gauntlet
// beside the working directory, which in a run is the reviewed tree.
func StateDir() string {
	root, ok := Dir()
	if !ok {
		return ""
	}
	return filepath.Join(root, "state")
}

// absolute resolves p against the working directory. If the working directory
// cannot be determined, p passes through unchanged rather than failing a path
// that may well be fine.
func absolute(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// ExpandPath expands $VARIABLES and a leading ~ in a user-supplied path.
//
// Every flag or file entry that takes a directory or file from the user goes
// through this one function, so --dir, --dirs, --log, --prompt-dir, and
// --bin all expand the same way. A bare "~" or "~user" is left alone: only
// "~/..." names something under HOME.
//
// A $VAR or ${VAR} whose environment variable is unset or empty is an error:
// replacing it with nothing would turn --dir $TYPO into the current
// directory, --prompt-dir $TYPO into the bundled prompts, and --log $TYPO
// into a silently dropped log. A leading ~/ with no usable HOME is the same
// class of miss: the path would otherwise be taken relative to cwd.
func ExpandPath(p string) (string, error) {
	var missing []string
	seen := map[string]bool{}
	expanded := os.Expand(p, func(key string) string {
		v, ok := os.LookupEnv(key)
		if !ok || v == "" {
			if !seen[key] {
				seen[key] = true
				missing = append(missing, key)
			}
			return ""
		}
		return v
	})
	if len(missing) == 1 {
		return "", fmt.Errorf("environment variable %s is unset or empty", missing[0])
	}
	if len(missing) > 1 {
		return "", fmt.Errorf("environment variables %s are unset or empty", strings.Join(missing, ", "))
	}
	after, ok := strings.CutPrefix(expanded, "~"+string(os.PathSeparator))
	if !ok {
		return expanded, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot expand ~: home directory is unknown")
	}
	return filepath.Join(home, after), nil
}

// SyncDir flushes a directory entry change (a file created, or a rename that
// replaced one) to stable storage. The state tree's writes are rename-based,
// and a synced temp file is only half of that: without the directory sync the
// rename itself is lost to a power cut, so the state the caller just made
// durable is not there after a reboot.
//
// A kernel that refuses to sync a directory at all reports EINVAL or ENOTSUP;
// there is no entry to flush there and the file contents are already synced,
// so those two are not failures. Every other error is reported.
func SyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	return err
}

// StaleTempAge is how old a leftover temp file beside a rename-based write
// must be before the next write removes it. The window that creates one
// closes when the process does, so anything a day old belongs to a write that
// never finished; the age keeps a concurrent writer's in-flight file safe.
const StaleTempAge = 24 * time.Hour

// WriteFileAtomic writes data to a temp file in dir named by prefix, syncs
// it, and renames it over path. The temp file is removed if anything fails
// and the rename never happens, so a reader sees either the previous file or
// the whole new one, never a truncated one.
func WriteFileAtomic(dir, prefix, path string, data []byte) error {
	tmp, err := NewTempFile(dir, prefix, nil)
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(name) // no-op once the rename succeeded
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// NewTempFile creates a temp file in dir whose name starts with prefix, after
// removing the leftovers there older than StaleTempAge that start with the same
// prefix. The sweep and the new file take one prefix because a caller that
// spells both has to keep them equal: a file created under a prefix the sweep
// does not look for is a leftover nothing will ever remove.
//
// now is the clock the cutoff is measured against, nil meaning time.Now, as in
// SweepStaleTemps.
func NewTempFile(dir, prefix string, now func() time.Time) (*os.File, error) {
	SweepStaleTemps(dir, prefix, StaleTempAge, now)
	return os.CreateTemp(dir, prefix+"*")
}

// SweepStaleTemps removes regular files in dir matching prefix whose modification
// time is older than age. Best effort: failures are ignored.
//
// now is the clock the cutoff is measured against, nil meaning time.Now. The
// cutoff is the one decision here that depends on when the sweep runs, so a
// caller that owns a clock passes it and the sweep boundary is reproducible
// from that clock alone rather than from when it happened to run.
func SweepStaleTemps(dir, prefix string, age time.Duration, now func() time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if now == nil {
		now = time.Now
	}
	cutoff := now().Add(-age)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		// The mode comes from the stat, not from the directory entry type. A
		// readdir that reports no type (some network and FUSE mounts, and
		// XFS without ftype) makes Go hand back ModeIrregular, which reads as
		// "not a regular file", and the sweep would then never remove
		// anything on a tree whose state root is exactly such a mount: the
		// leftovers accumulate beside the binary forever. Info is the lstat
		// Go already performed for the entry's other fields, so it costs
		// nothing extra where the type is known, and it keeps a symlink out
		// of the way because the link itself is what is stat-ed.
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() || fi.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}
