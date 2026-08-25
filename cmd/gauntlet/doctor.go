// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/maci0/gauntlet/internal/agent"
)

// doctor reports which agent CLIs and helper tools are installed. It returns
// the process exit code: 1 when no agent can be launched at all.
func doctor(out io.Writer, pal palette, overrides map[string]string, width int) int {
	// One parallel probe for every binary, instead of one blocking lookup per
	// question. This is the difference between a snappy doctor and a second of
	// stat calls on a cold cache.
	found := agent.ResolveMany(append(agent.AllProbeNames(), agent.CustomNames()...))
	have := func(name string) bool {
		for alt := range strings.SplitSeq(name, "|") {
			if found[alt] != "" {
				return true
			}
		}
		return false
	}
	mark := func(ok bool, missing string) string {
		if ok {
			return pal.green("✓")
		}
		if missing == "yellow" {
			return pal.yellow("✗")
		}
		return pal.dim("✗")
	}
	ratio := func(n, total int) string {
		s := fmt.Sprintf("%d/%d", n, total)
		switch {
		case n == total:
			return pal.green(s)
		case n > 0:
			return pal.yellow(s)
		default:
			return pal.red(s)
		}
	}

	// Defined agents (the pi family, and anything in ~/.gauntlet/agents.json)
	// are as real as the compiled-in ones and belong in the inventory.
	agents := agent.AllNames()

	fmt.Fprintln(out, pal.bold("Agent CLIs")+pal.dim("  (at least one required)"))
	installed, usable := 0, 0
	for _, a := range agents {
		ok := found[a] != ""
		if ok {
			installed++
		}
		note := ""
		if def, defined := agent.CustomDef(a); defined {
			// A definition names its own binary, which may not have been in
			// the probe list.
			if !ok && agent.Resolve(def.Argv[0]) != "" {
				ok = true
			}
			extra := "defined"
			if def.Note != "" {
				extra += ": " + def.Note
			}
			note = pal.dim("  " + extra)
		}
		if path := overrides[a]; path != "" {
			// An override names the executable directly, so "usable" means it
			// exists and could be executed; a broken override must not make
			// doctor report a working setup (or exit 0) on an empty box.
			if binRunnable(path) {
				ok = true
				note = pal.dim("  --bin " + path)
			} else {
				note = pal.red("  --bin " + path + " is not a runnable file")
			}
		} else if agent.IsOptIn(a) && note == "" {
			note = pal.dim("  opt-in: name it with --agents")
		} else if a == "dsh" && !ok && found["bunx"] != "" {
			ok = true // launchable, but only when named: bunx fetches on first use
			note = pal.dim("  via bunx (@deepseek-ai/dsh); name it with --agents")
		}
		if ok && !agent.IsOptIn(a) {
			usable++
		}
		fmt.Fprintf(out, "  %s %s%s\n", mark(ok, "dim"), a, note)
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, pal.bold("Core tools")+pal.dim("  (used by every review)"))
	coreHave := 0
	for _, c := range agent.CoreTools {
		ok := have(c.Name)
		if ok {
			coreHave++
		}
		label := strings.ReplaceAll(c.Name, "|", " or ")
		fmt.Fprintf(out, "  %s %-24s %s\n", mark(ok, "yellow"), label, pal.dim(c.Purpose))
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, pal.bold("Per-review helpers")+pal.dim("  (* = worth installing anywhere)"))
	reviews := make([]string, 0, len(agent.ReviewTools)+len(agent.ReviewsWithoutTools))
	for r := range agent.ReviewTools {
		reviews = append(reviews, r)
	}
	reviews = append(reviews, agent.ReviewsWithoutTools...)
	sort.Strings(reviews)
	nameCol := 0
	for _, r := range reviews {
		nameCol = max(nameCol, len(r))
	}
	nameCol++

	// Tallies are over unique binaries: many tools serve more than one review.
	seenRec := map[string]bool{}
	seenOpt := map[string]bool{}
	for _, review := range reviews {
		tools := agent.ReviewTools[review]
		if len(tools) == 0 {
			fmt.Fprintf(out, "  %-*s %s\n", nameCol, review, pal.dim("no external tools"))
			continue
		}
		n := 0
		var cells []string
		for _, t := range tools {
			ok, rec := found[t] != "", agent.RecommendedTools[t]
			if ok {
				n++
			}
			if rec {
				seenRec[t] = ok || seenRec[t]
			} else {
				seenOpt[t] = ok || seenOpt[t]
			}
			star := ""
			if rec {
				star = "*"
			}
			label := t + star
			if !ok {
				// The '*' carries the recommended distinction when color is
				// off; styling only reinforces it.
				if rec {
					label = pal.bold(label)
				} else {
					label = pal.dim(label)
				}
			}
			missing := "dim"
			if rec {
				missing = "yellow"
			}
			cells = append(cells, mark(ok, missing)+" "+label)
		}
		head := fmt.Sprintf("  %-*s %s ", nameCol, review, ratio(n, len(tools)))
		fmt.Fprintln(out, head+strings.Join(cells, "  "))
	}

	recHave, optHave := 0, 0
	var missingRec []string
	for t, ok := range seenRec {
		if ok {
			recHave++
		} else {
			missingRec = append(missingRec, t)
		}
	}
	for _, ok := range seenOpt {
		if ok {
			optHave++
		}
	}
	sort.Strings(missingRec)

	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s %s   %s %s   %s %s   %s %s\n",
		pal.bold("Agents"), ratio(installed, len(agents)),
		pal.bold("core"), ratio(coreHave, len(agent.CoreTools)),
		pal.bold("recommended"), ratio(recHave, len(seenRec)),
		pal.bold("stack-specific"), ratio(optHave, len(seenOpt)))

	if usable == 0 && len(overrides) == 0 {
		msg := "No agent CLI found: install one to run reviews."
		if installed > 0 {
			msg = "No auto-detectable agent CLI found: install one, or name an opt-in agent with --agents."
		}
		fmt.Fprintln(out, pal.red(msg))
		return 1
	}
	if len(missingRec) > 0 {
		fmt.Fprintln(out, pal.dim("Worth installing: ")+wrapIndent(strings.Join(missingRec, " "), width, 2))
	}
	fmt.Fprintln(out, pal.dim("Stack-specific tools only matter for the languages you review."))
	return 0
}

// binRunnable reports whether an explicit --bin path names a file that could
// be executed: present, regular, and carrying an execute bit.
func binRunnable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}
