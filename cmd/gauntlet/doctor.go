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
func doctor(out io.Writer, pal palette, overrides map[string]string, width int) (code int) {
	var werr error
	defer func() {
		if werr != nil {
			fmt.Fprintf(os.Stderr, "cannot write doctor report: %v\n", werr)
			code = exitFail
		}
	}()
	write := func(format string, args ...any) {
		if werr == nil {
			_, werr = fmt.Fprintf(out, format, args...)
		}
	}
	writeln := func(args ...any) {
		if werr == nil {
			_, werr = fmt.Fprintln(out, args...)
		}
	}

	// One parallel probe for every binary, instead of one blocking lookup per
	// question. This is the difference between a snappy doctor and a second of
	// stat calls on a cold cache.
	probeNames := append(agent.AllProbeNames(), agent.CustomNames()...)
	for _, n := range agent.CustomNames() {
		probeNames = append(probeNames, agent.Binary(n))
	}
	found := agent.ResolveMany(probeNames)
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

	writeln(pal.bold("Agent CLIs") +
		pal.dim("  (✓ installed, ✗ missing; at least one required)"))
	installed, usable := 0, 0
	for _, a := range agents {
		bin := agent.Binary(a)
		ok := found[a] != "" || found[bin] != ""
		note := ""
		if def, defined := agent.CustomDef(a); defined {
			// A definition names its own binary, which may not have been in
			// the probe list.
			if !ok && agent.Resolve(bin) != "" {
				ok = true
			}
			extra := "defined"
			if def.Note != "" {
				extra += ": " + def.Note
			}
			note = pal.dim("  " + extra)
		}
		if path := overrides[a]; path != "" {
			// An override names the executable directly, so it wins over what
			// PATH has: cmd[0] becomes exactly this file, which is what a run
			// would exec. "Usable" means it exists and could be executed; a
			// broken override must not make doctor report a working setup
			// (or exit 0) on an empty box.
			if binRunnable(path) {
				ok = true
				note = pal.dim("  --bin " + path)
			} else {
				ok = false
				note = pal.red("  --bin " + path + " is not a runnable file")
			}
		} else if agent.IsOptIn(a) && note == "" {
			note = pal.dim("  opt-in: name it with --agents")
		} else if a == "dsh" && !ok && found["bunx"] != "" {
			ok = true // launchable, but only when named: bunx fetches on first use
			note = pal.dim("  via bunx (@deepseek-ai/dsh); name it with --agents")
		}
		if ok {
			installed++
			if !agent.IsOptIn(a) && !(a == "dsh" && found["dsh"] == "") {
				usable++
			}
		}
		write("  %s %s%s\n", mark(ok, "dim"), a, note)
	}

	writeln()
	writeln(pal.bold("Core tools") + pal.dim("  (used by every review)"))
	coreHave := 0
	for _, c := range agent.CoreTools {
		ok := have(c.Name)
		if ok {
			coreHave++
		}
		label := strings.ReplaceAll(c.Name, "|", " or ")
		write("  %s %-24s %s\n", mark(ok, "yellow"), label, pal.dim(c.Purpose))
	}

	writeln()
	writeln(pal.bold("Per-review helpers") + pal.dim("  (* = worth installing anywhere)"))
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
			write("  %-*s %s\n", nameCol, review, pal.dim("no external tools"))
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
		writeln(head + strings.Join(cells, "  "))
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

	writeln()
	write("%s %s   %s %s   %s %s   %s %s\n",
		pal.bold("Agents"), ratio(installed, len(agents)),
		pal.bold("Core"), ratio(coreHave, len(agent.CoreTools)),
		pal.bold("Recommended"), ratio(recHave, len(seenRec)),
		pal.bold("Stack-specific"), ratio(optHave, len(seenOpt)))

	// An explicit --bin is the user vouching for one exact file, so a
	// runnable override counts as an agent even when nothing auto-detects.
	// A broken override vouches for nothing: with no working agent anywhere,
	// this is the empty box again and gets its answer.
	pinned := false
	for _, path := range overrides {
		if binRunnable(path) {
			pinned = true
			break
		}
	}
	if usable == 0 && !pinned {
		msg := "No agent CLI found: install one to run reviews."
		if installed > 0 {
			msg = "No auto-detectable agent CLI found: install one, or name an opt-in agent with --agents."
		}
		writeln(pal.red(msg))
		return exitFail
	}
	if len(missingRec) > 0 {
		writeln(pal.dim("Worth installing: ") + wrapIndent(strings.Join(missingRec, " "), width, 2))
	}
	// Where persistent definitions were read from, so a definition that
	// misbehaves can be traced to its file (and to any GAUNTLET_HOME in
	// play). Named only when the file exists: missing is missing.
	if p := agent.CustomFilePath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			writeln(pal.dim("Definitions: " + p))
		}
	}
	writeln(pal.dim(tokenSourceLine))
	writeln(pal.dim("Stack-specific tools only matter for the languages you review."))
	return exitOK
}

// binRunnable reports whether an explicit --bin path names a file that could
// be executed: present, regular, and carrying an execute bit.
func binRunnable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}
