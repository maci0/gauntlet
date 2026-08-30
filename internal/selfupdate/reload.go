// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// stateEnv names the handoff file passed to the reloaded process.
const stateEnv = "GAUNTLET_STATE"

// fingerprint identifies one build of the executable on disk.
type fingerprint struct {
	inode uint64
	size  int64
	mtime time.Time
}

func (f fingerprint) valid() bool { return f.size > 0 }

func stat(path string) (fingerprint, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fingerprint{}, err
	}
	var ino uint64
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		ino = st.Ino
	}
	return fingerprint{inode: ino, size: fi.Size(), mtime: fi.ModTime()}, nil
}

// Watcher notices when the running executable is replaced on disk.
//
// Polling beats an inotify dependency here: the question is asked every few
// seconds, the answer is one stat, and a missed event only delays a reload
// until the next tick.
type Watcher struct {
	path   string
	every  time.Duration
	base   fingerprint
	Change <-chan string // receives the executable path once it changes
}

// Watch starts watching the current executable. A zero interval means 5s.
func Watch(ctx context.Context, every time.Duration) (*Watcher, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	base, err := stat(self)
	if err != nil {
		return nil, err
	}
	if every <= 0 {
		every = 5 * time.Second
	}
	ch := make(chan string, 1)
	w := &Watcher{path: self, every: every, base: base, Change: ch}
	go w.run(ctx, ch)
	return w, nil
}

func (w *Watcher) run(ctx context.Context, ch chan<- string) {
	defer close(ch)
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cur, err := stat(w.path)
			if err != nil || !cur.valid() {
				continue // mid-rename: the next tick sees the new file
			}
			if cur == w.base {
				continue
			}
			// Re-stat at once and require the same reading: a file caught
			// mid-rename comes back different or missing. The double read is
			// not proof the new binary is complete, only that it has settled;
			// writers here (self-update, go build) rename atomically, so a
			// stable fingerprint means a whole file.
			settled, err := stat(w.path)
			if err != nil || settled != cur {
				continue
			}
			w.base = cur
			select {
			case ch <- w.path:
			default:
			}
			return
		}
	}
}

// SaveState writes the handoff blob the reloaded process picks up, and returns
// its path.
//
// The blob is written to a sibling temp file, synced, and renamed into place:
// the exec follows immediately, so a kill mid-write must leave either the
// previous state or none, never a truncated file. LoadState cannot parse a
// torn blob, and an unparseable handoff silently restarts every loop from
// zero.
//
// Handoffs whose successor never ran are swept here: a handoff is meant to be
// read moments after it is written, and one that outlives the retention window
// belongs to a reload that died between the save and the exec, which nothing
// will ever pick up again.
func SaveState(dir, runID string, v any) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sweepStaleHandoffs(dir)
	path := filepath.Join(dir, runID+".json")
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "."+runID+".json-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(name) // no-op once the rename succeeded
	}()
	if _, err := tmp.Write(data); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(name, path); err != nil {
		return "", err
	}
	return path, nil
}

// sweepStaleHandoffs removes handoff files whose successor never ran. The same
// crash shape as sweepStaleTemps in selfupdate.go: a kill, an OOM, a power cut,
// or an exec failure skips every defer, and a handoff no future process will
// ever read is garbage. The state dir is gauntlet's own, so every regular file
// in it is a handoff or the temp of one that died mid-write; anything older
// than the shared retention window is a corpse. Best effort by design, like
// the temp sweep: one that cannot run must not block the reload.
func sweepStaleHandoffs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleTempAge)
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil || fi.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// LoadState reads and removes the handoff blob named by GAUNTLET_STATE. It
// reports whether state was found: a normal start simply has none.
func LoadState(v any) bool {
	path := os.Getenv(stateEnv)
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	// Read once, then drop it: a stale handoff must not resurrect old counters
	// on the next manual start.
	_ = os.Remove(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, v) == nil
}

// Reexec replaces this process with the binary at path, keeping the original
// arguments and adding the handoff file to the environment.
//
// execve, not fork: the pid, the terminal, and the exit status all stay the
// same, so a supervisor or a shell job sees one continuous process.
func Reexec(path, statePath string, args []string) error {
	if path == "" {
		return errors.New("no executable to reload")
	}
	env := os.Environ()
	filtered := env[:0]
	for _, kv := range env {
		if len(kv) > len(stateEnv) && kv[:len(stateEnv)+1] == stateEnv+"=" {
			continue
		}
		filtered = append(filtered, kv)
	}
	if statePath != "" {
		filtered = append(filtered, stateEnv+"="+statePath)
	}
	// The successor continues this run, so it is handed the arguments this
	// process is actually running, not the ones it was typed with: a run
	// composed by the launcher would otherwise reopen the launcher, and a
	// `--suggest` run would ask an agent to choose all over again.
	argv := append([]string{path}, args...)
	if err := syscall.Exec(path, argv, filtered); err != nil {
		return fmt.Errorf("exec %s: %w", path, err)
	}
	return nil // unreachable on success
}
