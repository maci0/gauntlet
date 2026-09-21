// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func TestPRBodyDescribesTheChange(t *testing.T) {
	// The title alone repeats the PR's own heading. What the body has to add
	// is the area, the files, and the size, so a reader can decide whether to
	// open the diff without opening it.
	body := prBody{
		Title: "fix(cache): drop the stale entry before the refill",
		Scope: "stale reads, cross-tenant bleed, stampedes",
		Files: []string{"internal/cache/store.go", "internal/cache/store_test.go"},
		Ins:   41, Del: 12, HaveLines: true,
		Base: "gauntlet/stack/ab12cd34ef56/02-sec-review", Root: "main", Layer: 3,
	}.render()

	for _, want := range []string{
		"## Summary",
		"fix(cache): drop the stale entry before the refill",
		"Scope: stale reads, cross-tenant bleed, stampedes.",
		"## Changes",
		"- `internal/cache/store.go`",
		"- `internal/cache/store_test.go`",
		"2 files changed, 41 insertions, 12 deletions.",
		"## Stack",
		"Layer 3 of a stack",
		"`gauntlet/stack/ab12cd34ef56/02-sec-review`",
		"`main`",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body is missing %q:\n%s", want, body)
		}
	}
}

func TestPRBodySingularCounts(t *testing.T) {
	// "1 files changed" is the seam that makes a reader wonder what else in
	// the body was assembled without looking.
	body := prBody{Title: "perf(index): stop rescanning the whole tree",
		Files: []string{"a.go"}, Ins: 3, Del: 1, HaveLines: true,
		Base: "main", Root: "main", Layer: 1}.render()
	if !strings.Contains(body, "1 file changed, 3 insertions, 1 deletion.") {
		t.Fatalf("counts are not singular:\n%s", body)
	}
	// The first layer has no predecessor to warn about, so it says what it is
	// instead of naming a base that is also the root.
	if !strings.Contains(body, "First layer of a stack, cut from `main`.") {
		t.Fatalf("first layer note missing:\n%s", body)
	}
	if strings.Contains(body, "Layer 1 of a stack") {
		t.Fatalf("first layer must not read as a stacked child:\n%s", body)
	}
}

func TestPRBodyCountsFilesPastTheList(t *testing.T) {
	// A body is an orientation, not an inventory: a review that touched a
	// hundred files must not paste a hundred lines above the diff.
	b := prBody{Title: "chore(ui): tidy the widget tree", Base: "b", Root: "main", Layer: 2}
	for range prBodyFileMax + 4 {
		b.Files = append(b.Files, "internal/ui/widget.go")
	}
	body := b.render()
	if n := strings.Count(body, "- `internal/ui/widget.go`"); n != prBodyFileMax {
		t.Fatalf("listed %d paths, want %d:\n%s", n, prBodyFileMax, body)
	}
	if !strings.Contains(body, "- and 4 more files") {
		t.Fatalf("the remainder is not counted:\n%s", body)
	}
}

func TestPRBodyLeavesOutWhatItCouldNotRead(t *testing.T) {
	// Missing is missing. A diff stat git would not answer must not print as
	// "0 files changed", and a review that declares no subject must not get an
	// empty "Scope:" line.
	body := prBody{Title: "refactor(agent): fold the two launch paths",
		Base: "main", Root: "main", Layer: 1}.render()
	for _, unwanted := range []string{"## Changes", "Scope:", "files changed"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("body invented %q with nothing to report:\n%s", unwanted, body)
		}
	}
	// A layer number of zero is the caller saying it does not know where the
	// branch sits, which is not the same as it sitting at the root.
	if strings.Contains(prBody{Title: "x", Base: "main", Root: "main"}.render(), "## Stack") {
		t.Fatal("an unknown layer must not be described")
	}
}

func TestPRBodyNeutralizesUntrustedText(t *testing.T) {
	// Paths, commit subjects, and prompt summaries all originate in the
	// reviewed repository. A backtick in a path would close its code span and
	// let the rest be read as markup; a newline in a subject would forge a
	// heading under text that looks like one sentence.
	body := prBody{
		Title: "fix: a\n## Injected\n- item",
		Scope: "one\ntwo",
		Files: []string{"a`.go", "b\n## Also.go"},
		Base:  "main", Root: "main", Layer: 1,
	}.render()
	// A "## " only means a heading at the start of a line, so the property is
	// that untrusted text never reaches one, not that it never contains the
	// characters.
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "#") && line != "## Summary" &&
			line != "## Changes" && line != "## Stack" {
			t.Fatalf("untrusted text forged the heading %q:\n%s", line, body)
		}
		if strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "- `") {
			t.Fatalf("untrusted text forged the list item %q:\n%s", line, body)
		}
	}
	if strings.Contains(body, "`a`.go`") {
		t.Fatalf("a backtick in a path closed its code span:\n%s", body)
	}
	if !strings.Contains(body, "- `a'.go`") {
		t.Fatalf("the replaced backtick left no trace:\n%s", body)
	}
	if !strings.Contains(body, "fix: a ## Injected - item") {
		t.Fatalf("flattening welded the words together:\n%s", body)
	}
}

func TestPRBodyBoundsEveryUntrustedValue(t *testing.T) {
	// A prompt file or a path is untrusted input, so its length is an input
	// too: without a cap one line pushes everything worth reading off screen.
	long := strings.Repeat("x", 4000)
	body := prBody{Title: long, Scope: long, Files: []string{long},
		Base: long, Root: long, Layer: 2}.render()
	if len(body) > 4000 {
		t.Fatalf("unbounded body of %d bytes", len(body))
	}
	if !strings.Contains(body, "…") {
		t.Fatalf("nothing was truncated:\n%s", body)
	}
}

func TestPRBodyRendersOverview(t *testing.T) {
	// The diff shows which files moved; the overview is the only place that
	// says what the change was about. It rides in the Summary section, and
	// the file list stays bare paths.
	body := prBody{
		Title:    "fix(cache): drop the stale entry before the refill",
		Overview: "guard the refill against a stale read; drop the dead branch.",
		Files:    []string{"internal/cache/store.go", "internal/cache/store_test.go"},
		Base:     "main", Root: "main", Layer: 1,
	}.render()
	if !strings.Contains(body, "\n\nguard the refill against a stale read; drop the dead branch.\n") {
		t.Fatalf("overview missing from the summary:\n%s", body)
	}
	if !strings.Contains(body, "- `internal/cache/store.go`\n") ||
		!strings.Contains(body, "- `internal/cache/store_test.go`\n") {
		t.Fatalf("files must render as bare paths:\n%s", body)
	}
}

func TestPRBodyNeutralizesHostileOverview(t *testing.T) {
	// The overview is agent output quoting repository content: injection with
	// the repository's words. Newlines must not open a heading or a list item,
	// and a backtick must not open a code span that swallows what follows.
	long := strings.Repeat("z", 4000)
	body := prBody{
		Title:    "chore: tidy",
		Overview: "done\n## Injected\n- item\n```go\ncode un`balanced " + long,
		Files:    []string{"a.go", "b.go", strings.Repeat("p", 500)},
		Base:     "main", Root: "main", Layer: 1,
	}.render()
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "#") && line != "## Summary" &&
			line != "## Changes" && line != "## Stack" {
			t.Fatalf("a note forged the heading %q:\n%s", line, body)
		}
		if strings.HasPrefix(line, "```") {
			t.Fatalf("a note opened a code fence:\n%s", body)
		}
		if strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "- `") {
			t.Fatalf("a note forged the list item %q:\n%s", line, body)
		}
		if strings.Count(line, "`")%2 != 0 {
			t.Fatalf("unbalanced backticks can swallow the next line %q:\n%s", line, body)
		}
	}
	if len(body) > prBodyMax+3 {
		t.Fatalf("unbounded body of %d bytes", len(body))
	}
	if !strings.Contains(body, "…") {
		t.Fatalf("nothing was truncated:\n%s", body)
	}
}

// FuzzPRBodyRender drives prBody.render and its formatting helpers (mdText,
// mdCode, changes, stackNote) with arbitrary untrusted inputs: titles,
// scopes, overviews, branch names, and raw file lists. It pins the structural
// and security invariants of the generated PR body: rendering never panics,
// output size is strictly bounded, internal newlines in fields cannot break out
// into multi-line injections, file lists are capped at prBodyFileMax, and code
// spans always have balanced delimiters.
func FuzzPRBodyRender(f *testing.F) {
	seeds := []struct {
		title     string
		scope     string
		overview  string
		base      string
		root      string
		filesRaw  string
		layer     int
		ins       int
		del       int
		haveLines bool
	}{
		{
			title:     "fix(cache): drop the stale entry before the refill",
			scope:     "stale reads, cross-tenant bleed, stampedes",
			overview:  "guard the refill against a stale read; drop the dead branch.",
			base:      "gauntlet/stack/ab12cd34ef56/02-sec-review",
			root:      "main",
			filesRaw:  "internal/cache/store.go\x00internal/cache/store_test.go",
			layer:     3,
			ins:       41,
			del:       12,
			haveLines: true,
		},
		{
			title:     "perf(index): stop rescanning the whole tree",
			scope:     "",
			overview:  "",
			base:      "main",
			root:      "main",
			filesRaw:  "a.go",
			layer:     1,
			ins:       3,
			del:       1,
			haveLines: true,
		},
		{
			title:     "fix: a\n## Injected\n- item",
			scope:     "one\ntwo",
			overview:  "done\n## Injected\n- item\n```go\ncode un`balanced",
			base:      "b`base",
			root:      "r`root",
			filesRaw:  "a`.go\x00b\n## Also.go\x00c```.go",
			layer:     2,
			ins:       0,
			del:       0,
			haveLines: false,
		},
		{
			title:     strings.Repeat("x", 4000),
			scope:     strings.Repeat("y", 4000),
			overview:  strings.Repeat("z", 4000),
			base:      strings.Repeat("b", 4000),
			root:      strings.Repeat("r", 4000),
			filesRaw:  strings.Repeat("p.go\x00", 25),
			layer:     -1,
			ins:       -100,
			del:       -200,
			haveLines: true,
		},
		{
			title:     "",
			scope:     "",
			overview:  "",
			base:      "",
			root:      "",
			filesRaw:  "",
			layer:     0,
			ins:       0,
			del:       0,
			haveLines: false,
		},
	}
	for _, s := range seeds {
		f.Add(s.title, s.scope, s.overview, s.base, s.root, s.filesRaw, s.layer, s.ins, s.del, s.haveLines)
	}

	f.Fuzz(func(t *testing.T, title, scope, overview, base, root, filesRaw string, layer, ins, del int, haveLines bool) {
		var files []string
		if filesRaw != "" {
			files = strings.Split(filesRaw, "\x00")
		}
		b := prBody{
			Title:     title,
			Scope:     scope,
			Files:     files,
			Overview:  overview,
			Ins:       ins,
			Del:       del,
			HaveLines: haveLines,
			Base:      base,
			Root:      root,
			Layer:     layer,
		}

		body := b.render()

		if again := b.render(); again != body {
			t.Fatalf("prBody.render is non-deterministic")
		}

		if runes := utf8.RuneCountInString(body); runes > prBodyMax+1 {
			t.Fatalf("body length %d runes exceeds cap %d", runes, prBodyMax+1)
		}

		// Untrusted helper invariants
		txt := mdText(title, prBodyTitleMax)
		if strings.ContainsAny(txt, "\r\n") {
			t.Fatalf("mdText contains newlines: %q", txt)
		}

		code := mdCode(base, prBodyPathMax)
		if !strings.HasPrefix(code, "`") || !strings.HasSuffix(code, "`") {
			t.Fatalf("mdCode not wrapped in backticks: %q", code)
		}
		if strings.Count(code, "`") != 2 {
			t.Fatalf("mdCode has interior backtick: %q", code)
		}
		if strings.ContainsAny(code, "\r\n") {
			t.Fatalf("mdCode contains newlines: %q", code)
		}

		// Changes section invariants
		changes := b.changes()
		fileLines := 0
		for line := range strings.SplitSeq(changes, "\n") {
			if strings.HasPrefix(line, "- `") {
				fileLines++
			}
			if strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "- `") && !strings.HasPrefix(line, "- and ") {
				t.Fatalf("Changes section forged list item: %q", line)
			}
			if strings.HasPrefix(line, "#") {
				t.Fatalf("Changes section forged heading: %q", line)
			}
			if strings.HasPrefix(line, "```") {
				t.Fatalf("Changes section opened code fence: %q", line)
			}
		}
		if fileLines > prBodyFileMax {
			t.Fatalf("changes listed %d files, cap is %d", fileLines, prBodyFileMax)
		}

		// Stack note invariants
		note := b.stackNote()
		for line := range strings.SplitSeq(note, "\n") {
			if strings.HasPrefix(line, "#") {
				t.Fatalf("Stack note forged heading: %q", line)
			}
			if strings.HasPrefix(line, "```") {
				t.Fatalf("Stack note opened code fence: %q", line)
			}
		}

		// Body line count bound: flattening guarantees bounded line count regardless of input size
		if lines := len(strings.Split(body, "\n")); lines > 40 {
			t.Fatalf("excessive lines in body: %d", lines)
		}
	})
}

func TestPRBodyNormalizesNFCToPreventRuneSplitting(t *testing.T) {
	nfdTitle := norm.NFD.String("fix(café): update entrée point")
	body := prBody{
		Title: nfdTitle,
		Files: []string{norm.NFD.String("café.go")},
	}.render()
	nfcTitle := norm.NFC.String(nfdTitle)
	if !strings.Contains(body, nfcTitle) {
		t.Fatalf("render did not normalize title to NFC: %q", body)
	}
	if !strings.Contains(body, norm.NFC.String("café.go")) {
		t.Fatalf("render did not normalize file to NFC: %q", body)
	}
}
