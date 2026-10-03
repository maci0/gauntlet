// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package evidence

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
)

// Domain evidence must come from implementation, not README vocabulary,
// manifest descriptions, test fixtures, comments, or strings. This bounded
// lexical pass masks comments and quoted text before recognizing identifiers;
// it is a hint of domain behavior, not a parser or proof of an invariant.
var domainNonCode = regexp.MustCompile("(?s)/\\*.*?(?:\\*/|$)|//[^\\n]*|#[^\\n]*|\"\"\".*?(?:\"\"\"|$)|'''.*?(?:'''|$)|\"(?:\\\\.|[^\"\\\\])*(?:\"|$)|'(?:\\\\.|[^'\\\\])*(?:'|$)|`[^`]*(?:`|$)")
var domainIdentifier = regexp.MustCompile(`[A-Za-z_$][A-Za-z_0-9$]*`)
var domainPath = regexp.MustCompile(`(?:^|/)(?:domain|domains|aggregates?|value[_-]?objects?|bounded[_-]?contexts?)(?:/|$)`)
var tacticalPath = regexp.MustCompile(`(?:^|[/._-])(?:aggregates?|value[_-]?objects?|domain[_-]?events?|bounded[_-]?contexts?)(?:[/._-]|$)`)
var aggregateAnnotation = regexp.MustCompile(`@(?:[A-Za-z_][A-Za-z_0-9]*\.)*[Aa]ggregate\b`)
var domainArrow = regexp.MustCompile(`^=\s*(?:async\s+)?(?:\([^{};\n]*\)|[A-Za-z_$][A-Za-z_0-9$]*)\s*=>`)

// domainSignals supplements the import and API markers with model semantics.
// Generic entity/repository/service names need a domain boundary and guarded
// behavior. Explicit tactical building blocks stand on their own. Operations
// on recognizable business concepts can establish a domain without DDD names.
func domainSignals(s *signals, rel string, head []byte) {
	ext := strings.ToLower(filepath.Ext(rel))
	base := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	if !sourceExts[ext] || ext == ".sql" || ext == ".sh" || ext == ".bash" || ext == ".zsh" ||
		isTestFile(filepath.Base(rel)) || strings.HasSuffix(base, "Test") || strings.HasSuffix(base, "Tests") ||
		strings.HasSuffix(base, "Spec") || strings.HasSuffix(base, "Specs") {
		return
	}
	for part := range strings.SplitSeq(strings.ToLower(filepath.ToSlash(rel)), "/") {
		switch part {
		case "test", "tests", "spec", "__tests__", "testdata", "fixtures", "examples":
			return
		}
	}
	code := domainNonCode.ReplaceAll(head, []byte(" "))
	ids := domainIdentifier.FindAllIndex(code, -1)
	tactical := aggregateAnnotation.Match(code)
	var aggregate, business, operation, constructor, guarded, invariant, state, rejection, model bool
	for _, id := range ids {
		raw := string(code[id[0]:id[1]])
		lower := strings.ToLower(raw)
		name := strings.ReplaceAll(lower, "_", "")
		for _, suffix := range []string{"aggregateroot", "valueobject", "domainevent", "domainservice", "boundedcontext", "businessrule", "domainexception", "businessrulevalidationexception"} {
			if strings.HasSuffix(name, suffix) {
				tactical = true
			}
		}
		aggregate = aggregate || strings.HasSuffix(name, "aggregate") && raw[0] >= 'A' && raw[0] <= 'Z'
		for _, suffix := range []string{"order", "invoice", "account", "reservation", "booking", "inventory", "balance", "subscription", "payment", "entitlement", "auction", "approval", "waiver", "quota", "budget", "license", "contract", "wallet", "money", "price", "quantity", "currency"} {
			if strings.HasSuffix(name, suffix) {
				business = true
			}
		}
		rest := bytes.TrimSpace(code[id[1]:])
		callable := len(rest) > 0 && (rest[0] == '(' || rest[0] == '=' && domainArrow.Match(rest))
		for _, value := range []string{"money", "price", "quantity", "currency"} {
			if callable && (name == value || strings.HasSuffix(name, value) && strings.HasPrefix(lower, "new")) {
				constructor = true
			}
		}
		for _, prefix := range []string{"approve", "reject", "cancel", "reserve", "withdraw", "deposit", "debit", "credit", "refund", "redeem", "fulfill", "checkout", "placeorder", "place_order", "transfer", "bid", "settle", "charge", "consume", "renew", "grant", "revoke"} {
			if callable && strings.HasPrefix(lower, prefix) && (len(raw) == len(prefix) || raw[len(prefix)] == '_' || raw[len(prefix)] >= 'A' && raw[len(prefix)] <= 'Z') {
				operation = true
			}
		}
		for _, prefix := range []string{"validate", "ensure", "checkinvariant"} {
			if callable && strings.HasPrefix(name, prefix) {
				guarded = true
				invariant = true
			}
		}
		switch name {
		case "if", "switch", "match", "raise", "throw", "assert", "guard":
			guarded = true
		case "class", "interface", "struct", "type", "record", "enum", "impl", "def", "func", "fn", "fun", "function":
			model = true
		}
		state = state || name == "state" || name == "status"
		rejection = rejection || name == "raise" || name == "throw" || name == "assert" || strings.HasPrefix(raw, "Err")
	}
	lowerPath := strings.ToLower(filepath.ToSlash(rel))
	if model && (tactical || tacticalPath.MatchString(lowerPath) || aggregate && (business || domainPath.MatchString(lowerPath))) {
		s.mark["ddd_model"] = 1
	}
	if guarded && (business && (operation || constructor) || model && (invariant || state && rejection) && domainPath.MatchString(lowerPath)) {
		s.mark["ddd_behavior"] = 1
	}
}
