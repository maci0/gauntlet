// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPorcelainPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{" M main.go", "main.go"},
		{"M  main.go", "main.go"},
		{"?? untracked.go", "untracked.go"},
		{"R  old.txt -> new.txt", "new.txt"},
		{"C  orig.txt -> copy.txt", "copy.txt"},
		// An untracked file may legitimately contain the arrow in its name.
		{"?? a -> b", "a -> b"},
		// Porcelain quotes paths containing special characters.
		{`?? "sp ace.go"`, "sp ace.go"},
		{"", ""},
		{" M", ""},
	}
	for _, c := range cases {
		if got := porcelainPath(c.in); got != c.want {
			t.Errorf("porcelainPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBranchSlug(t *testing.T) {
	cases := map[string]string{
		"sec-review": "sec-review",
		"A B/c.d":    "A-B-c-d",
		"--xx--":     "xx",
		"***":        "review", // an all-mangled name must still be a valid ref
		"":           "review",
		"unicodeéé":  "unicode",
	}
	for in, want := range cases {
		if got := branchSlug(in); got != want {
			t.Errorf("branchSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCountLines(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := map[string]struct {
		body string
		want int
	}{
		"trailing newline":  {"one\ntwo\n", 2},
		"partial last line": {"one\ntwo", 2},
		"empty":             {"", 0},
		"single line":       {"only", 1},
		"binary is refused": {"text\x00more\n", 0},
	}
	for name, c := range cases {
		if got := countLines(write(strings.NewReplacer(" ", "_").Replace(name)+".txt", c.body)); got != c.want {
			t.Errorf("%s: countLines = %d, want %d", name, got, c.want)
		}
	}

	// A symlink must not be followed: O_NOFOLLOW refuses it at open time.
	target := write("target.txt", "line\n")
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := countLines(link); got != 0 {
		t.Errorf("a symlink counted %d lines; it must be refused outright", got)
	}
}

// newRepo makes a git repository with one commit and opens a handle on it.
func newRepo(t *testing.T) *Repo {
	t.Helper()
	if !Available() {
		t.Skip("git is required for gitx tests")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")

	r := Open(dir)
	if !r.HasBaseline() {
		t.Fatal("a committed repo should have a baseline")
	}
	return r
}

func TestSampleCountsUntrackedAndSkipsOwnArtifacts(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	newFile := filepath.Join(r.Dir, "new.go")
	if err := os.WriteFile(newFile, []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, ok := r.Sample(ctx, nil)
	if !ok || st.Ins != 5 || st.Del != 0 {
		t.Fatalf("untracked lines should count as insertions, got %+v ok=%v", st, ok)
	}

	real, err := filepath.EvalSymlinks(newFile)
	if err != nil {
		t.Fatal(err)
	}
	r.Invalidate() // otherwise the 750ms sample cache returns the previous value
	st, ok = r.Sample(ctx, map[string]bool{real: true})
	if !ok || st.Ins != 0 {
		t.Fatalf("the runner's own artifacts must not count, got %+v ok=%v", st, ok)
	}
}

func TestSampleNeedsABaseline(t *testing.T) {
	if !Available() {
		t.Skip("git is required for gitx tests")
	}
	r := Open(t.TempDir()) // not a repository
	if r.HasBaseline() {
		t.Fatal("an unversioned directory has no baseline")
	}
	if _, ok := r.Sample(context.Background(), nil); ok {
		t.Fatal("sampling without a baseline must report unavailable")
	}
}

func TestDirtyPathsHonorsOwnArtifacts(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(r.Dir, "main.go"),
		[]byte("package main\n\nfunc main() { /* changed */ }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(r.Dir, ".gauntlet-lockfile-check")
	if err := os.WriteFile(artifact, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(artifact)
	if err != nil {
		t.Fatal(err)
	}

	dirty, err := r.DirtyPaths(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 2 {
		t.Fatalf("want both changes, got %v", dirty)
	}

	dirty, err = r.DirtyPaths(ctx, map[string]bool{real: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 1 || dirty[0] != "main.go" {
		t.Fatalf("own artifact should be excluded by real path, got %v", dirty)
	}
	if clean, err := r.IsClean(ctx, map[string]bool{real: true}); clean || err != nil {
		t.Fatalf("a real edit is not clean: clean=%v err=%v", clean, err)
	}
}

func TestExcludeWorktreeRootIsIdempotent(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	r.ExcludeWorktreeRoot(ctx)
	r.ExcludeWorktreeRoot(ctx)

	body, err := os.ReadFile(filepath.Join(r.Dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(body), "/"+WorktreeRoot+"/"); n != 1 {
		t.Fatalf("exclude entry written %d times:\n%s", n, body)
	}
}

// CheckIgnore must report exactly the paths git's exclude rules match. Exit 1
// (none ignored) is a normal answer, not an error, and outside a repository
// nothing counts as ignored.
func TestCheckIgnoreReportsOnlyIgnoredPaths(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(r.Dir, ".gitignore"),
		[]byte("ignored.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "kept.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := r.CheckIgnore(ctx, []string{"kept.md", "ignored.md", ".gitignore"})
	if !got["ignored.md"] {
		t.Fatalf("an excluded path was not reported: %v", got)
	}
	for _, keep := range []string{"kept.md", ".gitignore"} {
		if got[keep] {
			t.Fatalf("%s is not ignored but was reported: %v", keep, got)
		}
	}

	// Outside a repository the answer is "nothing is ignored", never a crash.
	outside := Open(t.TempDir())
	if out := outside.CheckIgnore(ctx, []string{"ignored.md"}); len(out) != 0 {
		t.Fatalf("a non-repo must ignore nothing, got %v", out)
	}
}

// A failing git command must carry git's own stderr diagnostic, not just an
// exit status: the cause is otherwise invisible to whoever reads the log.
func TestRunCarriesGitStderr(t *testing.T) {
	if !Available() {
		t.Skip("git is required for gitx tests")
	}
	r := Open(t.TempDir()) // deliberately not a repository
	_, err := r.run(context.Background(), 10*time.Second, "rev-parse", "HEAD")
	if err == nil {
		t.Fatal("rev-parse HEAD outside a repository must fail")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error should quote git's stderr, got: %v", err)
	}
}

// Reviews add and remove their checkouts at the same time, and git validates
// every registered worktree while doing either: without serialization one
// removal reads another's half-deleted metadata and fails, stranding a branch
// in the reviewed repo.
func TestConcurrentWorktreesDoNotCollide(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	const n = 6
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			wt, err := r.AddWorktree(ctx, fmt.Sprintf("review-%d", i), "run", base)
			if err != nil {
				errs <- err
				return
			}
			errs <- wt.Remove(ctx)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent worktree lifecycle: %v", err)
		}
	}

	out, err := exec.Command("git", "-C", r.Dir, "branch", "--list", "gauntlet/*").Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		return // every branch was cleaned up by its own removal
	}
	// Remove leaves branches alone by design; what matters is that each one
	// exists exactly once and no checkout survives.
	if list, err := exec.Command("git", "-C", r.Dir, "worktree", "list").Output(); err != nil {
		t.Fatal(err)
	} else if strings.Count(string(list), "\n") != 1 {
		t.Errorf("checkouts survived:\n%s", list)
	}
}
