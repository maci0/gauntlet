// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/prompt"
)

// The trailer strip rewrites HEAD, so it may only ever touch a commit the
// commit step itself wrote. The tip read before the step is what proves that,
// and when the read fails there is no proof to go on: an empty `before` sends
// the strip into its "clean HEAD whatever it is" mode, which amends whatever
// commit is at HEAD even though nothing established that this step wrote it.
// The launch still runs and still commits, so the repair is to skip the amend
// rather than to abandon the work.
func TestCommitStepSkipsStripWhenHEADCannotBeRead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for runner tests")
	}
	// An unborn HEAD is the honest way to make the tip read fail: the
	// repository has no commit yet, so `rev-parse --verify HEAD` does not.
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(repo, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := fakeAgent(t, t.TempDir(), "claude", `
git add -A
git commit -qm "first commit

Co-Authored-By: Cursor <cursoragent@cursor.com>"
exit 0`)

	cfg := baseConfig(t, repo, prompt.Set{}, []string{"a-review"}, bin)
	cfg.Commit = true

	bus := NewBus()
	defer bus.Close()
	drain(bus)
	r, err := New(t.Context(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	r.runCommitStep(t.Context())

	body := gitOut(t, repo, "log", "-1", "--format=%B")
	if !strings.Contains(body, "Co-Authored-By") {
		t.Fatalf("the strip ran against a HEAD it could not prove this step wrote:\n%s", body)
	}
	if !strings.Contains(body, "first commit") {
		t.Fatalf("the commit step did not land its work at all:\n%s", body)
	}
}
