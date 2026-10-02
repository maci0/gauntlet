// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// A repository whose history is in its own script has to get the same branch
// names out of a stacked run as an ASCII one. Both slugs were ASCII-only
// character classes, so a review the reviewed repository names in Japanese or
// Russian was erased to the placeholder "review", and a commit subject it
// wrote in any non-Latin script distilled to "" so the layer never shed its
// provisional "-wip-" name: a reader opening the published branch found the
// internal name instead of the topic.
func TestSlugsNameNonLatinReviewsAndSubjects(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	cases := []struct{ review, subject, topic string }{
		{"日本語-review", "fix: 検索を高速化した", "検索を高速化した"},
		{"검증-review", "fix: 검색 속도 개선", "검색-속도-개선"},
		{"тест-review", "исправить: ускорить поиск", "ускорить-поиск"},
		{"مراجعة", "إصلاح: تسريع البحث", "تسريع-البحث"},
		{"Ασφάλεια", "fix: ενίσχυση ελέγχου", "ενίσχυση-ελέγχου"},
	}
	for _, c := range cases {
		if got := BranchSlug(c.review); got != c.review {
			t.Errorf("BranchSlug(%q) = %q, want the name whole", c.review, got)
		}
		if got := TopicSlug(c.subject); got != c.topic {
			t.Errorf("TopicSlug(%q) = %q, want %q", c.subject, got, c.topic)
		}
		// The composed and the decomposed spelling of one subject are one
		// commit, so they name one branch. A macOS filesystem hands out the
		// second spelling.
		const nfd = "fix: café" // "café" with a combining acute
		if got := TopicSlug(nfd); got != "café" {
			t.Errorf("TopicSlug(%q) = %q, want the composed spelling", nfd, got)
		}
		// Whatever the script, the result is a ref git will create.
		for _, name := range []string{
			StackLoopFinalBranch(1, 0, c.review, c.subject),
			StackLoopProvisionalBranch("abc1234def", 1, 0, c.review),
		} {
			if name == "" {
				t.Fatalf("review %q produced no branch name", c.review)
			}
			if err := r.ValidateBranchName(ctx, name); err != nil {
				t.Errorf("review %q made invalid ref %q: %v", c.review, name, err)
			}
		}
	}

	// Two distinct non-Latin reviews must not share a ref fragment, a lane
	// worktree directory, or a merge scratch directory. Under the ASCII-only
	// slug all three collapsed to "review", so two unrelated reviews shared
	// one checkout and one branch and overwrote each other.
	seen := map[string]string{}
	for _, review := range []string{
		"日本語-review", "한국어-review", "тест-review", "مراجعة", "Ασφάλεια",
	} {
		slug := BranchSlug(review)
		if prev, dup := seen[slug]; dup {
			t.Fatalf("reviews %q and %q share ref fragment %q", prev, review, slug)
		}
		seen[slug] = review
	}
}

// A letter with case is lowered by Unicode's own mapping, not by an ASCII
// comparison, so a Greek, Cyrillic, or Turkish letter reaches a reader of that
// language in the form they look for.
//
// The mapping is the locale-independent one, and that is the right choice for
// a ref name: Go's strings.ToLower maps Greek final sigma to sigma rather than
// to the final form, and U+0130 to a dotted i rather than to a dotless one,
// because the rule that differs between locales depends on context this
// function has none of. A ref has to be reproducible on every machine, and a
// locale-dependent branch name would name a different branch on the reader's
// machine than on the writer's.
func TestTopicSlugLowercasesInEveryScript(t *testing.T) {
	cases := map[string]string{
		"fix: ΕΛΕΓΧΟΣ ΚΑΙ ΕΛΕΓΧΟΣ": "ελεγχοσ-και-ελεγχοσ",
		"fix: ПРОВЕРКА ФАЙЛА":      "проверка-файла",
		"fix: İŞLEM KAYDI":         "işlem-kaydi",
		"fix: ÜBER UND ÜBER":       "über-und-über",
	}
	for subject, want := range cases {
		if got := TopicSlug(subject); got != want {
			t.Errorf("TopicSlug(%q) = %q, want %q", subject, got, want)
		}
	}
}

// topicSlugMax bounds the bytes of the ref fragment, not its runes. Charging a
// multi-byte letter one unit let a subject of two-byte Cyrillic overshoot the
// budget, and the fragment is what lands in refs/heads.
func TestTopicSlugBudgetCountsBytes(t *testing.T) {
	for _, subject := range []string{
		"исправить: " + strings.Repeat("слово ", 40),
		"修正: " + strings.Repeat("漢字 ", 40),
		"إصلاح: " + strings.Repeat("كلمة ", 40),
		"έλεγχος: " + strings.Repeat("λέξη ", 40),
		strings.Repeat("é", 100),
	} {
		got := TopicSlug(subject)
		if len(got) > topicSlugMax {
			t.Errorf("TopicSlug(%q) is %d bytes, over the %d budget: %q",
				subject, len(got), topicSlugMax, got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("TopicSlug(%q) cut a UTF-8 sequence: %q", subject, got)
		}
		if strings.HasSuffix(got, "-") {
			t.Errorf("TopicSlug(%q) ends on a hyphen: %q", subject, got)
		}
	}
}
