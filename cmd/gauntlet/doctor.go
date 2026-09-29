// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/fuzzy"
	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/journal"
)

// doctor reports which agent CLIs and helper tools are installed. It returns
// the process exit code: 1 when no agent can be launched at all, or when the
// state root cannot be written to.
func doctor(out io.Writer, pal palette, overrides map[string]string, width int) (code int) {
	w := errWriter{out: out}
	defer func() {
		if w.err != nil {
			fmt.Fprintf(os.Stderr, "cannot write doctor report: %v\n", w.err)
			code = exitFail
		}
	}()

	// One parallel probe for every binary, instead of one blocking lookup per
	// question. This is the difference between a snappy doctor and a second of
	// stat calls on a cold cache.
	custom := agent.CustomNames()
	probeNames := append(agent.AllProbeNames(), custom...)
	for _, n := range custom {
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

	w.println(pal.bold("Agent CLIs") +
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
			// An executable the definition names through a variable the
			// operator has not exported expands to nothing, and the launch
			// path refuses it. The row says which variable, rather than
			// showing a missing mark beside a definition that reads as
			// complete.
			if err := agent.BinaryError(a); err != nil {
				note = pal.red("  " + err.Error())
			}
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
		w.printf("  %s %s%s\n", mark(ok, "dim"), a, note)
	}

	w.println()
	w.println(pal.bold("Core tools") + pal.dim("  (used by every review)"))
	coreHave := 0
	for _, c := range agent.CoreTools {
		ok := have(c.Name)
		if ok {
			coreHave++
		}
		label := strings.ReplaceAll(c.Name, "|", " or ")
		w.printf("  %s %-24s %s\n", mark(ok, "yellow"), label, pal.dim(c.Purpose))
	}

	w.println()
	w.println(pal.bold("Per-review helpers") + pal.dim("  (* = worth installing anywhere)"))
	reviews := make([]string, 0, len(agent.ReviewTools)+len(agent.ReviewsWithoutTools))
	for r := range agent.ReviewTools {
		reviews = append(reviews, r)
	}
	reviews = append(reviews, agent.ReviewsWithoutTools...)
	fuzzy.Sort(reviews)
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
			w.printf("  %-*s %s\n", nameCol, review, pal.dim("no external tools"))
			continue
		}
		n := 0
		var cells []string
		for _, t := range tools {
			// An entry with alternatives is present when any of its binaries
			// resolved, and is named by its primary, the way SplitTools
			// hands the same entry to the prompt. Looking the whole entry up
			// would never match: the probe is per binary.
			primary, _, _ := strings.Cut(t, "|")
			ok, rec := have(t), agent.RecommendedTools[primary]
			if ok {
				n++
			}
			if rec {
				seenRec[primary] = ok || seenRec[primary]
			} else {
				seenOpt[primary] = ok || seenOpt[primary]
			}
			star := ""
			if rec {
				star = "*"
			}
			label := primary + star
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
		w.println(head + strings.Join(cells, "  "))
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
	fuzzy.Sort(missingRec)

	w.println()
	w.printf("%s %s   %s %s   %s %s   %s %s\n",
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
	// Which tree this process reads, and where the answer came from, plus
	// where persistent definitions are read from. Neither is shown anywhere
	// else on the screen, so a run that wrote somewhere unexpected cannot be
	// told apart from one that did not. Printed before the verdict, because a
	// box with no agent CLI is exactly the box whose state root is in
	// question. The definitions file is named only when it exists: missing is
	// missing.
	var stateBad bool
	root, homeOK := gauntlethome.Dir()
	src := "from $HOME"
	switch {
	case !homeOK:
		src = "no usable HOME: GAUNTLET_HOME unset and HOME missing, so beside the working directory"
	case strings.TrimSpace(os.Getenv("GAUNTLET_HOME")) != "":
		src = "from GAUNTLET_HOME"
	}
	w.println(pal.dim("State: " + root + "  (" + src + ")"))
	if p := agent.CustomFilePath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			w.println(pal.dim("Definitions: " + p))
		}
	}
	for _, line := range envSettingLines() {
		w.println(pal.dim(line))
	}
	// A root that cannot be written loses the run journal, and a run that
	// cannot journal is only reported by one line of a warning that scrolls
	// past mid-run. Here it is the finding, before the verdict.
	if problem := stateRootProblem(root); problem != "" {
		w.println(pal.red("State root unusable: " + problem))
		stateBad = true
	}
	// What the state tree holds, so a restore is checked rather than assumed:
	// a journal without its index row is a run a listing will reconstruct,
	// and an index row without its journal is a run nothing can read back.
	// Neither changes the verdict, both change what the operator believes they
	// have.
	if st, err := journal.Inspect(); err != nil {
		w.println(pal.yellow("Run history unreadable: " + err.Error()))
	} else if st.Journals > 0 || st.Pruned > 0 {
		line := fmt.Sprintf("Run history: %s", humanize.Plural(st.Journals, "journal", "journals"))
		if st.Disagreed > 0 {
			w.println(pal.yellow(line + fmt.Sprintf(
				", %d not matched by the index (gauntlet runs repairs what it can reconstruct)", st.Disagreed)))
		} else {
			w.println(pal.dim(line + ", index agrees"))
		}
		if st.Pruned > 0 {
			w.println(pal.dim(fmt.Sprintf(
				"  %s still recoverable: gauntlet runs --restore <run-id>",
				humanize.Plural(st.Pruned, "pruned run", "pruned runs"))))
		}
		// A journal cut mid-line is the one loss the counts above cannot show:
		// the half-line is not JSON, so the run replays as a shorter run that
		// looks whole, and a restored archive is checked by its counts.
		if st.Truncated > 0 {
			w.println(pal.yellow(fmt.Sprintf(
				"  %s mid-line: its last events are missing, so it lists as a shorter run",
				humanize.Plural(st.Truncated, "journal ends", "journals end"))))
		}
	}
	if usable == 0 && !pinned {
		msg := "No agent CLI found: install one to run reviews."
		if installed > 0 {
			msg = "No auto-detectable agent CLI found: install one, or name an opt-in agent with --agents."
		}
		w.println(pal.red(msg))
		return exitFail
	}
	if len(missingRec) > 0 {
		w.println(pal.dim("Worth installing: ") + wrapIndent(strings.Join(missingRec, " "), width, 2))
	}
	w.println(pal.dim(tokenSourceLine))
	w.println(pal.dim("Stack-specific tools only matter for the languages you review."))
	if stateBad {
		return exitFail
	}
	return exitOK
}

// envSettingLines reports which documented variables this process actually
// saw, so an operator can tell a knob that is unset from one set to empty
// without reading the source. Presence, not meaning: the value of a variable
// is printed as the operator set it, and nothing here interprets it, because
// the rules differ per variable (NO_COLOR counts however it is set, the
// motion and color-force names count as on above empty, 0, false, no, off,
// and TERM is compared against dumb).
//
// GAUNTLET_HOME is left out: the State line above already names it and where
// it came from. A variable carrying a secret is reported as present and never
// as a value, so a pasted doctor transcript cannot leak one.
func envSettingLines() []string {
	var set []string
	for _, e := range helpEnvVars {
		if e.Name == "GAUNTLET_HOME" {
			continue
		}
		value, present := os.LookupEnv(e.Name)
		switch {
		case !present:
			continue
		case e.Secret:
			set = append(set, e.Name+"=(set)")
		case value == "":
			set = append(set, e.Name+"=(empty)")
		default:
			set = append(set, e.Name+"="+value)
		}
	}
	if len(set) == 0 {
		return []string{"Environment: no documented variable is set"}
	}
	return []string{"Environment: " + strings.Join(set, ", ")}
}

// binRunnable reports whether an explicit --bin path names a file that could
// be executed: present, regular, and carrying an execute bit.
func binRunnable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// stateRootProblem says why the state root cannot hold a run journal, or ""
// when it can. A root that does not exist yet is not a problem: the first
// run creates it, so nothing here is created on doctor's account.
//
// Writability is proved the way the run will find out: a temp file in the
// root, removed again. The root is gauntlet's own directory and every run
// writes there, so the probe touches nothing the operator has not already
// agreed to; asking the kernel instead needs a direct dependency on
// x/sys for one call.
func stateRootProblem(root string) string {
	fi, err := os.Stat(root)
	switch {
	case os.IsNotExist(err):
		return ""
	case err != nil:
		return fmt.Sprintf("%s cannot be read: %v", root, err)
	case !fi.IsDir():
		return root + " is not a directory"
	}
	sweepProbeLeftovers(root)
	f, err := os.CreateTemp(root, stateProbePrefix+"*")
	if err != nil {
		return fmt.Sprintf("%s is not writable: %v", root, err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Sprintf("%s is not writable: %v", root, err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Sprintf("%s holds %s that cannot be removed: %v", root, name, err)
	}
	return ""
}

// sweepProbeLeftovers removes probe files an earlier doctor was killed before
// it could unlink, so a crash does not leave one behind per run. A removal
// that fails is swallowed on purpose: it means the same thing the probe is
// about to report, and the probe's own create-close-remove is what surfaces
// it with the path and the reason. The prefix cannot match anything but a
// probe file: the name is reserved for this, and every other file the tool
// writes carries a different one. The age cutoff keeps a doctor running now
// from unlinking the probe a second one is in the middle of writing.
func sweepProbeLeftovers(root string) {
	gauntlethome.SweepStaleTemps(root, stateProbePrefix, stateProbeMaxAge, nil)
}

// stateProbePrefix names the temp file the writability probe creates, so one
// left behind by an interrupted doctor is recognizable and never mistaken
// for a run journal.
const stateProbePrefix = ".gauntlet-doctor-"

// stateProbeMaxAge is how old a probe file has to be before the sweep takes it.
// A probe lives for the length of one create, write, close, and remove, so any
// file older than this belongs to a doctor that did not get that far.
const stateProbeMaxAge = time.Minute
