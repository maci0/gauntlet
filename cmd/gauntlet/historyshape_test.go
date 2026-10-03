// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// docs/CLI.md names the members of the `history` object, and a restore script
// is written from that sentence rather than from the Go tree. The `agents`
// count landed with a changelog entry and a RUNS.md example while the sentence
// in CLI.md kept listing five members, so the documented contract and the
// document a consumer receives disagreed and nothing said so: the count is the
// one member whose loss no other count reports, and the reader told to compare
// two trees was told to compare a set that left it out.
//
// Both directions are read, and the code side is the struct rather than a
// second list: a member the document gains without the prose following fails
// here, and a member the prose names without the document carrying it fails
// the other way, which is a script reading a count that is always absent.
func TestDocsCLIDocumentsEveryHistoryCount(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The enumeration itself, between the words that introduce it and the
	// parenthesis that closes it. The rest of the row is about stdout, exit
	// codes, and the other objects, and none of it lists members.
	const intro = "the rows: "
	from := strings.Index(string(data), intro)
	if from < 0 {
		t.Fatalf("docs/CLI.md no longer describes the `history` object as %q; this test reads that sentence", intro)
	}
	prose := string(data)[from+len(intro):]
	if end := strings.Index(prose, ")"); end >= 0 {
		prose = prose[:end]
	}

	var members []string
	typ := reflect.TypeFor[historyJSON]()
	for field := range typ.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Errorf("historyJSON.%s carries no JSON name, so a consumer cannot read it",
				field.Name)
			continue
		}
		members = append(members, name)
		if !strings.Contains(prose, "`"+name+"`") {
			t.Errorf("docs/CLI.md does not document the `history` member %q; a restore script written from that page compares across two trees",
				name)
		}
	}
	// A backtick-quoted lowercase word is a member the page claims, which is
	// how it spells each of them; anything else in the sentence is a path, a
	// program, or prose. One the document does not carry is caught here rather
	// than by a restore that reads an absent field as a zero.
	for _, word := range quotedWords(prose) {
		if strings.ContainsFunc(word, func(r rune) bool { return r < 'a' || r > 'z' }) {
			continue
		}
		if !slices.Contains(members, word) {
			t.Errorf("docs/CLI.md documents a `history` member %q that `gauntlet runs --json` does not carry", word)
		}
	}
}

// quotedWords returns every `backtick`-quoted span in s, in order.
func quotedWords(s string) []string {
	var words []string
	for rest := s; ; {
		i := strings.IndexByte(rest, '`')
		if i < 0 {
			return words
		}
		rest = rest[i+1:]
		j := strings.IndexByte(rest, '`')
		if j < 0 {
			return words
		}
		words = append(words, rest[:j])
		rest = rest[j+1:]
	}
}
