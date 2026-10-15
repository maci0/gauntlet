// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/fuzzy"
	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/report"
	"github.com/maci0/gauntlet/internal/runner"
)

// doctorAt is doctor with a JSON document beside it. The text printer stays
// as it is. JSON probes again and keeps the same exit: 1 when no agent can
// be launched and no --bin override is runnable, or the state root cannot be
// written. A history that cannot be read is a null field and does not change
// the exit.
func doctorAt(out io.Writer, pal report.Palette, overrides map[string]string, width int, asJSON bool) int {
	if !asJSON {
		return doctor(out, pal, overrides, width)
	}
	return writeDoctorJSON(out, overrides)
}

type doctorAgent struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Usable    bool   `json:"usable"`
	Note      string `json:"note,omitempty"`
}

type doctorCore struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Purpose   string `json:"purpose"`
}

type doctorGit struct {
	Version    string `json:"version"`
	BelowFloor bool   `json:"below_floor"`
}

type doctorSandbox struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type doctorState struct {
	Path    string `json:"path"`
	Source  string `json:"source"`
	Problem string `json:"problem,omitempty"`
}

type doctorHistory struct {
	Journals  int `json:"journals"`
	Rows      int `json:"rows"`
	Disagreed int `json:"disagreed"`
	Pruned    int `json:"pruned"`
	Truncated int `json:"truncated"`
}

type doctorDoc struct {
	Agents             []doctorAgent  `json:"agents"`
	Core               []doctorCore   `json:"core"`
	Git                *doctorGit     `json:"git"`
	Sandbox            doctorSandbox  `json:"sandbox"`
	RecommendedMissing []string       `json:"recommended_missing"`
	State              doctorState    `json:"state"`
	History            *doctorHistory `json:"history"`
	Unlanded           []string       `json:"unlanded"`
	OK                 bool           `json:"ok"`
}

func writeDoctorJSON(out io.Writer, overrides map[string]string) int {
	doc, code := doctorDocument(overrides)
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write doctor report: %v\n", err)
		noteExit("failed", "cannot write doctor report", "")
		return exitFail
	}
	return code
}

// doctorDocument repeats the probes doctor prints, without the text layout.
// The usable-agent rule and the state-root rule are the same ones the text
// path returns on.
func doctorDocument(overrides map[string]string) (doctorDoc, int) {
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

	doc := doctorDoc{
		Agents:             []doctorAgent{},
		Core:               []doctorCore{},
		RecommendedMissing: []string{},
		Unlanded:           []string{},
	}
	usable := 0
	pinned := false
	for _, a := range agent.AllNames() {
		row := doctorAgentRow(a, found, overrides)
		if row.Usable {
			usable++
		}
		if path := overrides[a]; path != "" && binRunnable(path) {
			pinned = true
		}
		doc.Agents = append(doc.Agents, row)
	}
	for _, c := range agent.CoreTools {
		doc.Core = append(doc.Core, doctorCore{Name: c.Name, Installed: have(c.Name), Purpose: c.Purpose})
	}
	if have("git") {
		if line := gitx.Version(context.Background()); line != "" {
			below, known := gitx.BelowFloor(line)
			doc.Git = &doctorGit{Version: line, BelowFloor: known && below}
		}
	}
	if detail, ok, err := runner.SandboxSupport(); err != nil {
		doc.Sandbox = doctorSandbox{OK: false, Detail: err.Error()}
	} else {
		doc.Sandbox = doctorSandbox{OK: ok, Detail: detail}
	}
	doc.RecommendedMissing = missingRecommended(have)

	root, homeOK := gauntlethome.Dir()
	src := "from $HOME"
	switch {
	case !homeOK:
		src = "no usable HOME: GAUNTLET_HOME unset and HOME missing, so beside the working directory"
	case strings.TrimSpace(os.Getenv("GAUNTLET_HOME")) != "":
		src = "from GAUNTLET_HOME"
	}
	problem := stateRootProblem(root)
	doc.State = doctorState{Path: normalize.RedactHome(root), Source: src, Problem: problem}

	if st, err := journal.Inspect(); err != nil {
		doc.History = nil
	} else {
		doc.History = &doctorHistory{
			Journals: st.Journals, Rows: st.Rows, Disagreed: st.Disagreed,
			Pruned: st.Pruned, Truncated: st.Truncated,
		}
	}
	if branches := laneBranches(); len(branches) > 0 {
		doc.Unlanded = branches
	}

	code := exitOK
	if (usable == 0 && !pinned) || problem != "" {
		code = exitFail
		noteExit("failed", "no usable agent, or the state root is unusable",
			"Install an agent, or fix the state root named in the report.")
	}
	doc.OK = code == exitOK
	return doc, code
}

func doctorAgentRow(name string, found map[string]string, overrides map[string]string) doctorAgent {
	bin := agent.Binary(name)
	ok := found[name] != "" || found[bin] != ""
	note := ""
	if def, defined := agent.CustomDef(name); defined {
		if !ok && agent.Resolve(bin) != "" {
			ok = true
		}
		extra := "defined"
		if def.Note != "" {
			extra += ": " + def.Note
		}
		note = extra
		if err := agent.BinaryError(name); err != nil {
			note = err.Error()
		}
	}
	if path := overrides[name]; path != "" {
		if binRunnable(path) {
			ok = true
			note = "--bin " + path
		} else {
			ok = false
			note = "--bin " + path + " is not a runnable file"
		}
	} else if agent.IsOptIn(name) && note == "" {
		note = "opt-in: name it with --agents"
	} else if name == "dsh" && !ok && found["bunx"] != "" {
		ok = true
		note = "via bunx (" + agent.DshNpmPackage + "); name it with --agents"
	}
	usable := ok && !agent.IsOptIn(name) && !(name == "dsh" && found["dsh"] == "")
	return doctorAgent{Name: name, Installed: ok, Usable: usable, Note: note}
}

func missingRecommended(have func(string) bool) []string {
	seen := map[string]bool{}
	reviews := make([]string, 0, len(agent.ReviewTools)+len(agent.ReviewsWithoutTools))
	for r := range agent.ReviewTools {
		reviews = append(reviews, r)
	}
	reviews = append(reviews, agent.ReviewsWithoutTools...)
	for _, review := range reviews {
		for _, t := range agent.ReviewTools[review] {
			primary, _, _ := strings.Cut(t, "|")
			if !agent.RecommendedTools[primary] {
				continue
			}
			seen[primary] = have(t) || seen[primary]
		}
	}
	var missing []string
	for name, ok := range seen {
		if !ok {
			missing = append(missing, name)
		}
	}
	fuzzy.Sort(missing)
	if missing == nil {
		missing = []string{}
	}
	return missing
}
