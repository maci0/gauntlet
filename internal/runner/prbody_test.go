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
	long := strings.Repeat("é", 4000)
	body := prBody{Title: long, Scope: long, Files: []string{long},
		Base: long, Root: long, Layer: 2}.render()
	// The cap counts runes, not bytes: an ASCII fixture makes the two agree,
	// so a body of multi-byte runes is what actually pins the limit.
	if n := utf8.RuneCountInString(body); n > prBodyMax {
		t.Fatalf("body is %d runes, cap is %d", n, prBodyMax)
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

// An agent's per-file note, and a project review's Summary line, both reach
// the prose of a pull request after reading the same untrusted repository. A
// link or an image needs no line break of its own to be markup, so flattening
// the line is not enough: without the inline escapes, a note renders a
// tracking pixel and a look-alike host as part of the review summary.
func TestPRBodyNeutralizesInlineMarkup(t *testing.T) {
	body := prBody{
		Title: "fix(index): rescan less",
		Scope: "[see the tracker](https://tracker.invalid/x) and <https://tracker.invalid/y>",
		Overview: "![pixel](https://tracker.invalid/p.gif) counted the rebuild; " +
			"see <details>the log</details>, and *reverted* the old path",
		Files: []string{"internal/cache/store.go"},
		Base:  "main", Root: "main", Layer: 1,
	}.render()
	// The Summary section is prose that came from the agent or the
	// repository; the file list below it is paths in code spans, where a
	// bracket is a bracket. No inline delimiter may survive the prose.
	summary, _, _ := strings.Cut(body, "\n\n## Changes")
	if d := unescapedDelimiter(summary); d != "" {
		t.Fatalf("unescaped %q survived into the summary:\n%s", d, body)
	}
	// The text is still there, escaped rather than dropped: a reader who
	// wants to know what the note said can still see it.
	if !strings.Contains(body, "tracker.invalid") {
		t.Fatalf("the note's own text was dropped instead of escaped:\n%s", body)
	}
}

// A backtick in prose opens a code span that runs to the next backtick, so an
// unpaired one in an agent's note swallows every following word of the
// paragraph. The replacement belongs in the flattening rather than at one
// call site: the title, the overview, and a declared scope are all untrusted
// text read from the reviewed repository, and a span left open in any of them
// takes the rest of the section with it.
func TestPRBodyProseCarriesNoBacktick(t *testing.T) {
	body := prBody{
		Title:    "fix: restore the `parser` path",
		Scope:    "`internal/cache` and `internal/gitx`",
		Overview: "the `parser` path reopened a span",
		Files:    []string{"internal/cache/store.go"},
		Base:     "main", Root: "main", Layer: 1,
	}.render()
	summary, _, _ := strings.Cut(body, "\n\n## Changes")
	if strings.Contains(summary, "`") {
		t.Fatalf("an unpaired backtick survived into the summary:\n%s", body)
	}
	// The text is still there, and the file list, which is a code span by
	// design, is unaffected.
	if !strings.Contains(summary, "'parser'") {
		t.Fatalf("the backtick was dropped instead of replaced:\n%s", body)
	}
	if !strings.Contains(body, "- `internal/cache/store.go`") {
		t.Fatalf("the file list lost its code span:\n%s", body)
	}
}

// unescapedDelimiter returns the first "[" "]" "<" or ">" a Markdown
// renderer would still act on, or "" when the text carries none.
func unescapedDelimiter(s string) string {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[', ']', '<', '>':
			// A backslash before it is the escape, and the escape itself is
			// only a backslash when an odd number of them precedes it.
			n := 0
			for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
				n++
			}
			if n%2 == 0 {
				return string(s[i])
			}
		}
	}
	return ""
}

// A path holds brackets and globs routinely, and none of them is markup
// inside a code span, so escaping them there would put backslashes into every
// path a reader copies out of the body.
func TestPRBodyPathsRenderWithoutEscapes(t *testing.T) {
	body := prBody{
		Title: "chore: tidy",
		Files: []string{"src/[id]/*.go", "src/glob<*>.go"},
		Base:  "main", Root: "main", Layer: 1,
	}.render()
	if !strings.Contains(body, "- `src/[id]/*.go`\n") {
		t.Fatalf("a glob path was rewritten:\n%s", body)
	}
	if !strings.Contains(body, "- `src/glob<*>.go`\n") {
		t.Fatalf("an angle-bracket path was rewritten:\n%s", body)
	}
}

// FuzzPRBodyRender drives prBody.render and its formatting helpers (mdText,
// mdCode, changes, stackNote) with arbitrary untrusted inputs: titles,
// scopes, overviews, branch names, and raw file lists. It pins the structural
// and security invariants of the generated PR body: rendering never panics,
// output size is strictly bounded, internal newlines in fields cannot break out
// into multi-line injections, inline delimiters in prose are escaped, file
// lists are capped at prBodyFileMax, and code spans always have balanced
// delimiters.
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
			title:     "fix: a [](x)",
			scope:     "<img src=x onerror=alert(1)>",
			overview:  "![p](https://tracker.invalid/p) *reverted* <b>bold</b>",
			base:      "main",
			root:      "main",
			filesRaw:  "a.go",
			layer:     1,
			ins:       1,
			del:       1,
			haveLines: true,
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
		// Every inline delimiter a Markdown renderer acts on has to be gone
		// from prose: a link, an image, an autolink, or a raw tag is what a
		// note plants when it cannot open a line of its own.
		if d := unescapedDelimiter(txt); d != "" {
			t.Fatalf("mdText left %q unescaped: %q", d, txt)
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
