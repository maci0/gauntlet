// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/report"
	"github.com/maci0/gauntlet/internal/runner"
	"github.com/maci0/gauntlet/internal/selfupdate"
)

// checkpoint is what `gauntlet resume` needs to continue a run whose process
// died without a word: an OOM kill, a crashed desktop session, a power cut. It
// is the hot-reload handoff, rewritten after every recorded result and every
// finished loop, plus the command line and the directory the run started in.
// A run that ends on its own (finished, stopped, or interrupted) deletes it,
// so one still on disk belongs to a process that never got the chance.
type checkpoint struct {
	Handoff handoff   `json:"handoff"`
	Argv    []string  `json:"argv"`
	Cwd     string    `json:"cwd"`
	PID     int       `json:"pid"`
	Version string    `json:"version"`
	Updated time.Time `json:"updated"`
}

// maxCheckpointBytes bounds a checkpoint read, for the same reason the handoff
// read is bounded: a legitimate one is a few kilobytes.
const maxCheckpointBytes = 16 << 20

// checkpointDir is where checkpoints live: beside the reload handoffs but not
// among them, because SaveState sweeps every file in the state dir older than a
// day as a handoff nobody picked up, and a crashed run may wait longer than
// that. The working-directory fallback is refused, as for a handoff: a
// checkpoint carries the argv a resume executes, and that fallback sits inside
// the tree under review, where a repository could plant one.
func checkpointDir() (string, error) {
	if _, ok := gauntlethome.Dir(); !ok {
		return "", errors.New("no usable state root: set GAUNTLET_HOME to a directory this process can write")
	}
	return filepath.Join(gauntlethome.StateDir(), "checkpoints"), nil
}

func checkpointPath(runID string) (string, error) {
	if !journal.ValidRunID(runID) {
		return "", fmt.Errorf("%w: %q", journal.ErrInvalidRunID, runID)
	}
	dir, err := checkpointDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, runID+".json"), nil
}

// saveCheckpoint replaces the run's checkpoint atomically and durably: the
// crash it exists for can be a power cut, and a torn or unrecorded file would
// leave the run with nothing to resume from.
func saveCheckpoint(cp checkpoint) error {
	path, err := checkpointPath(cp.Handoff.RunID)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := gauntlethome.MkdirAllPrivate(dir); err != nil {
		return err
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	if err := gauntlethome.WriteFileAtomic(dir, "."+cp.Handoff.RunID+".json-", path, data); err != nil {
		return err
	}
	return gauntlethome.SyncDir(dir)
}

// dropCheckpoint removes the run's checkpoint once the run has ended on its
// own. A checkpoint that outlived its run would offer to resume work that is
// already done.
func dropCheckpoint(runID string) error {
	path, err := checkpointPath(runID)
	if err != nil {
		return err
	}
	return selfupdate.DropState(path)
}

// readCheckpoint loads one checkpoint, refusing anything but a regular file
// whose recorded run id is the one its name says.
func readCheckpoint(path string) (checkpoint, error) {
	var cp checkpoint
	fi, err := os.Lstat(path)
	if err != nil {
		return cp, err
	}
	if !fi.Mode().IsRegular() {
		return cp, fmt.Errorf("checkpoint %s: not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return cp, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxCheckpointBytes+1))
	_ = f.Close()
	if err != nil {
		return cp, fmt.Errorf("checkpoint %s: %w", path, err)
	}
	if len(data) > maxCheckpointBytes {
		return cp, fmt.Errorf("checkpoint %s exceeds %d bytes", path, maxCheckpointBytes)
	}
	if err := json.Unmarshal(data, &cp); err != nil {
		return cp, fmt.Errorf("checkpoint %s: %w", path, err)
	}
	if want := strings.TrimSuffix(filepath.Base(path), ".json"); cp.Handoff.RunID != want {
		return cp, fmt.Errorf("checkpoint %s records run %q", path, cp.Handoff.RunID)
	}
	return cp, nil
}

// busyDir names a directory of the run that a live gauntlet holds, or ""
// when none does. The lock is the authority rather than the recorded pid: a
// pid gets reused, an flock dies with its process. A directory held by some
// other run blocks a resume just the same, so it is reported the same way.
func busyDir(cp checkpoint) string {
	dirs := make([]string, 0, len(cp.Handoff.Dirs))
	for dir := range cp.Handoff.Dirs {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		lock, err := runner.Acquire(runner.LockPath(dir))
		if errors.Is(err, runner.ErrLocked) {
			return dir
		}
		lock.Release()
	}
	return ""
}

// reexec is the exec a resume ends in, a variable so a test can run the
// successor in-process instead of replacing the test binary.
var reexec = selfupdate.Reexec

// cmdResume lists the runs a crash cut off, or continues one. Continuing is a
// hot reload into this binary from the last checkpoint: the same run id,
// schedule, seed, and carried results, in the directory the run started in,
// with the command line it was started with.
func cmdResume(out io.Writer, pal report.Palette, runID string) int {
	if runID == "" {
		return listCheckpoints(out, pal)
	}
	path, err := checkpointPath(runID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	cp, err := readCheckpoint(path)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "run %s has no checkpoint: it ended on its own, or never recorded a result (see: gauntlet resume)\n", runID)
		return exitUsage
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFail
	}
	if dir := busyDir(cp); dir != "" {
		fmt.Fprintf(os.Stderr, "run %s cannot resume: a gauntlet is running in %s\n", runID,
			normalize.Sanitize(normalize.RedactHome(dir)))
		return exitLocked
	}
	if err := os.Chdir(cp.Cwd); err != nil {
		fmt.Fprintf(os.Stderr, "run %s cannot resume: %v\n", runID, err)
		return exitFail
	}
	h := cp.Handoff
	h.Reloads++
	statePath, err := saveHandoff(runID, h)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run %s cannot resume: cannot save the handoff: %v\n", runID, err)
		return exitFail
	}
	exe, err := os.Executable()
	if err == nil {
		fmt.Fprintf(out, "Resuming run %s in %s: gauntlet %s\n", runID,
			normalize.Sanitize(normalize.RedactHome(cp.Cwd)),
			normalize.Sanitize(strings.Join(journaledArgs(cp.Argv), " ")))
		err = reexec(exe, statePath, cp.Argv)
	}
	if err != nil {
		// No successor will read the handoff, and one left behind would be
		// swept as stale anyway; the checkpoint stays for the next attempt.
		fmt.Fprintf(os.Stderr, "run %s cannot resume: %v\n", runID, err)
		if derr := selfupdate.DropState(statePath); derr != nil {
			fmt.Fprintln(os.Stderr, derr)
		}
		return exitFail
	}
	return exitOK
}

// listCheckpoints prints every run with a checkpoint, newest first, and the
// command that continues it.
func listCheckpoints(out io.Writer, pal report.Palette) int {
	dir, err := checkpointDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFail
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(os.Stderr, err)
		return exitFail
	}
	var cps []checkpoint
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue // a write in progress, or not ours
		}
		cp, err := readCheckpoint(filepath.Join(dir, name))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			continue
		}
		cps = append(cps, cp)
	}
	if len(cps) == 0 {
		fmt.Fprintln(out, "No interrupted runs to resume.")
		return exitOK
	}
	slices.SortFunc(cps, func(a, b checkpoint) int { return b.Updated.Compare(a.Updated) })
	for _, cp := range cps {
		left := 0
		dirs := make([]string, 0, len(cp.Handoff.Dirs))
		for d, dh := range cp.Handoff.Dirs {
			left += len(dh.Pending)
			dirs = append(dirs, normalize.RedactHome(d))
		}
		slices.Sort(dirs)
		state := "ready"
		if busyDir(cp) != "" {
			state = "busy: a gauntlet holds its directory"
		}
		fmt.Fprintf(out, "%s  %s  %s\n", pal.Bold(cp.Handoff.RunID), cp.Updated.Local().Format("2006-01-02 15:04"), state)
		fmt.Fprintf(out, "  %s, %d unfinished in the current loop, %s done\n",
			normalize.Sanitize(strings.Join(dirs, ", ")), left, humanize.Plural(cp.Handoff.Loops(), "loop", "loops"))
		fmt.Fprintf(out, "  gauntlet %s\n", normalize.Sanitize(strings.Join(journaledArgs(cp.Argv), " ")))
	}
	fmt.Fprintln(out, "\nContinue one with: gauntlet resume <run-id>")
	return exitOK
}
