// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/runner"
	"github.com/maci0/gauntlet/internal/selfupdate"
)

// resumeFixture is a repository, three reviews, and a stub claude that logs
// which review each launch was for. Its second launch of a run blocks until
// the release file appears, which is the moment a crash would land.
type resumeFixture struct {
	repo, promptDir, log, started, release string
}

func newResumeFixture(t *testing.T) resumeFixture {
	t.Helper()
	repo, _ := gitRepo(t, "package main\n\nfunc main() {}\n")
	f := resumeFixture{repo: repo, promptDir: t.TempDir()}
	gate := t.TempDir()
	f.log = filepath.Join(gate, "launches")
	f.started = filepath.Join(gate, "started")
	f.release = filepath.Join(gate, "release")
	for _, name := range []string{"a-review", "b-review", "c-review"} {
		body := "Your goal is to test " + name + ".\n"
		if err := os.WriteFile(filepath.Join(f.promptDir, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	stub := `#!/bin/sh
for r in a-review b-review c-review; do
  case "$*" in *"test $r."*) name=$r ;; esac
done
echo "$name" >> '` + f.log + `'
if [ "$(wc -l < '` + f.log + `')" -eq 2 ] && [ ! -e '` + f.release + `' ]; then
  touch '` + f.started + `'
  while [ ! -e '` + f.release + `' ]; do sleep 0.05; done
fi
echo "RESULT: no-changes"
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func (f resumeFixture) argv() []string {
	return []string{"--dirs", f.repo, "--agents", "claude", "--prompt-dir", f.promptDir,
		"--once", "--no-sandbox", "--quiet"}
}

func (f resumeFixture) launches(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

// waitForCheckpoint polls until the run has a checkpoint with one recorded
// result and two unfinished reviews: the state at the moment the second
// review's agent is running.
func waitForCheckpoint(t *testing.T) checkpoint {
	t.Helper()
	dir, err := checkpointDir()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		if len(paths) == 1 {
			if cp, err := readCheckpoint(paths[0]); err == nil {
				for _, dh := range cp.Handoff.Dirs {
					if len(dh.Results) == 1 && len(dh.Pending) == 2 {
						return cp
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no checkpoint with one result and two unfinished reviews appeared")
	return checkpoint{}
}

// A run killed while its second review runs leaves a checkpoint; `gauntlet
// resume` continues it under the same run id, launches only the two reviews
// it had not finished, and closes the run with one index row for all three.
// SIGKILL cannot be sent to an in-process run, so the kill is staged: the
// checkpoint is copied while the review blocks, the run is let finish, and
// what a kill would have left is put back (the checkpoint) or taken away (the
// index row, which a killed run never writes).
func TestResumeContinuesARunCutOffMidReview(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	t.Chdir(t.TempDir())
	f := newResumeFixture(t)

	firstDone := make(chan int, 1)
	go func() { firstDone <- run(f.argv()) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(f.started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second review never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cp := waitForCheckpoint(t)
	cpPath, err := checkpointPath(cp.Handoff.RunID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(cpPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := <-firstDone; code != exitOK {
		t.Fatalf("the first run exited %d", code)
	}
	if _, err := os.Stat(cpPath); !os.IsNotExist(err) {
		t.Fatalf("a run that ended on its own kept its checkpoint (stat err=%v)", err)
	}
	first := f.launches(t)

	// What the kill would have left.
	if err := os.WriteFile(cpPath, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, "index.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.log); err != nil {
		t.Fatal(err)
	}

	var successor int
	reexec = func(_, statePath string, args []string) error {
		t.Setenv("GAUNTLET_STATE", statePath)
		successor = run(args)
		return nil
	}
	t.Cleanup(func() { reexec = selfupdate.Reexec })
	if code := run([]string{"resume", cp.Handoff.RunID}); code != exitOK || successor != exitOK {
		t.Fatalf("resume exited %d, its successor %d", code, successor)
	}

	second := f.launches(t)
	slices.Sort(second)
	unfinished := slices.Clone(first[1:])
	slices.Sort(unfinished)
	if !slices.Equal(second, unfinished) {
		t.Errorf("resume launched %v, want only the unfinished %v (first run launched %v)", second, unfinished, first)
	}
	if _, err := os.Stat(cpPath); !os.IsNotExist(err) {
		t.Errorf("the resumed run kept its checkpoint after finishing (stat err=%v)", err)
	}

	rows := indexRows(t, filepath.Join(home, "index.jsonl"))
	if len(rows) != 1 {
		t.Fatalf("want one index row, got %d: %v", len(rows), rows)
	}
	row := rows[0]
	if row["run_id"] != cp.Handoff.RunID {
		t.Errorf("the resumed run wrote its row under %v, not %s", row["run_id"], cp.Handoff.RunID)
	}
	if row["reviews"] != float64(3) || row["ok"] != float64(3) {
		t.Errorf("the row should count all three reviews as done: reviews=%v ok=%v", row["reviews"], row["ok"])
	}
}

func indexRows(t *testing.T, path string) []map[string]any {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	var rows []map[string]any
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		var row map[string]any
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatalf("index row %q: %v", sc.Text(), err)
		}
		rows = append(rows, row)
	}
	return rows
}

func TestResumeListsCheckpointsAndRefusesUnknownRuns(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	dir := t.TempDir()

	code, out := captureFD(t, &os.Stdout, func() int { return run([]string{"resume"}) })
	if code != exitOK || !strings.Contains(out, "No interrupted runs") {
		t.Fatalf("an empty listing exited %d:\n%s", code, out)
	}

	cp := checkpoint{
		Handoff: handoff{RunID: "20261001T015123Z-1a2b", Dirs: map[string]dirHandoff{
			handoffKey(dir): {Loops: 1, Pending: []string{"b-review", "c-review"}},
		}},
		Argv: []string{"--once", "-j", "4"}, Cwd: dir, Updated: time.Now(),
	}
	if err := saveCheckpoint(cp); err != nil {
		t.Fatal(err)
	}
	code, out = captureFD(t, &os.Stdout, func() int { return run([]string{"resume"}) })
	for _, want := range []string{"20261001T015123Z-1a2b", "ready", "2 unfinished", "1 loop done",
		"gauntlet --once -j 4", "gauntlet resume <run-id>"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing lacks %q:\n%s", want, out)
		}
	}
	if code != exitOK {
		t.Errorf("listing exited %d", code)
	}

	code, _ = captureFD(t, &os.Stderr, func() int { return run([]string{"resume", "20261001T000000Z-dead"}) })
	if code != exitUsage {
		t.Errorf("resuming a run with no checkpoint exited %d, want %d", code, exitUsage)
	}
	code, _ = captureFD(t, &os.Stderr, func() int { return run([]string{"resume", "../escape"}) })
	if code != exitUsage {
		t.Errorf("resuming a malformed run id exited %d, want %d", code, exitUsage)
	}
}

// A directory a live gauntlet holds cannot be resumed into: two agents would
// share the tree.
func TestResumeRefusesARunWhoseDirectoryIsLocked(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	dir := t.TempDir()
	cp := checkpoint{
		Handoff: handoff{RunID: "20261001T015123Z-1a2b", Dirs: map[string]dirHandoff{handoffKey(dir): {}}},
		Cwd:     dir, Updated: time.Now(),
	}
	if err := saveCheckpoint(cp); err != nil {
		t.Fatal(err)
	}
	lock, err := runner.Acquire(runner.LockPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	reexec = func(string, string, []string) error {
		t.Fatal("a locked directory must not reach the exec")
		return nil
	}
	t.Cleanup(func() { reexec = selfupdate.Reexec })
	code, stderr := captureFD(t, &os.Stderr, func() int { return run([]string{"resume", "20261001T015123Z-1a2b"}) })
	if code != exitLocked || !strings.Contains(stderr, "a gauntlet is running in") {
		t.Fatalf("exit %d, want %d:\n%s", code, exitLocked, stderr)
	}
}
