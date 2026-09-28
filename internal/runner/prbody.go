// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// The body a stacked layer's pull request opens with. The title says what the
// change is called; the body has to say enough that someone who did not run
// it can tell what they are looking at before they open the diff: which area
// was examined, which files moved, and where the branch sits in the chain.

package runner

import (
	"fmt"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
)

// Bounds on every part of the body that comes from outside this process. A
// path list is the part that grows without limit, so it is counted past ten
// rather than pasted in full: a body is an orientation, not an inventory, and
// the file list is one click away in the diff. The rune caps keep a single
// hostile path or prompt line from pushing the rest of the body off the page.
const (
	prBodyFileMax  = 10
	prBodyPathMax  = 120
	prBodyTitleMax = 120
	prBodyScopeMax = 160
	// prBodyOverviewMax bounds the overview paragraph. It is assembled from
	// per-file notes, so without a cap a review that touched many files would
	// push the file list and stack note off the page.
	prBodyOverviewMax = 400
	// prBodyMax caps the whole rendered body. The per-field caps above make
	// reaching it need many hostile inputs at once, but nothing below relies
	// on that arithmetic staying true as fields are added.
	prBodyMax = 8000
)

// prBody is what one stacked layer knows about itself at publication time.
// Every string in it originates outside this process (an agent's subject, a
// prompt file, paths from the reviewed repository), so rendering sanitizes
// and bounds rather than trusting the caller to have done it.
type prBody struct {
	Title string   // the commit subject, which is also the PR title
	Scope string   // the review's declared subject area; empty when it declares none
	Files []string // paths this layer's single commit touches
	// Overview is a one-paragraph account of what the change did, assembled
	// from the agent's per-file notes. It rides in the Summary section: the
	// title says what the change is called, this says what it is about.
	Overview string
	Ins      int
	Del      int
	// HaveLines distinguishes a measured zero from an unmeasured one. A diff
	// stat that could not be read is missing, and missing is not "0 changed".
	HaveLines bool
	Base      string // the branch this PR merges into
	Root      string // the branch the whole chain is cut from
	Layer     int    // 1-based position in the chain; 0 leaves the stack note out
}

// render writes the Markdown body. The shape follows the repository's own
// pull requests: an `## Summary` heading first, sections after it, prose over
// tables. Nothing here names the tool, the pass, or the agent that wrote the
// change, for the same reason commit subjects do not.
func (b prBody) render() string {
	var sb strings.Builder
	sb.WriteString("## Summary\n\n")
	sb.WriteString(mdText(b.Title, prBodyTitleMax))
	// Backticks are replaced like mdCode does, for the opposite reason: in
	// prose position one would open a code span that swallows what follows.
	if ov := strings.ReplaceAll(mdText(b.Overview, prBodyOverviewMax), "`", "'"); ov != "" {
		sb.WriteString("\n\n" + ov)
	}
	if scope := mdText(b.Scope, prBodyScopeMax); scope != "" {
		sb.WriteString("\n\nScope: " + strings.TrimSuffix(scope, ".") + ".")
	}
	if changes := b.changes(); changes != "" {
		sb.WriteString("\n\n## Changes\n\n" + changes)
	}
	if note := b.stackNote(); note != "" {
		sb.WriteString("\n\n## Stack\n\n" + note)
	}
	sb.WriteString("\n")
	return normalize.Truncate(norm.NFC.String(sb.String()), prBodyMax)
}

// changes lists the touched paths and the size of the change. Either half can
// be missing on its own: a diff stat that would not read still leaves the
// paths worth printing, and the reverse holds too.
func (b prBody) changes() string {
	var lines []string
	shown := b.Files
	if len(shown) > prBodyFileMax {
		shown = shown[:prBodyFileMax]
	}
	for _, path := range shown {
		lines = append(lines, "- "+mdCode(path, prBodyPathMax))
	}
	if rest := len(b.Files) - len(shown); rest > 0 {
		lines = append(lines, fmt.Sprintf("- and %s", humanize.Plural(rest, "more file", "more files")))
	}
	if b.HaveLines {
		stat := fmt.Sprintf("%s changed, %s, %s.",
			humanize.Plural(len(b.Files), "file", "files"),
			humanize.Plural(b.Ins, "insertion", "insertions"),
			humanize.Plural(b.Del, "deletion", "deletions"))
		if len(lines) > 0 {
			stat = "\n" + stat
		}
		lines = append(lines, stat)
	}
	return strings.Join(lines, "\n")
}

// stackNote says where the branch sits, because a stacked PR's comparison is
// against its predecessor rather than the chain's root: without this, a diff
// that looks complete is read as if it were the whole change, and a merge in
// the wrong order silently pulls in a layer nobody approved.
func (b prBody) stackNote() string {
	switch {
	case b.Layer <= 0 || b.Base == "":
		return ""
	case b.Base == b.Root:
		return fmt.Sprintf("First layer of a stack, cut from %s. Anything stacked after it "+
			"branches from here, so this one merges first.", mdCode(b.Root, prBodyPathMax))
	case b.Root == "":
		return fmt.Sprintf("Layer %d of a stack, targeting %s rather than the chain's root, so "+
			"this comparison holds only this change. The base merges first.",
			b.Layer, mdCode(b.Base, prBodyPathMax))
	default:
		return fmt.Sprintf("Layer %d of a stack, targeting %s rather than %s, so this comparison "+
			"holds only this change. The base merges first.",
			b.Layer, mdCode(b.Base, prBodyPathMax), mdCode(b.Root, prBodyPathMax))
	}
}

// mdText renders untrusted text as one line of prose. Line breaks become
// spaces before Sanitize would drop them outright: a heading or a list item
// only reads as markup at the start of a line, so flattening is what makes an
// injected "## " inert, and doing it with a space rather than nothing keeps
// the words on either side from being welded into one.
func mdText(s string, limit int) string {
	return normalize.Truncate(escapeInline(norm.NFC.String(mdFlat(s))), limit)
}

// mdFlat collapses untrusted text to one sanitized line, without deciding what
// a Markdown renderer makes of the characters left.
func mdFlat(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(normalize.Sanitize(s)), " ")
}

// escapeInline escapes the inline constructs a Markdown renderer turns into
// markup without needing a line break first, which is all mdFlat can defuse.
//
// Every string rendered in prose here is model output (an agent's per-file
// note, the subject it wrote) or repository content (a project's review
// Summary line), and both reach it after a prompt that read the same
// untrusted sources. A link, an image, or a raw tag is what survives: the
// reader of a pull request is shown a tracking pixel, a look-alike host, or a
// <details> block that hides the real diff, and nothing in the file list or
// the commit history says where the markup came from. Escaping makes such a
// construct render as the text it is, which is the honest reading of a note
// that was never markup to begin with.
func escapeInline(s string) string {
	return inlineEscaper.Replace(s)
}

// inlineEscaper escapes Markdown's inline delimiters. "[" and "]" close a
// link or image label, "<" and ">" an autolink or a raw HTML tag, and "*" a
// span of emphasis or strong text, which is a quieter version of the same
// problem: a note that italicizes "reverted" says something the agent never
// did. Every one of these renders as its own character once escaped, so the
// reader still sees the note in full.
var inlineEscaper = strings.NewReplacer(
	`\`, `\\`,
	`[`, `\[`,
	`]`, `\]`,
	`<`, `&lt;`,
	`>`, `&gt;`,
	`*`, `\*`,
)

// mdCode renders untrusted text as a Markdown code span. A backtick in a
// repository path would close the span early and let whatever follows be read
// as markup; it is replaced rather than dropped, so the reader can see a
// character was there instead of silently getting a path that does not exist.
// Nothing inside a code span is markup, so the inline escaper is not applied:
// it would put backslashes into every path holding a bracket or a wildcard.
func mdCode(s string, limit int) string {
	return "`" + strings.ReplaceAll(normalize.Truncate(norm.NFC.String(mdFlat(s)), limit), "`", "'") + "`"
}
