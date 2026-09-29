// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/prompt"
)

// discover is a thin test helper around prompt.Discover.
func discover(t *testing.T, dir string) prompt.Set {
	t.Helper()
	set, _, err := prompt.Discover(context.Background(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// suggestHome points the journal at an empty tree, so a test judges the files
// in front of it and never the machine's own run history.
func suggestHome(t *testing.T) {
	t.Helper()
	t.Setenv("GAUNTLET_HOME", t.TempDir())
}

// frozenClock is the instant the suggester's churn window is measured back
// from. Fixed rather than wall time so a test judges the tree in front of it
// and not the date it happens to run on: a commit made "now" is inside any
// 90-day window, and one made long ago is outside any.
var frozenClock = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// reviews is Reviews for the cases that are not about its error: the
// history read is expected to succeed, so an error fails the test rather than
// passing unnoticed.
func reviews(t *testing.T, dir string, pool []string, set prompt.Set) []prompt.Suggestion {
	t.Helper()
	picked, err := Reviews(dir, pool, set, func() time.Time { return frozenClock })
	if err != nil {
		t.Fatalf("Reviews(%s): %v", dir, err)
	}
	return picked
}

// tree writes a set of files, creating the directories they need. A file may
// carry content as "path\x00body"; without one it gets a byte.
func tree(t *testing.T, files ...string) string {
	t.Helper()
	suggestHome(t)
	dir := t.TempDir()
	for _, f := range files {
		f, body, ok := strings.Cut(f, "\x00")
		if !ok {
			body = "x\n"
		}
		path := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The heuristic suggester proposes what the files justify, and nothing else:
// proposing everything would be the same as proposing nothing.
func TestFastSuggestFollowsTheFiles(t *testing.T) {
	pool := []string{
		"code-review", "sec-review", "container-review", "db-review",
		"ux-review", "test-review", "i18n-review", "mobile-review",
	}
	cases := []struct {
		name    string
		files   []string
		want    []string
		notWant []string
	}{
		{
			name:    "a Go service with tests and a Dockerfile",
			files:   []string{"main.go", "main_test.go", "Dockerfile"},
			want:    []string{"code-review", "sec-review", "test-review", "container-review"},
			notWant: []string{"ux-review", "mobile-review", "db-review"},
		},
		{
			name:    "a web frontend",
			files:   []string{"src/app.tsx", "src/app.css", "index.html"},
			want:    []string{"ux-review", "code-review"},
			notWant: []string{"container-review", "db-review"},
		},
		{
			name:    "migrations and queries",
			files:   []string{"db/migrations/001_init.sql"},
			want:    []string{"db-review"},
			notWant: []string{"ux-review"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]bool{}
			for _, s := range reviews(t, tree(t, c.files...), pool, prompt.Set{}) {
				got[s.Name] = true
				if s.Reason == "" {
					t.Fatalf("%s was proposed with no evidence", s.Name)
				}
			}
			for _, want := range c.want {
				if !got[want] {
					t.Errorf("%s was not proposed for %v", want, c.files)
				}
			}
			for _, no := range c.notWant {
				if got[no] {
					t.Errorf("%s was proposed for %v with nothing to justify it", no, c.files)
				}
			}
		})
	}
}

// Only reviews in the pool are proposed: --exclude and a project's own prompt
// set decide what exists, and the suggester does not get to widen that.
func TestFastSuggestStaysInThePool(t *testing.T) {
	dir := tree(t, "main.go", "Dockerfile")
	var names []string
	for _, s := range reviews(t, dir, []string{"code-review"}, prompt.Set{}) {
		if s.Name != "code-review" {
			t.Fatalf("%s is outside the pool", s.Name)
		}
		names = append(names, s.Name)
	}
	// The loop above also passes when Reviews returns nothing at all, so
	// the pool check needs a positive control: this tree is a Go program, and
	// code-review is in the pool.
	if len(names) != 1 {
		t.Fatalf("a Go tree proposed %v, want exactly code-review", names)
	}
}

// An empty directory justifies nothing, and says so by proposing nothing
// rather than falling back to everything.
func TestFastSuggestProposesNothingForAnEmptyTree(t *testing.T) {
	suggestHome(t)
	if got := reviews(t, t.TempDir(), []string{"code-review", "sec-review"}, prompt.Set{}); len(got) != 0 {
		t.Fatalf("an empty tree produced %v", got)
	}
}

// The walk stays out of dependency and build directories: what npm downloaded
// says nothing about the project under review.
func TestFastSuggestIgnoresVendoredTrees(t *testing.T) {
	dir := tree(t, "node_modules/react/index.tsx", "vendor/lib/thing.c", "README.md")
	var names []string
	for _, s := range reviews(t, dir, []string{"ux-review", "resource-review", "doc-review"}, prompt.Set{}) {
		names = append(names, s.Name)
	}
	if strings.Contains(strings.Join(names, ","), "ux-review") ||
		strings.Contains(strings.Join(names, ","), "resource-review") {
		t.Fatalf("vendored files drove the suggestion: %v", names)
	}
	// Only the README is outside the vendored trees, so it is the one signal
	// that has to survive: without it an empty proposal would pass.
	if !slices.Contains(names, "doc-review") {
		t.Fatalf("a tree with a README proposed %v, want doc-review", names)
	}
}

// Presence is not proportion: one stylesheet in a Go repository is not a
// frontend, and used to light up five frontend reviews.
func TestFastSuggestWeighsHowMuchOfATreeAThingIs(t *testing.T) {
	var files []string
	for i := range 30 {
		files = append(files, filepath.Join("internal", "pkg", "f"+string(rune('a'+i))+".go"))
	}
	files = append(files, "docs/theme.css")
	pool := []string{"code-review", "ux-review", "a11y-review", "webperf-review"}

	var names []string
	for _, s := range reviews(t, tree(t, files...), pool, prompt.Set{}) {
		names = append(names, s.Name)
		if strings.HasPrefix(s.Name, "ux") || strings.HasPrefix(s.Name, "a11y") ||
			strings.HasPrefix(s.Name, "webperf") {
			t.Fatalf("one .css file proposed %s (%s)", s.Name, s.Reason)
		}
	}
	// The absence checks pass on an empty proposal, so pin the one the Go
	// files alone must justify.
	if !slices.Contains(names, "code-review") {
		t.Fatalf("30 .go files proposed %v, want code-review", names)
	}
}

// What is missing is evidence too: a tree with no tests is the strongest case
// for the review that would add them, and presence-only rules said the reverse.
func TestFastSuggestReadsWhatIsMissing(t *testing.T) {
	pool := []string{"test-review", "doc-review", "build-review", "code-review"}
	dir := tree(t, "main.go", "internal/app/app.go")
	got := map[string]string{}
	for _, s := range reviews(t, dir, pool, prompt.Set{}) {
		got[s.Name] = s.Reason
	}
	for _, want := range []string{"test-review", "doc-review", "build-review"} {
		if got[want] == "" {
			t.Errorf("%s was not proposed for a tree that has none of it", want)
		}
	}
	if !strings.Contains(got["test-review"], "no tests") {
		t.Errorf("test-review's evidence was %q, which does not name the absence", got["test-review"])
	}
}

// A review draws more reasons than reasonsShown, and the printed line is the
// only explanation it gets, so it has to lead with the evidence worth most
// rather than whichever rule the table reached first.
func TestFastSuggestLeadsWithTheStrongestEvidence(t *testing.T) {
	pool := []string{"db-review", "test-review", "doc-review", "build-review", "code-review"}
	dir := tree(t,
		"go.mod\x00module x\n",
		"main.go\x00import \"database/sql\"\n",
		"main_test.go\x00package main\n",
	)
	var reason string
	for _, s := range reviews(t, dir, pool, prompt.Set{}) {
		if s.Name == "db-review" {
			reason = s.Reason
		}
	}
	if reason == "" {
		t.Fatal("db-review was not proposed for a tree that opens a database")
	}
	if !strings.HasPrefix(reason, "database access in the source") {
		t.Errorf("db-review's evidence was %q, which does not lead with the strong rule", reason)
	}
}

// Directory names are a guess about a codebase; what it imports is a fact.
func TestFastSuggestReadsInsideFiles(t *testing.T) {
	pool := []string{
		"code-review", "concurrency-review", "db-review", "time-review",
		"o11y-review", "cache-review", "llm-review", "idempotency-review",
	}
	dir := tree(t,
		"svc/worker.py\x00import asyncio\nfrom sqlalchemy import text\nimport redis\n",
		"svc/clock.py\x00from datetime import datetime\nx = datetime.now()\n",
		"svc/obs.py\x00from prometheus_client import Counter\n",
		"svc/agent.py\x00import anthropic\n",
		"svc/queue.py\x00def retry(): ...\n# idempotency key\n",
	)
	got := map[string]bool{}
	for _, s := range reviews(t, dir, pool, prompt.Set{}) {
		got[s.Name] = true
	}
	for _, want := range []string{
		"concurrency-review", "db-review", "time-review",
		"o11y-review", "cache-review", "llm-review", "idempotency-review",
	} {
		if !got[want] {
			t.Errorf("%s was not proposed for source that plainly calls it", want)
		}
	}
}

// A project's own review is unreachable through the built-in rules, which know
// only built-in names. Declaring signals is how it becomes suggestable.
func TestFastSuggestHonorsSignalsAPromptDeclares(t *testing.T) {
	dir := tree(t, "src/main.zig", "build.zig")
	promptDir := t.TempDir()
	body := "You are a Zig reviewer.\n\nSignals: ext:.zig, name:build.zig\n\nYour goal is to review Zig.\n"
	if err := os.WriteFile(filepath.Join(promptDir, "zig-idiomatic-review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set := discover(t, promptDir)

	var reason string
	for _, s := range reviews(t, dir, []string{"zig-idiomatic-review"}, set) {
		if s.Name == "zig-idiomatic-review" {
			reason = s.Reason
		}
	}
	if reason == "" {
		t.Fatal("a review that declared its own signals was not proposed")
	}
	if !strings.Contains(reason, "ext:.zig") {
		t.Errorf("evidence was %q, which does not name the signal that matched", reason)
	}
}

// `mark:` is documented as "a substring found near the top of a source file",
// and the example both docs/RUNS.md and prompt.Signals give is `mark:comptime`.
// It could not work: peek only ever recorded the built-in table's category
// labels, so a declared value matched only by colliding with one of those, and
// `comptime` is not one. A review declaring it was silently unreachable
// through --suggest-agent gauntlet, which is the one way a project's own
// prompt gets proposed at all.
func TestFastSuggestFindsASubstringAReviewDeclares(t *testing.T) {
	dir := tree(t, "src/main.zig\x00const std = @import(\"std\");\n\ncomptime {}\n")
	promptDir := t.TempDir()
	body := "Signals: mark:comptime\n\nYour goal is to review comptime code.\n"
	if err := os.WriteFile(filepath.Join(promptDir, "comptime-review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set := discover(t, promptDir)

	var reason string
	for _, s := range reviews(t, dir, []string{"comptime-review"}, set) {
		if s.Name == "comptime-review" {
			reason = s.Reason
		}
	}
	if reason == "" {
		t.Fatal("a review declaring mark:comptime was not proposed for a tree containing it")
	}
	if !strings.Contains(reason, "mark:comptime") {
		t.Errorf("evidence was %q, which does not name the signal that matched", reason)
	}
}

// The control: a declared substring the tree does not carry proposes nothing.
// Without this, a suggester that matched every declared mark would pass the
// test above and be no better than the bug.
func TestFastSuggestIgnoresASubstringTheTreeLacks(t *testing.T) {
	dir := tree(t, "src/main.zig\x00const std = @import(\"std\");\n")
	promptDir := t.TempDir()
	body := "Signals: mark:comptime\n\nYour goal is to review comptime code.\n"
	if err := os.WriteFile(filepath.Join(promptDir, "comptime-review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set := discover(t, promptDir)
	for _, s := range reviews(t, dir, []string{"comptime-review"}, set) {
		if s.Name == "comptime-review" && strings.Contains(s.Reason, "mark:comptime") {
			t.Fatalf("claimed a mark the tree does not carry: %q", s.Reason)
		}
	}
}

// The built-in table keeps working beside the declared ones, and a declared
// value that repeats one of its labels does not double-count.
func TestMarkSearchAddsDeclaredWithoutDisturbingTheTable(t *testing.T) {
	base := len(marks)
	if got := markSearch(nil); len(got) != base {
		t.Fatalf("no declared marks changed the table: %d entries, want %d", len(got), base)
	}
	got := markSearch([]string{"comptime", "comptime", "", "borrow"})
	if len(got) != base+2 {
		t.Fatalf("added %d entries for 2 distinct values", len(got)-base)
	}
	if len(marks) != base {
		t.Fatal("markSearch wrote into the package-level table")
	}
	many := make([]string, declaredMarkMax*2)
	for i := range many {
		many[i] = fmt.Sprintf("m%03d", i)
	}
	if got := markSearch(many); len(got) != base+declaredMarkMax {
		t.Fatalf("%d declared values became %d entries, want the %d cap",
			len(many), len(got)-base, declaredMarkMax)
	}
}

// A declared mark is stored NFC+lower. File contents are not: macOS editors
// write NFD, and asciiFold leaves non-ASCII capitals alone. Both spellings
// of the same word in a source head must still match the signal.
func TestFastSuggestMatchesMarkAcrossNormalizationForms(t *testing.T) {
	dir := tree(t, "src/main.go\x00package main\n// cafe\u0301 notes\n")
	promptDir := t.TempDir()
	body := "Signals: mark:caf\u00e9\n\nYour goal is to review café notes.\n"
	if err := os.WriteFile(filepath.Join(promptDir, "cafe-review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set := discover(t, promptDir)

	var reason string
	for _, s := range reviews(t, dir, []string{"cafe-review"}, set) {
		if s.Name == "cafe-review" {
			reason = s.Reason
		}
	}
	if reason == "" {
		t.Fatal("an NFD spelling of café in a source file never matched mark:café")
	}
	if !strings.Contains(reason, "mark:café") {
		t.Errorf("evidence was %q, which does not name the mark that matched", reason)
	}
}

func TestFastSuggestMatchesMarkIgnoringNonASCIICase(t *testing.T) {
	dir := tree(t, "src/main.go\x00package main\n// CAFÉ notes\n")
	promptDir := t.TempDir()
	body := "Signals: mark:caf\u00e9\n\nYour goal is to review café notes.\n"
	if err := os.WriteFile(filepath.Join(promptDir, "cafe-review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set := discover(t, promptDir)
	var reason string
	for _, s := range reviews(t, dir, []string{"cafe-review"}, set) {
		if s.Name == "cafe-review" {
			reason = s.Reason
		}
	}
	if reason == "" {
		t.Fatal("CAFÉ in a source file never matched mark:café")
	}
}

// A macOS tree hands out NFD filenames while an author types NFC into the
// Signals: line of a prompt. Both sides are stored NFC (record normalizes
// what it receives, prompt.Signals normalizes what the author declared), so
// the same word spelled in two forms still matches.
func TestFastSuggestMatchesSignalsAcrossNormalizationForms(t *testing.T) {
	suggestHome(t)
	dir := t.TempDir()
	nfd := "cafe\u0301-notes.md" // decomposed é, as a Mac filesystem spells it
	if norm.NFC.String(nfd) == nfd {
		t.Fatal("fixture is not decomposed; it proves nothing")
	}
	if err := os.WriteFile(filepath.Join(dir, nfd), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	promptDir := t.TempDir()
	body := "Signals: name:caf\u00e9-notes.md\n\nYour goal is to review caf\u00e9 notes.\n"
	if err := os.WriteFile(filepath.Join(promptDir, "cafe-review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set := discover(t, promptDir)

	var reason string
	for _, s := range reviews(t, dir, []string{"cafe-review"}, set) {
		if s.Name == "cafe-review" {
			reason = s.Reason
		}
	}
	if reason == "" {
		t.Fatal("an NFD filename never matched its NFC-declared signal")
	}
	if !strings.Contains(reason, "name:") {
		t.Errorf("evidence was %q, which does not name the signal that matched", reason)
	}
}

// A review that has finished here several times without changing a line is a
// bad pick for this directory, whatever the files say.
func TestFastSuggestLearnsFromPastRunsInThisDirectory(t *testing.T) {
	dir := tree(t, "main.go", "main_test.go")
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	writeHistory(t, home, dir, "sec-review", 4, 0)
	writeHistory(t, home, dir, "test-review", 4, 4)

	var order []string
	for _, s := range reviews(t, dir, []string{"sec-review", "test-review", "code-review"}, prompt.Set{}) {
		order = append(order, s.Name)
	}
	if len(order) == 0 || order[0] != "test-review" {
		t.Errorf("the review that keeps finding work here did not rank first: %v", order)
	}
	if len(order) == 0 || order[len(order)-1] != "sec-review" {
		t.Errorf("the review that never changes anything here did not rank last: %v", order)
	}
}

// writeHistory fakes runs in a GAUNTLET_HOME: n finished reviews in dir, of
// which changed left lines behind.
func writeHistory(t *testing.T, home, dir, review string, n, changed int) {
	t.Helper()
	runs := filepath.Join(home, "runs", "2026-01-01")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	index, err := os.OpenFile(filepath.Join(home, "index.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	for i := range n {
		runID := "20260101T00000" + string(rune('0'+i)) + "Z-" + review[:3]
		path := filepath.Join(runs, runID+".jsonl")
		ev := map[string]any{"ev": "review_end", "dir": dir, "review": review, "status": "ok"}
		if i < changed {
			ev["ins"], ev["del"] = 10, 2
		}
		line, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		row, err := json.Marshal(map[string]any{"run_id": runID, "path": path, "dirs": []string{dir}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := index.Write(append(row, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

// peek reads heads from the reviewed tree. A symlink, a FIFO, or a path that
// walks out of it must not contribute marks, and must not block the scan.
func TestPeekStaysInsideTheTree(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.go")
	if err := os.WriteFile(outside, []byte("package x\nimport \"net/http\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "http.go"), []byte("package main\nimport \"net/http\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	in := signals{mark: map[string]int{}}
	peek(dir, []string{"http.go"}, &in, nil)
	if in.mark["http"] == 0 {
		t.Fatal("peek missed an in-tree net/http import")
	}

	if err := os.Symlink(outside, filepath.Join(dir, "evil.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.go"), 0o644); err != nil {
		t.Skipf("fifo unavailable: %v", err)
	}

	s := signals{mark: map[string]int{}}
	escape := filepath.Join("..", filepath.Base(outsideDir), "secret.go")
	done := make(chan struct{})
	go func() {
		peek(dir, []string{"main.go", "evil.go", "pipe.go", escape}, &s, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("peek blocked on a fifo or an escaping path")
	}
	if s.mark["http"] > 0 {
		t.Fatal("peek followed a symlink or escaped the tree")
	}
}

// An index that cannot be read is a failure the operator has to hear about:
// every review then weighs as untried here, and the suggester re-proposes the
// ones that keep finishing without changing a line. The file evidence still
// stands, so the picks come back beside the error rather than instead of it.
func TestFastSuggestReportsAJournalItCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens a 0000 file anyway, so the failure cannot be provoked here")
	}
	dir := tree(t, "main.go")
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	// An index nobody can read: the shape a state tree another account owns,
	// or one a crashed run left half-written, takes.
	if err := os.WriteFile(filepath.Join(home, "index.jsonl"), nil, 0o000); err != nil {
		t.Fatal(err)
	}

	picked, err := Reviews(dir, []string{"code-review"}, prompt.Set{}, func() time.Time { return frozenClock })
	if err == nil {
		t.Fatal("an unreadable journal was swallowed")
	}
	if len(picked) == 0 {
		t.Fatal("the tree evidence was thrown away with the journal")
	}
}

// TestScanChurnWindowReadsTheInjectedClock pins the property the cutoff exists
// for: the same tree scanned under two different clocks sees the history the
// caller asked for, not whatever git thinks is recent. The window reaches back
// from the clock, so a clock a day past the commit still counts it and a clock
// a year past it does not, and only a cutoff the caller computed can answer
// both.
func TestScanChurnWindowReadsTheInjectedClock(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is required to read churn")
	}
	dir := tree(t, "main.go\x00package main\n")
	commit := commitAll(t, dir, "2026-03-01T12:00:00Z")

	after := scan(dir, nil, func() time.Time { return commit.Add(24 * time.Hour) })
	if !after.churn {
		t.Fatal("a commit a day old is not churn")
	}
	// The window reaches back from the clock, so a clock a year past the
	// commit puts its cutoff beyond it and the history reads as dormant.
	before := scan(dir, nil, func() time.Time { return commit.Add(365 * 24 * time.Hour) })
	if before.churn {
		t.Fatal("a commit a year before the clock's window is still churn")
	}
}

// commitAll makes the tree's single commit carry a stated date, so the test
// decides where the churn window's edge falls rather than when it happens to
// run.
func commitAll(t *testing.T, dir, date string) time.Time {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "test")
	git("add", "-A")
	cmd := exec.Command("git", "commit", "-qm", "init")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	at, err := time.Parse(time.RFC3339, date)
	if err != nil {
		t.Fatal(err)
	}
	return at
}
