// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The project site is a real user-facing page, and the only one here that
// renders in a browser rather than a terminal, so WCAG 2.2 AA applies to it
// directly. Nothing in the build reads site/public: a color edited to taste
// there ships unreviewed, and the two custom properties this file checks were
// both under their floor the first time it ran (--muted 4.27:1 and --accent
// 4.46:1 on the light background, against a 4.5:1 text floor). The tokens are
// therefore pinned here, and the page's markup invariants alongside them.

// siteRoot is site/public, found by walking up from this package so the test
// does not depend on the working directory the suite happens to run in.
func siteRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	for range 8 {
		if _, err := os.Stat(filepath.Join(dir, "site", "public", "index.html")); err == nil {
			return filepath.Join(dir, "site", "public")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("site/public/index.html not found above this package")
	return ""
}

func readSiteFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(siteRoot(t), name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// siteToken matches one custom property's declaration. The page declares every
// property in the light :root block and overrides it in the dark one, so a
// property seen twice is a light value followed by a dark value.
var siteToken = regexp.MustCompile(`--([a-z-]+):\s*(#[0-9a-f]{6})`)

// siteTheme is every custom property the page declares, keyed by name, with
// the light value first and the dark-scheme value second. A property the dark
// block does not override has an empty second value, which the checks below
// read as "not declared for that scheme" rather than as a passing one.
func siteTheme(t *testing.T) map[string][2]string {
	t.Helper()
	theme := map[string][2]string{}
	for _, m := range siteToken.FindAllStringSubmatch(readSiteFile(t, "index.html"), -1) {
		if prev, seen := theme[m[1]]; seen {
			theme[m[1]] = [2]string{prev[0], m[2]}
			continue
		}
		theme[m[1]] = [2]string{m[2], ""}
	}
	return theme
}

// siteStyle is the page's inline style sheet, the only place it has one.
func siteStyle(t *testing.T) string {
	t.Helper()
	html := readSiteFile(t, "index.html")
	start, end := strings.Index(html, "<style>"), strings.Index(html, "</style>")
	if start < 0 || end < start {
		t.Fatal("the page has no inline style sheet to check")
	}
	return html[start:end]
}

// The site's deployment config is a line-oriented file Cloudflare reads and
// nothing in the build validates, so the two things that make it work are
// pinned here rather than discovered on a deploy. It is parsed by line, and a
// last line with no terminating newline is a truncated one to every reader
// that is not Cloudflare's parser, so the header that matters most, the
// transport one, is the one a truncated file drops silently. The pattern line
// is the other: a `_headers` file that never names a path applies to nothing,
// and the page it was written for then serves with none of these headers.
func TestSiteHeadersAreTerminatedAndScoped(t *testing.T) {
	raw := readSiteFile(t, "_headers")
	if !strings.HasSuffix(raw, "\n") {
		t.Error("site/public/_headers does not end with a newline; the last header is read as a truncated line and is dropped")
	}
	if !strings.Contains(raw, "\n/*\n") {
		t.Error("site/public/_headers names no path pattern, so the headers apply to no asset and the site serves with none of them")
	}
	for _, want := range []string{
		"Content-Security-Policy:",
		"Referrer-Policy:",
		"X-Content-Type-Options: nosniff",
		"X-Frame-Options: DENY",
		"Strict-Transport-Security:",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("site/public/_headers is missing %q", want)
		}
	}
}

// channel is one sRGB channel at 0..1, linearized the way WCAG 2.x defines it.
func channel(v float64) float64 {
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// relativeLuminance is the WCAG 2.x relative luminance of an sRGB hex color.
// siteToken only matches six-digit hex, so the parse cannot fail; a byte that
// would not parse reads as zero rather than stopping the run.
func relativeLuminance(hex string) float64 {
	h := strings.TrimPrefix(hex, "#")
	v := func(i int) float64 {
		n, _ := strconv.ParseUint(h[i:i+2], 16, 8)
		return channel(float64(n) / 255)
	}
	return 0.2126*v(0) + 0.7152*v(2) + 0.0722*v(4)
}

// siteContrast is the WCAG contrast ratio between two hex colors.
func siteContrast(a, b string) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// The surfaces the page's text sits on, one pair per color scheme, and the
// floors the terminal palette is held to in internal/ui: 4.5:1 for anything
// behind text (SC 1.4.3) and 3:1 for a stroke (SC 1.4.11).
const (
	lightPage   = "#f6f9fa"
	lightPanel  = "#e8eff2"
	darkPage    = "#11191e"
	darkPanel   = "#1a252c"
	siteText    = 4.5
	siteStroke  = 3.0
	schemeLight = 0
	schemeDark  = 1
)

// TestSiteClearsWCAGContrastFloors pins the page's own tokens on both color
// schemes. A token has to clear the floor on the page and on the panel, since
// the tagline, the footer and the code block sit on one or the other, and the
// primary button inverts the pair so its label is read against the fill.
func TestSiteClearsWCAGContrastFloors(t *testing.T) {
	theme := siteTheme(t)

	// value resolves a token for one scheme, reporting an undeclared one
	// rather than comparing a color that is not on the page.
	value := func(name string, scheme int) (string, bool) {
		pair, ok := theme[name]
		if !ok {
			t.Errorf("--%s is not declared on the page", name)
			return "", false
		}
		if pair[scheme] == "" {
			t.Errorf("--%s has no %s-scheme value", name,
				map[int]string{schemeLight: "light", schemeDark: "dark"}[scheme])
			return "", false
		}
		return pair[scheme], true
	}

	for _, name := range []string{"fg", "muted", "accent"} {
		for _, scheme := range []int{schemeLight, schemeDark} {
			fg, ok := value(name, scheme)
			if !ok {
				continue
			}
			page, panel := lightPage, lightPanel
			if scheme == schemeDark {
				page, panel = darkPage, darkPanel
			}
			if got := siteContrast(fg, page); got < siteText {
				t.Errorf("--%s %q is %.2f:1 on %s, want at least %.1f",
					name, fg, got, page, siteText)
			}
			if got := siteContrast(fg, panel); got < siteText {
				t.Errorf("--%s %q is %.2f:1 on the panel %s, want at least %.1f",
					name, fg, got, panel, siteText)
			}
		}
	}

	// The primary button inverts the pair: --accent is its fill and --bg the
	// label on it. That is where a link color is easiest to leave under the
	// floor, because there it is never read as text on the page itself.
	for _, scheme := range []int{schemeLight, schemeDark} {
		label, ok1 := value("bg", scheme)
		fill, ok2 := value("accent", scheme)
		if !ok1 || !ok2 {
			continue
		}
		if got := siteContrast(label, fill); got < siteText {
			t.Errorf("the primary button label %q is %.2f:1 on the fill %q, want at least %.1f",
				label, got, fill, siteText)
		}
	}

	// The focus ring is drawn in --fg against the page, so it only owes the
	// non-text floor: SC 2.4.7 asks for a visible indicator, not a legible
	// one. Pinned anyway, because --fg is the token the rule reads.
	for _, scheme := range []int{schemeLight, schemeDark} {
		fg, ok := value("fg", scheme)
		if !ok {
			continue
		}
		page := lightPage
		if scheme == schemeDark {
			page = darkPage
		}
		if got := siteContrast(fg, page); got < siteStroke {
			t.Errorf("the focus ring %q is %.2f:1 on %s, want at least %.1f",
				fg, got, page, siteStroke)
		}
	}
}

// TestSiteKeepsAKeyboardFocusIndicator holds the ring the page draws. A link
// with an outline removed and no replacement is invisible to a keyboard user,
// and the removal is invisible to everyone else (SC 2.4.7).
func TestSiteKeepsAKeyboardFocusIndicator(t *testing.T) {
	block := siteStyle(t)
	if !strings.Contains(block, ":focus-visible") {
		t.Error("the page draws no focus indicator: every link is tabbed past unannounced")
	}
	// A rule that takes the outline away without drawing another one is the
	// failure this test exists for, so both halves are pinned.
	if regexp.MustCompile(`outline:\s*(none|0)\b`).MatchString(block) {
		t.Error("the page removes an outline without replacing it")
	}
	// The rule has to reach every link, not one kind of link, so it is
	// attached to a bare a and not to a class.
	if !regexp.MustCompile(`(^|[^-\w.])a:focus-visible`).MatchString(block) {
		t.Error("the focus rule is not attached to a bare a selector, so some links have no ring")
	}
}

// TestSiteReflowsWithoutHorizontalScroll holds the page to 320 CSS pixels
// (SC 1.4.10). A scroll container inside the page is a scrollbar for the whole
// document there, so the code block wraps rather than scrolling sideways.
func TestSiteReflowsWithoutHorizontalScroll(t *testing.T) {
	block := siteStyle(t)
	if !strings.Contains(block, "white-space: pre-wrap") {
		t.Error("the code block does not wrap, so the page scrolls sideways at 320px (SC 1.4.10)")
	}
	if strings.Contains(block, "overflow-x: auto") {
		t.Error("the code block still scrolls sideways, which is a horizontal scrollbar for the page")
	}
	// A width pinned in pixels on a property that is not max-width or
	// min-width is the same reflow failure in a different place. Both of
	// those are fine: max-width still lets the box shrink, and min-width
	// is checked against the 320px width below. The style sheet is one
	// declaration per line, so the property name and its value are on the
	// same line and can be matched together.
	if hit := regexp.MustCompile(`(?m)^[^;{}]*[;{ ]\s*width:\s*(\d+)px`).FindStringSubmatch(block); hit != nil {
		t.Errorf("a fixed %spx width in the style sheet blocks reflow (SC 1.4.10)", hit[1])
	}
	// 320 CSS pixels is the narrower of the two reflow widths; a page may not
	// demand more of either axis than the viewport has.
	if hit := regexp.MustCompile(`\bmin-width:\s*(\d+)px`).FindStringSubmatch(block); hit != nil {
		if w, _ := strconv.Atoi(hit[1]); w > 320 {
			t.Errorf("the style sheet demands %dpx of horizontal room, over the 320px reflow width", w)
		}
	}
}

// TestSiteNamesEveryImageAndTheLanguage holds the two things a screen reader
// reads before anything else: the document's language (SC 3.1.1) and an
// alternative for every image (SC 1.1.1). The logo is the one image that is
// decoration beside an h1 already carrying the name, so it is the one image
// allowed to say nothing.
func TestSiteNamesEveryImageAndTheLanguage(t *testing.T) {
	html := readSiteFile(t, "index.html")
	if !strings.Contains(html, `<html lang="en">`) {
		t.Error(`the document has no lang attribute, so a screen reader picks a voice by guess (SC 3.1.1)`)
	}
	for _, tag := range regexp.MustCompile(`<img\b[^>]*>`).FindAllString(html, -1) {
		alt := regexp.MustCompile(`\balt="([^"]*)"`).FindStringSubmatch(tag)
		if alt == nil {
			t.Errorf("an image carries no alt attribute at all: %s", tag)
			continue
		}
		if strings.TrimSpace(alt[1]) == "" && !strings.Contains(tag, "logo-") {
			t.Errorf("a content image has an empty alt: %s", tag)
		}
	}
	// The dashboard screenshot is the page's only picture of the product, so
	// its alt has to say what is in it rather than name the file.
	shot := regexp.MustCompile(`<img[^>]*class="shot"[^>]*>`).FindString(html)
	if shot == "" {
		t.Fatal("the dashboard screenshot is gone from the page")
	}
	alt := regexp.MustCompile(`\balt="([^"]*)"`).FindStringSubmatch(shot)
	if alt == nil || len(strings.Fields(alt[1])) < 8 {
		t.Errorf("the screenshot's alt describes nothing a reader can picture: %v", alt)
	}
}

// TestSiteNamesEveryLinkAndHeading holds the two structural invariants a
// screen reader navigates by: link text that says where the link goes
// (SC 2.4.4), and one h1 with no level skipped below it (SC 1.3.1).
func TestSiteNamesEveryLinkAndHeading(t *testing.T) {
	html := readSiteFile(t, "index.html")
	for _, bad := range []string{"click here", "here", "read more", "learn more", "link"} {
		if regexp.MustCompile(`(?i)>\s*` + regexp.QuoteMeta(bad) + `\s*<`).MatchString(html) {
			t.Errorf("a link reads %q, which says nothing about where it goes", bad)
		}
	}
	if n := strings.Count(html, "<h1"); n != 1 {
		t.Errorf("the page has %d h1 elements, want exactly one", n)
	}
	prev := 0
	for _, m := range regexp.MustCompile(`<h([1-6])\b`).FindAllStringSubmatch(html, -1) {
		lvl, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("bad heading level %q", m[1])
		}
		if prev != 0 && lvl > prev+1 {
			t.Errorf("heading level jumps from h%d to h%d (SC 1.3.1)", prev, lvl)
		}
		prev = lvl
	}
}
