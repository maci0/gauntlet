// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func plantCommit(t *testing.T, dir, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "work.txt"),
		[]byte("edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	cmd = exec.Command("git", "commit", "-qm", msg)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
}

// A Cursor-style commit must lose the injected trailers while keeping its
// subject, body, and author.
func TestStripAITrailersRewritesHEAD(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	before, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	plantCommit(t, r.Dir, "fix: guard the nil map write\n\nCo-Authored-By: Cursor <cursoragent@cursor.com>\nGenerated-by: Cursor")
	bodies := func() string {
		return gitOut(t, r.Dir, "log", "-1", "--format=%B")
	}
	if !strings.Contains(bodies(), "cursoragent") {
		t.Fatal("fixture carries no AI trailer, so this proves nothing")
	}
	changed, err := r.StripAITrailers(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("AI trailers present but nothing was stripped")
	}
	body := bodies()
	for _, banned := range []string{"cursoragent", "Co-Authored-By", "Generated-by"} {
		if strings.Contains(body, banned) {
			t.Fatalf("%q survived the strip:\n%s", banned, body)
		}
	}
	if !strings.Contains(body, "fix: guard the nil map write") {
		t.Fatalf("subject lost during the strip:\n%s", body)
	}
	if got := gitOut(t, r.Dir, "log", "-1", "--format=%an <%ae>"); got != "test <test@example.invalid>" {
		t.Fatalf("author changed to %q", got)
	}
}

// Clean commits must not move: amending without need rewrites the SHA for
// nothing and breaks the no-op contract the runner relies on.
func TestStripAITrailersLeavesCleanCommitsAlone(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	before, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	plantCommit(t, r.Dir, "fix: guard the nil map write\n\nSigned-off-by: test <test@example.invalid>")
	tip, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := r.StripAITrailers(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("clean commit was rewritten")
	}
	after, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if after != tip {
		t.Fatal("clean commit moved")
	}
}

// An unchanged HEAD must stay untouched: stripping old history is not this
// step's job.
func TestStripAITrailersSkipsUnmovedHEAD(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	head, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := r.StripAITrailers(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("unmoved HEAD was rewritten")
	}
}
