// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/normalize"
)

func TestSandboxConfinesAgentAndChildren(t *testing.T) {
	base := t.TempDir()
	// Make's scratch directory is outside /tmp. Give the sandbox a narrower
	// TMPDIR so the sibling below really is outside every writable root.
	if strings.HasPrefix(base, "/tmp/") || strings.HasPrefix(base, "/private/tmp/") {
		t.Fatal("sandbox test needs make's disk-backed scratch directory outside /tmp")
	}
	work := filepath.Join(base, "work")
	outside := filepath.Join(base, "outside")
	extra := filepath.Join(base, "extra")
	for _, dir := range []string{work, outside, extra} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", work)
	home := filepath.Join(base, "home")
	for _, name := range []string{".codex", ".claude"} {
		if err := os.MkdirAll(filepath.Join(home, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	for _, name := range []string{"truncate", "delete", "rename"} {
		if err := os.WriteFile(filepath.Join(outside, name), []byte("unchanged"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(work, "escape")); err != nil {
		t.Fatal(err)
	}
	script := `set -eu
printf allowed > inside
cat "$1/truncate" > read-copy
sh -c 'printf denied > "$1/new"' sh "$1" && exit 10
sh -c 'printf denied > "$1/truncate"' sh "$1" && exit 11
rm "$1/delete" && exit 12
mv "$1/rename" moved && exit 13
printf denied > escape/new && exit 14
printf granted > "$2/granted"
printf state > "$HOME/.codex/state"
printf denied > "$HOME/.claude/state" && exit 15
printf denied > "$HOME/global" && exit 16
printf finished
`
	bin := fakeAgent(t, work, "agent", script)
	var out strings.Builder
	res := runProc(context.Background(), procOpts{
		Argv: []string{"./" + filepath.Base(bin), outside, extra}, Dir: work, Timeout: 30 * time.Second,
		Tool: "codex", SandboxWrite: []string{extra}, Raw: true,
		Sink: func(l normalize.Line) { out.WriteString(l.Text) },
	})
	if res.Err != nil || res.ExitCode != 0 || !strings.Contains(out.String(), "finished") {
		t.Fatalf("sandbox launch: %+v\n%s", res, out.String())
	}
	for _, name := range []string{"truncate", "delete", "rename"} {
		got, err := os.ReadFile(filepath.Join(outside, name))
		if err != nil || string(got) != "unchanged" {
			t.Fatalf("outside %s changed: %q, %v", name, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatalf("outside creation: %v", err)
	}
	for path, want := range map[string]string{filepath.Join(work, "inside"): "allowed", filepath.Join(work, "read-copy"): "unchanged", filepath.Join(extra, "granted"): "granted", filepath.Join(home, ".codex", "state"): "state"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("allowed %s: %q, %v", path, got, err)
		}
	}
	// Disabling confinement is explicit; it is independent of --yolo.
	res = runProc(context.Background(), procOpts{Argv: []string{"sh", "-c", `printf opted-out > "$1/new"`, "sh", outside}, Dir: work, NoSandbox: true})
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("opt-out: %+v", res)
	}
	// The scheduler itself was never restricted by the child's policy.
	if err := os.WriteFile(filepath.Join(outside, "parent"), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSandboxRejectsInvalidRootBeforeStartingAgent(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	for _, root := range []string{filepath.Join(dir, "missing"), filepath.Join(dir, "file")} {
		if filepath.Base(root) == "file" {
			if err := os.WriteFile(root, nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		res := runProc(context.Background(), procOpts{Argv: []string{"sh", "-c", `touch "$1"`, "sh", marker}, Dir: dir, SandboxWrite: []string{root}})
		if res.Err == nil {
			t.Fatalf("invalid root %s launched agent: %+v", root, res)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("agent started: %v", err)
	}
}

func TestSeatbeltProfileEscapesPaths(t *testing.T) {
	profile, err := seatbeltProfile([]string{`/root/"quoted\path`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile, `(subpath "/root/\"quoted\\path")`) || !strings.Contains(profile, "(deny file-write*)") {
		t.Fatalf("unsafe profile: %s", profile)
	}
	if _, err := seatbeltProfile([]string{"/root/\n(allow default)"}); err == nil {
		t.Fatal("control character accepted")
	}
}

func TestSandboxCommitInLinkedWorktree(t *testing.T) {
	dir := testRepo(t)
	repo := &gitx.Repo{Dir: dir}
	ctx := context.Background()
	wt, err := repo.AddWorktree(ctx, "sandbox", "test", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Remove(ctx)
	t.Setenv("TMPDIR", wt.Dir)
	res := runProc(ctx, procOpts{Argv: []string{"sh", "-c", "printf changed > main.go && git add main.go && git -c commit.gpgsign=false commit -qm changed"}, Dir: wt.Dir, Timeout: 30 * time.Second})
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("linked worktree commit: %+v", res)
	}
}
