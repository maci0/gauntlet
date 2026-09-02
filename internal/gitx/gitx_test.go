// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
		// With core.quotePath off, non-ASCII arrives raw inside the quotes
		// when something else forced quoting.
		{`?? "café.md"`, "café.md"},
		// Octal escapes are how git encodes non-ASCII bytes when quoting is
		// on; decoding them is what keeps é from arriving as \303\251.
		{`?? "caf\303\251.md"`, "café.md"},
		// A literal backslash in a filename arrives doubled inside quotes.
		{` M "naïve\\dir\\file"`, `naïve\dir\file`},
		{`?? "quo\"te.go"`, `quo"te.go`},
		{`?? "tab\there.md"`, "tab\there.md"},
		// A lone trailing backslash inside quotes stays verbatim.
		{`?? "trail\"`, `trail\`},
		{`?? "weird\qescape"`, `weird\qescape`},
		{"?? café.md", "café.md"},
		{"", ""},
		{" M", ""},
	}
	for _, c := range cases {
		if got := porcelainPath(c.in); got != c.want {
			t.Errorf("porcelainPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// quoteGitPath encodes a path the way git quotes it inside porcelain output
// when the path needs quoting: wrapped in double quotes, backslash and double
// quote doubled, control bytes as three-digit octal, everything else raw.
// It is the encoder half of the round-trip property FuzzPorcelainPath pins.
func quoteGitPath(p string) string {
	var b strings.Builder
	b.Grow(len(p) + 2)
	b.WriteByte('"')
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '\\' || c == '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\a':
			b.WriteString(`\a`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\v':
			b.WriteString(`\v`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, "\\%03o", c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// FuzzPorcelainPath drives the git status line parser with arbitrary hostile
// repository output. The C-unquote is hand-rolled and its result decides
// whether a run sees the tree as dirty (which blocks worktree isolation) and
// which paths are reported, so it is pinned two ways per iteration: every raw
// line must parse deterministically and never yield anything on a line too
// short to carry a path, and decoding must invert exactly what git produces,
// checked by re-encoding an arbitrary byte string into git's quoted form and
// requiring unquoteC to restore it verbatim.
func FuzzPorcelainPath(f *testing.F) {
	seeds := []string{
		" M main.go",
		"M  main.go",
		"?? untracked.go",
		"R  old.txt -> new.txt",
		"C  orig.txt -> copy.txt",
		"?? a -> b",
		`?? "sp ace.go"`,
		`?? "caf\303\251.md"`,
		` M "naïve\\dir\\file"`,
		`?? "quo\"te.go"`,
		`?? "tab\there.md"`,
		`?? "trail\"`,
		`?? "weird\qescape"`,
		`?? "\000\037\177.md"`,
		`R  "a\001b" -> `,
		"",
		" M",
		"ab",
	}
	for _, s := range seeds {
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, line, path string) {
		got := porcelainPath(line)
		if again := porcelainPath(line); again != got {
			t.Fatalf("porcelainPath(%q) is not deterministic: %q vs %q", line, got, again)
		}
		if len(line) <= 3 && got != "" {
			t.Fatalf("short line %q yielded a path %q", line, got)
		}
		if decoded := unquoteC(quoteGitPath(path)); decoded != path {
			t.Fatalf("round trip lost %q: got %q", path, decoded)
		}
	})
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
		if got := BranchSlug(in); got != want {
			t.Errorf("BranchSlug(%q) = %q, want %q", in, got, want)
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
		r := &Repo{Dir: dir}
		path := write(strings.NewReplacer(" ", "_").Replace(name)+".txt", c.body)
		if got := r.countLinesCached(path); got != c.want {
			t.Errorf("%s: countLinesCached = %d, want %d", name, got, c.want)
		}
		// And again from the cache: the same file must count the same.
		if got := r.countLinesCached(path); got != c.want {
			t.Errorf("%s: cached read = %d, want %d", name, got, c.want)
		}
	}

	// A symlink must not be followed: O_NOFOLLOW refuses it at open time.
	target := write("target.txt", "line\n")
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := (&Repo{Dir: dir}).countLinesCached(link); got != 0 {
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

// Sampling repeats every interval for the life of a loop, so an unchanged
// file must answer from the cache (same count) and an edited one must not
// (size and mtime no longer match, so the entry is recomputed).
func TestCountLinesCachedRecountsChangedFiles(t *testing.T) {
	r := Open(t.TempDir())
	p := filepath.Join(r.Dir, "notes.txt")
	if err := os.WriteFile(p, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := r.countLinesCached(p); n != 2 {
		t.Fatalf("first count = %d, want 2", n)
	}
	if n := r.countLinesCached(p); n != 2 {
		t.Fatalf("cached count = %d, want 2", n)
	}
	if err := os.WriteFile(p, []byte("a\nb\nc\nd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := r.countLinesCached(p); n != 4 {
		t.Fatalf("count after edit = %d, want 4 (stale cache entry?)", n)
	}
	if n := r.countLinesCached(filepath.Join(r.Dir, "absent.txt")); n != 0 {
		t.Fatalf("missing file counted %d lines", n)
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
}

func TestDirtyPathsNonASCIIFilename(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	name := "café-review-notes.md"
	if err := os.WriteFile(filepath.Join(r.Dir, name),
		[]byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := r.DirtyPaths(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 1 || dirty[0] != name {
		t.Fatalf("non-ASCII path must survive git status verbatim, got %q", dirty)
	}
}

// gauntlet's own scratch never belongs in a project's git status, and the
// exclusion is written once however often a run asks for it.
func TestExcludeOwnArtifactsIsIdempotent(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	r.ExcludeOwnArtifacts(ctx)
	r.ExcludeOwnArtifacts(ctx)

	body, err := os.ReadFile(filepath.Join(r.Dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"/" + worktreeRoot + "/", "/" + LockName} {
		if n := strings.Count(string(body), entry); n != 1 {
			t.Fatalf("%q written %d times:\n%s", entry, n, body)
		}
	}

	// And git agrees: a lock file in the tree is ignored, not untracked work.
	if err := os.WriteFile(filepath.Join(r.Dir, LockName), []byte("held\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ignored := r.CheckIgnore(ctx, []string{LockName}); !ignored[LockName] {
		t.Fatal("the run lock must be ignored by the repository it runs in")
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

// installHook writes a pre-commit hook and returns its directory. The hook is
// armed through a trailing -c because safeConfig pins core.hooksPath to
// /dev/null first, and the last -c wins. The child pid lands in a file so the
// test can verify (and clean up) what the hook left behind.
func installHook(t *testing.T, r *Repo, body string) string {
	t.Helper()
	hooks := filepath.Join(r.Dir, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return hooks
}

// hookChildPID waits for the hook to record its background child.
func hookChildPID(t *testing.T, r *Repo) int {
	t.Helper()
	pidfile := filepath.Join(r.Dir, "hooks", "child.pid")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(pidfile)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the hook never recorded its child")
	return 0
}

// processAlive probes one pid with a zero signal: nil or EPERM means something
// is still there, ESRCH means it is gone. kill(pid, 0) works on every POSIX
// target this project ships for; a /proc walk would make this probe answer
// "gone" unconditionally on macOS, where the assertion below could never fire.
// The killed group's parents died in the same sweep, so a dead pid is reaped
// by init rather than lingering as an unreapable zombie holding the probe
// green.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// The deadline kill must take down the whole process group, not just the git
// pid: a helper git spawns that outlives it must be reaped too, or every
// timed-out call leaks an orphan still holding the output pipes. This is the
// same rule runProc and runIndexer enforce on their own children.
//
// The kill is triggered by canceling the caller's context rather than by a
// short fixed timeout, for the same reason the indexer's twin test waits on
// its fixture: runIn derives its deadline context from the caller's, so both
// paths fire the identical group-kill Cancel, but a timeout short enough to
// keep the test quick also loses a race the test is not about. On macOS the
// first exec of a freshly written hook script pays a one-time security check,
// and under a loaded suite a one-second deadline landed before the hook had
// backgrounded anything -- no grandchild, no pid file, and a failure that
// reads as the hook never running.
func TestRunDeadlineKillsTheProcessGroup(t *testing.T) {
	r := newRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The hook blocks on its background sleeper, so git is alive when the
	// cancel fires and the group kill has something to reach.
	hooks := installHook(t, r,
		"sleep 60 &\necho $! > \"$GAUNTLET_HOOK_PIDFILE\"\nwait\n")
	t.Setenv("GAUNTLET_HOOK_PIDFILE", filepath.Join(hooks, "child.pid"))

	done := make(chan error, 1)
	go func() {
		_, err := r.run(ctx, time.Minute,
			"-c", "core.hooksPath="+hooks,
			"commit", "--allow-empty", "-m", "hooked")
		done <- err
	}()

	// Only once the grandchild exists is there anything for the kill to miss.
	pid := hookChildPID(t, r)
	canceled := time.Now()
	cancel()

	select {
	case err := <-done:
		// From the cancel, the run must come back promptly: a grandchild
		// holding the output pipes is bounded by WaitDelay, not by its own
		// sleep. This is what the old start-to-finish bound really checked.
		if elapsed := time.Since(canceled); elapsed > 5*time.Second {
			t.Fatalf("the kill took %s to land on the whole group", elapsed)
		}
		if err == nil {
			t.Fatal("a killed commit must report failure")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the canceled commit never returned")
	}
	deadline := time.Now().Add(10 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Errorf("grandchild %d survived the group kill", pid)
	}
	syscall.Kill(pid, syscall.SIGKILL) // belt and braces; nothing outlives the test
}

// A grandchild that outlived a successful git call would hold the inherited
// output pipes open, and Run waits for EOF on them: without WaitDelay one
// lingering child parks this call, and with it the caller's mutexes, long
// past any bound. ErrWaitDelay is the bounded answer.
func TestRunBoundedWhenGrandchildHoldsOutput(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	// This hook exits successfully while its background sleeper lives on,
	// holding stdout and stderr for far longer than the grace period.
	hooks := installHook(t, r,
		"sleep 30 &\necho $! > \"$GAUNTLET_HOOK_PIDFILE\"\nexit 0\n")
	t.Setenv("GAUNTLET_HOOK_PIDFILE", filepath.Join(hooks, "child.pid"))

	oldGrace := waitGrace
	waitGrace = 200 * time.Millisecond
	defer func() { waitGrace = oldGrace }()

	start := time.Now()
	_, err := r.run(ctx, 30*time.Second,
		"-c", "core.hooksPath="+hooks,
		"commit", "--allow-empty", "-m", "hooked")
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("run took %s; a grandchild holding the pipes must not set the bound", elapsed)
	}
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("want exec.ErrWaitDelay, got %v", err)
	}
	pid := hookChildPID(t, r)
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
}

// AddWorktree must converge when a rerun hits its own leftovers: a hot reload
// continues the same run id while loop numbering restarts, so tags recur. A
// branch still at base is provably empty and may be rebuilt; a branch holding
// commits (what a kept conflict looks like) must stop the run, not be destroyed.
func TestAddWorktreeConvergesOnLeftoverBranch(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(ctx, "sec-review", "run-l1-00", base)
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.AddWorktree(ctx, "sec-review", "run-l1-00", base)
	if err != nil {
		t.Fatalf("a second identical add must succeed: %v", err)
	}
	if again.Branch != wt.Branch || again.Dir != wt.Dir {
		t.Fatalf("rerun should rebuild the same worktree: %+v vs %+v", again, wt)
	}
	if err := again.Remove(ctx); err != nil {
		t.Fatal(err)
	}

	tree := gitOut(t, r.Dir, "rev-parse", "HEAD^{tree}")
	commit := gitOut(t, r.Dir, "commit-tree", tree, "-p", base, "-m", "conflicted work")
	kept := "gauntlet/run-l2-00/sec-review"
	gitIn(t, r.Dir, "branch", kept, commit)

	if _, err := r.AddWorktree(ctx, "sec-review", "run-l2-00", base); err == nil {
		t.Fatal("an add over a branch with real work must fail")
	} else if !strings.Contains(err.Error(), "already exists at") {
		t.Fatalf("leftover-work error = %v, want it to name both tips", err)
	}
	if got := gitOut(t, r.Dir, "rev-parse", kept); got != commit {
		t.Fatalf("the kept branch was modified: %s != %s", got, commit)
	}
}

func TestAddStackWorktreeConvergesOnLeftoverBranch(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	branch := "gauntlet/stack/" + base[:12] + "/01-sec-review"
	wt, err := r.AddStackWorktree(ctx, branch, "run", base)
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.AddStackWorktree(ctx, branch, "run", base)
	if err != nil {
		t.Fatalf("a second identical add must succeed: %v", err)
	}
	if again.Branch != wt.Branch || again.Dir != wt.Dir {
		t.Fatalf("rerun should rebuild the same stack worktree: %+v vs %+v", again, wt)
	}
	if err := again.Remove(ctx); err != nil {
		t.Fatal(err)
	}

	tree := gitOut(t, r.Dir, "rev-parse", "HEAD^{tree}")
	commit := gitOut(t, r.Dir, "commit-tree", tree, "-p", base, "-m", "unpublished work")
	gitIn(t, r.Dir, "branch", "-f", branch, commit)

	if _, err := r.AddStackWorktree(ctx, branch, "run", base); err == nil {
		t.Fatal("an add over a branch with real work must fail")
	} else if !strings.Contains(err.Error(), "already exists at") {
		t.Fatalf("leftover-work error = %v, want the same wording AddWorktree uses", err)
	}
	if got := gitOut(t, r.Dir, "rev-parse", branch); got != commit {
		t.Fatalf("the kept branch was modified: %s != %s", got, commit)
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

// The tree scan asks git what belongs to the project, so ListFiles must be
// exactly that: tracked files plus untracked ones git does not ignore.
func TestListFilesFollowsWhatTheRepoIgnores(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	write := func(name, body string) {
		t.Helper()
		full := filepath.Join(r.Dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "build/\n")
	write("build/artifact.o", "x")
	write("untracked.py", "x")

	files, err := r.ListFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f] = true
	}
	if !got["main.go"] || !got["untracked.py"] {
		t.Fatalf("ListFiles missed the project's own files: %v", files)
	}
	if got["build/artifact.o"] {
		t.Fatalf("ListFiles returned an ignored build artifact: %v", files)
	}
}

// git's -z output is raw bytes on purpose, and a file name may legitimately
// begin or end with a space. Trimming the records would hand every caller a
// name that no longer matches the file: a stacked PR body naming a path the
// commit did not touch, and a tree listing whose signals key on the wrong
// name.
func TestNULOutputKeepsSpacesInNames(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	const spaced = " leading and trailing "
	if err := os.WriteFile(filepath.Join(r.Dir, spaced), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := r.ListFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(files, spaced) {
		t.Fatalf("ListFiles renamed the file: %q", files)
	}

	gitIn(t, r.Dir, "add", "-A")
	gitIn(t, r.Dir, "commit", "-qm", "add a file with spaces around its name")

	changed, err := r.ChangedFiles(ctx, r.Dir, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(changed, spaced) {
		t.Fatalf("ChangedFiles renamed the file: %q", changed)
	}
	if since, err := r.ChangedSince(ctx, "90 days ago"); err != nil {
		t.Fatal(err)
	} else if !slices.Contains(since, spaced) {
		t.Fatalf("ChangedSince renamed the file: %q", since)
	}
}

func TestChangedSinceNamesRecentWork(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	changed, err := r.ChangedSince(ctx, "90 days ago")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(changed, "main.go") {
		t.Fatalf("the only commit's file is missing from %v", changed)
	}
	// A window wide enough to cover the whole history still reports it, so a
	// caller reading "no churn" knows it means no commits, not a parse slip.
	old, err := r.ChangedSince(ctx, "2000-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(old) == 0 {
		t.Fatal("a window covering the whole history reported no files")
	}
}
