// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Custom describes an agent gauntlet was not compiled to know about: the argv
// that runs one prompt headlessly, and the flags that change its behavior.
//
// This exists because the set of agent CLIs is not closed. Frameworks like pi
// produce whole families of them, in-house wrappers exist, and an agent's
// flags change between releases. Rather than guess at an invocation and ship a
// definition that silently breaks, gauntlet lets the invocation be stated
// where it can be corrected in one line.
type Custom struct {
	// Argv runs one prompt. It must contain the {prompt} placeholder, which is
	// replaced with the composed review, as a single argument. {model} is
	// replaced when a model is pinned. No shell is involved: these are exec
	// arguments, so quoting and word splitting do not apply.
	Argv []string `json:"argv"`

	// Model, when set, is appended (with {model} expanded) instead of
	// requiring a {model} placeholder inside Argv.
	Model []string `json:"model,omitempty"`

	// Stream flags ask for machine-readable output, if the agent has such a
	// mode. Inserted before the prompt, like the built-in agents' flags.
	Stream []string `json:"stream,omitempty"`

	// Continue flags resume the agent's last session in this directory.
	Continue []string `json:"continue,omitempty"`

	// OptIn keeps the agent out of auto-detection and "mixed": it runs only
	// when named with --agents. Definitions whose invocation has not been
	// verified against the real CLI should set this.
	OptIn bool `json:"opt_in,omitempty"`

	// Usage describes where this agent keeps its session transcripts, so live
	// token counts work for it. Roots may use ~; records are parsed
	// generically, so any JSONL carrying recognizable counters works.
	Usage *UsageSpec `json:"usage,omitempty"`

	// Note is shown by doctor, for definitions that need explaining.
	Note string `json:"note,omitempty"`
}

// UsageSpec locates a defined agent's transcripts. It mirrors the reader's
// own spec, which the CLI hands it to; keeping it here means the whole
// definition lives in one JSON object.
type UsageSpec struct {
	Roots      []string `json:"roots"`
	Suffix     string   `json:"suffix,omitempty"`
	Cumulative bool     `json:"cumulative,omitempty"`
	// HeaderCwd says the working directory appears once at the top of a
	// session file rather than on every record.
	HeaderCwd bool `json:"header_cwd,omitempty"`
}

const (
	promptPlaceholder = "{prompt}"
	modelPlaceholder  = "{model}"
)

// Validate reports whether a definition can actually launch something.
func (c Custom) Validate(name string) error {
	if name == "" {
		return fmt.Errorf("custom agent needs a name")
	}
	if strings.ContainsAny(name, " \t,:=") {
		return fmt.Errorf("invalid agent name %q: no spaces, commas, colons, or equals signs", name)
	}
	if len(c.Argv) == 0 {
		return fmt.Errorf("custom agent %q has no argv", name)
	}
	for _, a := range c.Argv {
		if strings.Contains(a, promptPlaceholder) {
			return nil
		}
	}
	return fmt.Errorf("custom agent %q: argv must contain %s", name, promptPlaceholder)
}

// builtinCustom are agents gauntlet ships a definition for rather than code.
//
// They are the pi family: one framework (github.com/earendil-works/pi) and the
// CLIs built on it, which share its flags and its transcript layout. Defining
// them instead of compiling them in is the point: a family grows faster than a
// release cycle, and every entry here can be corrected or replaced from
// ~/.gauntlet/agents.json without a new binary.
//
// Flags marked verified were read from that CLI's own --help on a machine
// where it is installed. Unverified entries are OptIn, so they never run
// unless named.
var builtinCustom = map[string]Custom{
	// pi: verified against pi 0.84.3 (@earendil-works/pi-coding-agent).
	// Non-interactive modes skip the trust prompt and fall back to the
	// defaultProjectTrust setting, so a review that must edit files needs
	// "defaultProjectTrust": "always" in ~/.pi/agent/settings.json.
	"pi": {
		Argv:     []string{"pi", "-p", promptPlaceholder},
		Model:    []string{"--model", modelPlaceholder},
		Stream:   []string{"--mode", "json"},
		Continue: []string{"-c"},
		Usage:    &UsageSpec{Roots: []string{"~/.pi/agent/sessions"}},
		Note:     "needs defaultProjectTrust=always in ~/.pi/agent/settings.json to edit files headlessly",
	},

	// prime-agent: verified against its --help. A pi fork, so the flags match,
	// and its sessions live under its own home.
	"prime-agent": {
		Argv:     []string{"prime-agent", "-p", promptPlaceholder},
		Model:    []string{"--model", modelPlaceholder},
		Stream:   []string{"--mode", "json"},
		Continue: []string{"-c"},
		Usage:    &UsageSpec{Roots: []string{"~/.prime/agent/sessions"}},
	},

	// feynman: verified against its --help. Built on pi but with its own
	// front end: the one-shot flag is --prompt, and it has no json mode.
	"feynman": {
		Argv:  []string{"feynman", "--prompt", promptPlaceholder},
		Model: []string{"--model", modelPlaceholder},
		Usage: &UsageSpec{Roots: []string{"~/.feynman/sessions"}},
	},

	// omp (oh-my-pi): a pi fork, so the flags below follow pi's, but the
	// installed copy could not be run to confirm them, and its session store
	// was not found. Opt-in until someone verifies it.
	"omp": {
		Argv:     []string{"omp", "-p", promptPlaceholder},
		Model:    []string{"--model", modelPlaceholder},
		Stream:   []string{"--mode", "json"},
		Continue: []string{"-c"},
		OptIn:    true,
		Note:     "invocation follows pi's and is unverified; override with --agent-cmd",
	},
}

var (
	customMu sync.RWMutex
	custom   = func() map[string]Custom {
		m := make(map[string]Custom, len(builtinCustom))
		maps.Copy(m, builtinCustom)
		return m
	}()
)

// Register adds or replaces a custom agent definition.
func Register(name string, def Custom) error {
	if err := def.Validate(name); err != nil {
		return err
	}
	if isBuiltinTool(name) {
		return fmt.Errorf("%q is a built-in agent and cannot be redefined", name)
	}
	customMu.Lock()
	custom[name] = def
	customMu.Unlock()
	return nil
}

// CustomDef returns the definition for a custom agent.
func CustomDef(name string) (Custom, bool) {
	customMu.RLock()
	defer customMu.RUnlock()
	d, ok := custom[name]
	return d, ok
}

// CustomNames lists the defined custom agents, in name order.
func CustomNames() []string {
	customMu.RLock()
	defer customMu.RUnlock()
	out := make([]string, 0, len(custom))
	for n := range custom {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// isBuiltinTool reports whether a name is compiled in, as opposed to defined.
func isBuiltinTool(name string) bool {
	return slices.Contains(Valid, name)
}

// ParseAgentCmd parses a NAME=ARGV... definition from the command line, where
// ARGV is space separated and must contain {prompt}:
//
//	--agent-cmd pi='pi --agent reviewer -p {prompt}'
//
// The value is split on spaces, not by a shell: gauntlet execs the agent
// directly, so there is nothing to quote and nothing to inject into.
func ParseAgentCmd(s string) (string, Custom, error) {
	name, rest, ok := strings.Cut(s, "=")
	name = strings.TrimSpace(name)
	if !ok || name == "" || strings.TrimSpace(rest) == "" {
		return "", Custom{}, fmt.Errorf("expected NAME=ARGV, got %q", s)
	}
	def := Custom{Argv: strings.Fields(rest)}
	if err := def.Validate(name); err != nil {
		return "", Custom{}, err
	}
	return name, def, nil
}

// LoadCustomFile reads agent definitions from a JSON file, if it exists. The
// file maps a name to a definition:
//
//	{"pi": {"argv": ["pi","-p","{prompt}"], "stream": ["--json"]}}
//
// A missing file is not an error; a malformed one is, because silently running
// with the wrong agent set is worse than refusing to start.
func LoadCustomFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var defs map[string]Custom
	if err := json.Unmarshal(data, &defs); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for name, def := range defs {
		if err := Register(name, def); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

// CustomFilePath is where agent definitions live by default.
func CustomFilePath() string {
	if h := os.Getenv("GAUNTLET_HOME"); h != "" {
		return filepath.Join(h, "agents.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gauntlet", "agents.json")
}

// buildCustom expands a custom definition into an argv.
func buildCustom(def Custom, spec Spec, prompt string, opts BuildOpts) []string {
	expand := func(in []string) []string {
		out := make([]string, 0, len(in))
		for _, a := range in {
			switch {
			case a == promptPlaceholder:
				out = append(out, prompt)
			case strings.Contains(a, promptPlaceholder):
				out = append(out, strings.ReplaceAll(a, promptPlaceholder, prompt))
			case strings.Contains(a, modelPlaceholder):
				out = append(out, strings.ReplaceAll(a, modelPlaceholder, spec.Model))
			default:
				out = append(out, a)
			}
		}
		return out
	}

	// Flags go before the prompt, which agents expect to come last.
	head := def.Argv
	promptAt := len(head)
	for i, a := range head {
		if strings.Contains(a, promptPlaceholder) {
			promptAt = i
			break
		}
	}
	var argv []string
	argv = append(argv, head[:promptAt]...)
	if opts.Stream {
		argv = append(argv, def.Stream...)
	}
	if opts.Continue {
		argv = append(argv, def.Continue...)
	}
	if spec.Model != "" && len(def.Model) > 0 {
		argv = append(argv, def.Model...)
	}
	argv = append(argv, head[promptAt:]...)

	argv = expand(argv)
	if opts.Binary != "" {
		argv[0] = opts.Binary
	}
	return argv
}
