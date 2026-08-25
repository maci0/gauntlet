// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package prompt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maci0/gauntlet/internal/fuzzy"
)

// Sets are shorthands usable anywhere a review name is: --reviews quick,
// --exclude frontend, --reviews backend,llm-review. Names missing from the
// available set are dropped silently, so a set stays usable when a
// --prompt-dir carries only some of them.
var Sets = map[string][]string{
	// Applies to essentially any codebase, cheapest useful pass.
	"quick": {
		"code-review", "sec-review", "error-review", "functionality-review",
		"test-review",
	},
	// Quick plus the broadly applicable quality and hygiene reviews.
	"standard": {
		"code-review", "sec-review", "error-review", "functionality-review",
		"test-review", "perf-review", "deps-review", "doc-review",
		"arch-review", "design-review", "specs-review", "concurrency-review",
		"minimalism-review", "slop-review", "lint-review", "compat-review",
		"time-review", "numerics-review", "resource-review",
	},
	"security": {
		"sec-review", "deps-review", "privacy-review", "config-review",
		"fuzz-review", "llm-review", "threat-review", "authz-review",
	},
	"frontend": {
		"ux-review", "a11y-review", "uislop-review", "i18n-review",
		"webperf-review", "mobile-review", "unicode-review",
	},
	"backend": {
		"api-review", "db-review", "error-review", "concurrency-review",
		"idempotency-review", "o11y-review", "perf-review", "dst-review",
		"authz-review", "cache-review", "dr-review",
	},
	// Repos that ship instructions for AI agents.
	"agents": {
		"prompt-review", "skills-review", "agentrules-review", "llm-review",
	},
	"shipping": {
		"release-review", "pkg-review", "build-review", "deps-review",
		"doc-review", "cli-review", "sdk-review", "infra-review",
		"container-review", "dx-review",
	},
}

// DynamicSets are computed from what discovery found rather than listed by name.
var DynamicSets = map[string]string{
	"all":     "every discovered review",
	"project": "only reviews found in the target tree, not the bundled set",
}

// Suggest is the reserved --reviews keyword: an agent inspects the repo and
// picks the relevant reviews. It is not in DynamicSets because it cannot
// compose with other names and needs an agent run plus confirmation before the
// loop starts.
const Suggest = "suggest"

// SetNames lists every named set, sorted.
func SetNames() []string {
	out := make([]string, 0, len(Sets)+len(DynamicSets))
	for n := range Sets {
		out = append(out, n)
	}
	for n := range DynamicSets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Expand turns a comma-separated list of review names and set names into
// review names, keeping order and repeats. Repeats are weight: naming a review
// or a set twice schedules its reviews twice per loop. The "-review" suffix
// may be omitted.
//
// emptyOK allows a set that matches nothing to be a no-op (used by --exclude,
// where excluding a set with no members is valid rather than a typo).
func (s Set) Expand(list, flag string, emptyOK bool) ([]string, error) {
	var out []string
	var emptySets []string
	unknown := map[string]bool{}

	for raw := range strings.SplitSeq(list, ",") {
		// Names are identity and discovery stores them NFC-normalized; a
		// decomposed spelling typed on the command line must find the same
		// review (see nfc).
		name := nfc(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		_, dynamic := DynamicSets[name]
		members, named := Sets[name]
		switch {
		case dynamic || named:
			var expanded []string
			switch {
			case name == "all":
				expanded = append(expanded, s.Names...)
			case name == "project":
				expanded = s.ProjectNames()
			default:
				for _, m := range members {
					if _, ok := s.byName[m]; ok {
						expanded = append(expanded, m)
					}
				}
			}
			if len(expanded) == 0 {
				emptySets = append(emptySets, name)
			}
			out = append(out, expanded...)
		case s.byName[name].Name != "":
			out = append(out, name)
		case s.byName[name+"-review"].Name != "":
			out = append(out, name+"-review")
		default:
			unknown[name] = true
		}
	}

	if len(unknown) > 0 {
		return nil, s.unknownError(unknown, flag)
	}
	if len(emptySets) > 0 && len(out) == 0 && !emptyOK {
		return nil, fmt.Errorf("set(s) in %s matched no available reviews: %s",
			flag, strings.Join(emptySets, ", "))
	}
	return out, nil
}

// unknownError reports unknown names with "did you mean" hints. Suggestions
// match sets, full names, and suffixless stems, since all three are accepted
// input, and compare case-insensitively so ALL and Quick still hint.
func (s Set) unknownError(unknown map[string]bool, flag string) error {
	candidates := make([]string, 0, len(s.Names)*2+len(Sets)+len(DynamicSets))
	candidates = append(candidates, s.Names...)
	for _, n := range s.Names {
		candidates = append(candidates, strings.TrimSuffix(n, "-review"))
	}
	candidates = append(candidates, SetNames()...)

	names := make([]string, 0, len(unknown))
	for n := range unknown {
		names = append(names, n)
	}
	sort.Strings(names)

	described := make([]string, 0, len(names))
	for _, n := range names {
		if c := fuzzy.Closest(n, candidates); c != "" {
			described = append(described, fmt.Sprintf("%s (did you mean %q?)", n, c))
			continue
		}
		described = append(described, n)
	}
	return fmt.Errorf("unknown review(s) in %s: %s\nSets: %s\nReviews: %s",
		flag, strings.Join(described, ", "),
		strings.Join(SetNames(), ", "), strings.Join(s.Names, ", "))
}
