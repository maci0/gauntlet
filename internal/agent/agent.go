// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agent knows which AI coding CLIs exist, where they are, and how to
// invoke them headlessly with a prompt.
package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/maci0/gauntlet/internal/fuzzy"
	"github.com/maci0/gauntlet/internal/gauntlethome"
)

// Spec is one agent, optionally pinned to a model. The zero value is invalid.
type Spec struct {
	Tool  string
	Model string // empty means the CLI picks its own default
}

// Label is the display and map-key form of a spec.
func (s Spec) Label() string {
	if s.Model == "" {
		return s.Tool
	}
	return s.Tool + ":" + s.Model
}

// Valid lists every supported agent CLI.
var Valid = []string{
	"claude", "gemini", "qwen", "codex", "grok", "agy", "cursor-agent",
	"kimi", "opencode", "crush", "clanker", "dsh",
}

// noModel agents pick their model from their own config, not the command line.
var noModel = map[string]bool{"agy": true, "clanker": true}

// optIn agents are usable, but only under conditions the runner cannot check,
// so auto-detection and "mixed" skip them. clanker loads its config from the
// working directory, so it can only review the repository holding that config.
var optIn = map[string]bool{"clanker": true}

// subcommand agents are invoked as "binary subcommand ...": session flags
// belong after the subcommand, not next to the binary.
var subcommand = map[string]string{"codex": "exec", "opencode": "run", "clanker": "run", "crush": "run"}

// continueFlags resume the agent's most recent session in this directory.
// Agents absent here always start fresh: their resume mechanics do not compose
// with one-shot prompt mode.
var continueFlags = map[string][]string{
	"claude":   {"-c"},
	"qwen":     {"-c"},
	"agy":      {"-c"},
	"kimi":     {"-c"},
	"grok":     {"-c"},
	"opencode": {"-c"},
	"crush":    {"-C"},
	"gemini":   {"--resume", "latest"},
}

// streamFlags turn on an agent's machine-readable output. Every entry was read
// from that CLI's own --help: gauntlet never guesses a flag, because an
// unknown flag makes the agent exit instead of run.
//
// The stream carries token usage and the reasoning/output split, which text
// mode hides. Agents absent here have no such mode, and keep printing prose.
var streamFlags = map[string][]string{
	"claude":       {"--output-format", "stream-json", "--verbose"},
	"gemini":       {"--output-format", "stream-json"},
	"qwen":         {"--output-format", "stream-json"},
	"kimi":         {"--output-format", "stream-json"},
	"cursor-agent": {"--output-format", "stream-json"},
	"grok":         {"--output-format", "streaming-messages-json"},
	// clanker gained --stream for exactly this: one JSON usage line per model
	// response, printed alongside its ordinary prose.
	"clanker": {"--stream"},
}

// isValid reports whether name is a supported agent CLI, built in or defined.
func isValid(name string) bool {
	if isBuiltinTool(name) {
		return true
	}
	_, ok := CustomDef(name)
	return ok
}

// AllNames lists every usable agent name, built in and defined, in order.
func AllNames() []string {
	out := append([]string(nil), Valid...)
	out = append(out, CustomNames()...)
	sort.Strings(out)
	return out
}

// takesModel reports whether the agent accepts a model on the command line.
func takesModel(tool string) bool {
	if d, ok := CustomDef(tool); ok {
		return len(d.Model) > 0 || containsPlaceholder(d.Argv, modelPlaceholder)
	}
	return !noModel[tool]
}

func containsPlaceholder(argv []string, want string) bool {
	for _, a := range argv {
		if strings.Contains(a, want) {
			return true
		}
	}
	return false
}

// IsOptIn reports whether the agent must be named explicitly to be used.
func IsOptIn(tool string) bool {
	if optIn[tool] {
		return true
	}
	if d, ok := CustomDef(tool); ok {
		return d.OptIn
	}
	return false
}

// pathNoCWD returns PATH entries that cannot change meaning after a chdir.
// Empty slots and "." mean cwd, and relative slots mean $CWD/<slot>: after a
// cd into the review target, either one would pick up a planted executable
// with the same name as an agent.
func pathNoCWD() string {
	raw := os.Getenv("PATH")
	if raw == "" {
		raw = "/usr/local/bin:/usr/bin:/bin"
	}
	keep := make([]string, 0, 16)
	for _, p := range filepath.SplitList(raw) {
		if p != "" && filepath.IsAbs(p) {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

// The cache is keyed by the PATH it was filled from. A memo that outlives its
// input answers for a machine that no longer exists: a process that changes
// PATH (a test harness, a wrapper that adds a directory before launching)
// would keep being told an agent is missing because it was missing before.
var (
	resolveMu    sync.RWMutex
	resolvePath  string
	resolveCache = map[string]string{}
)

// resolveLookup answers from the cache, refilling it when PATH has changed
// since it was built.
func resolveLookup(name, path string) (string, bool) {
	resolveMu.RLock()
	defer resolveMu.RUnlock()
	if resolvePath != path {
		return "", false
	}
	got, ok := resolveCache[name]
	return got, ok
}

func resolveStore(name, path, found string) {
	resolveMu.Lock()
	defer resolveMu.Unlock()
	if resolvePath != path {
		resolvePath, resolveCache = path, map[string]string{}
	}
	resolveCache[name] = found
}

// Resolve returns the absolute path of an executable found on a
// cwd-independent PATH, or "" when it is not installed. Results are memoized:
// doctor and auto-detection probe the same names repeatedly.
func Resolve(name string) string {
	path := pathNoCWD()
	if got, ok := resolveLookup(name, path); ok {
		return got
	}
	found := lookIn(name, path)
	resolveStore(name, path, found)
	return found
}

// ResolveMany probes names concurrently and returns the installed subset as a
// name -> absolute path map. One pool of goroutines beats a serial stat walk
// over the ~90 binaries doctor asks about.
func ResolveMany(names []string) map[string]string {
	out := make(map[string]string, len(names))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for _, n := range names {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if p := Resolve(n); p != "" {
				mu.Lock()
				out[n] = p
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return out
}

// lookIn is exec.LookPath restricted to an explicit PATH.
func lookIn(name, path string) string {
	if strings.ContainsRune(name, os.PathSeparator) {
		if err := executable(name); err == nil {
			abs, _ := filepath.Abs(name)
			return abs
		}
		return ""
	}
	for _, dir := range filepath.SplitList(path) {
		p := filepath.Join(dir, name)
		if err := executable(p); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	return ""
}

func executable(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	if fi.IsDir() || fi.Mode()&0o111 == 0 {
		return os.ErrPermission
	}
	return nil
}

// Installed lists agents eligible for auto-detection and "mixed", in name
// order. Discovery is PATH-based: an agent whose binary is not on PATH under
// its own name must be named explicitly (see --bin).
func Installed() []Spec {
	names := make([]string, 0, len(Valid))
	for _, t := range AllNames() {
		if !IsOptIn(t) {
			names = append(names, t)
		}
	}
	found := ResolveMany(names)
	specs := make([]Spec, 0, len(found))
	for _, t := range names {
		if found[t] != "" {
			specs = append(specs, Spec{Tool: t})
		}
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Tool < specs[j].Tool })
	return specs
}

var mixedKeywords = map[string]bool{"mixed": true, "random": true, "all": true}

var dshModelRe = regexp.MustCompile(`^[A-Za-z0-9._/:-]+$`)

// ParseSpecs parses a comma-separated agent list ("claude", "mixed",
// "claude:opus,codex:gpt-5-codex"), preserving order and dropping duplicates.
func ParseSpecs(s string) ([]Spec, error) {
	var specs []Spec
	seen := map[Spec]bool{}
	add := func(sp Spec) {
		if !seen[sp] {
			seen[sp] = true
			specs = append(specs, sp)
		}
	}
	for raw := range strings.SplitSeq(s, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if mixedKeywords[strings.ToLower(entry)] {
			inst := Installed()
			if len(inst) == 0 {
				return nil, fmt.Errorf("%q matched no installed tools (supported: %s)",
					entry, strings.Join(sortedValid(), ", "))
			}
			for _, sp := range inst {
				add(sp)
			}
			continue
		}
		tool, model, _ := strings.Cut(entry, ":")
		tool = strings.ToLower(strings.TrimSpace(tool))
		model = strings.TrimSpace(model)
		if !isValid(tool) {
			hint := ""
			if c := fuzzy.Closest(tool, AllNames()); c != "" {
				hint = fmt.Sprintf(" (did you mean %q?)", c)
			}
			return nil, fmt.Errorf("unknown tool: %q%s (valid: %s, or mixed for all)",
				tool, hint, strings.Join(AllNames(), ", "))
		}
		if !takesModel(tool) && model != "" {
			return nil, fmt.Errorf("%s does not support specifying a model: %q", tool, entry)
		}
		// The dsh model is spliced into a generated YAML overlay; keep the
		// charset too narrow to escape the quoted scalar.
		if tool == "dsh" && model != "" && !dshModelRe.MatchString(model) {
			return nil, fmt.Errorf("invalid dsh model name: %q (letters, digits, . _ / : -)", model)
		}
		add(Spec{Tool: tool, Model: model})
	}
	if len(specs) == 0 {
		return nil, errors.New("no agents specified")
	}
	return specs, nil
}

func sortedValid() []string {
	out := append([]string(nil), Valid...)
	sort.Strings(out)
	return out
}

// BuildOpts tunes one command line.
type BuildOpts struct {
	Continue bool   // resume this agent's most recent session in this directory
	Binary   string // --bin override: same argv, different executable
	// Stream asks for machine-readable output where the agent has it. Agents
	// without one are unaffected, so a caller can set this unconditionally.
	Stream bool
}

// maxPromptArg bounds one exec argument. A composed prompt travels as a
// single argv element, and Linux refuses the whole exec with E2BIG once one
// argument passes MAX_ARG_STRLEN (32 pages, about 128 KiB). Prompt files are
// accepted well past that, so without this check an oversized review would
// fail at launch on every agent instead of failing here, once, by name.
// The margin below MAX_ARG_STRLEN leaves room for the flags around it.
const maxPromptArg = 120 << 10

// BuildCmd returns the argv that runs one prompt headlessly through spec.
func BuildCmd(spec Spec, prompt string, opts BuildOpts) ([]string, error) {
	var (
		cmd []string
		err error
	)
	if def, ok := CustomDef(spec.Tool); ok {
		cmd = buildCustom(def, spec, prompt, opts)
	} else if cmd, err = buildBuiltin(spec, prompt, opts); err != nil {
		return nil, err
	}
	if opts.Stream {
		if flags, ok := streamFlags[spec.Tool]; ok {
			cmd = splice(cmd, flagInsertAt(spec.Tool), flags)
		}
	}
	if opts.Continue {
		if flags, ok := continueFlags[spec.Tool]; ok {
			cmd = splice(cmd, flagInsertAt(spec.Tool), flags)
		}
	}
	if opts.Binary != "" {
		cmd[0] = opts.Binary
	}
	for _, a := range cmd {
		if len(a) > maxPromptArg {
			return nil, fmt.Errorf("an argument is %d bytes, over the %d-byte "+
				"single-argument exec limit: the composed prompt cannot be dispatched, "+
				"shorten or split it", len(a), maxPromptArg)
		}
	}
	return cmd, nil
}

func buildBuiltin(spec Spec, prompt string, opts BuildOpts) ([]string, error) {
	var cmd []string
	switch spec.Tool {
	case "claude":
		cmd = []string{"claude", "--dangerously-skip-permissions"}
		if spec.Model != "" {
			cmd = append(cmd, "--model", spec.Model)
		}
		cmd = append(cmd, "-p", prompt)
	case "gemini", "qwen":
		cmd = []string{spec.Tool, "-y"}
		if spec.Model != "" {
			cmd = append(cmd, "-m", spec.Model)
		}
		cmd = append(cmd, "-p", prompt)
	case "codex":
		cmd = []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox"}
		if spec.Model != "" {
			cmd = append(cmd, "-m", spec.Model)
		}
		cmd = append(cmd, prompt)
	case "grok":
		cmd = []string{"grok", "--permission-mode", "bypassPermissions"}
		if spec.Model != "" {
			cmd = append(cmd, "-m", spec.Model)
		}
		cmd = append(cmd, "-p", prompt)
	case "agy":
		cmd = []string{"agy", "--dangerously-skip-permissions", "-p", prompt}
	case "cursor-agent":
		cmd = []string{"cursor-agent", "--print", "-f"}
		if spec.Model != "" {
			cmd = append(cmd, "--model", spec.Model)
		}
		cmd = append(cmd, prompt)
	case "dsh":
		// Permissions come from the headless profile's config; dsh:model pins
		// the model via a generated --patch overlay. When the launcher is not
		// on PATH, fall back to bunx, which fetches @deepseek-ai/dsh on first
		// use; that network fetch is why auto-detection ignores the fallback.
		base := []string{"dsh"}
		if opts.Binary == "" && Resolve("dsh") == "" {
			bunx := Resolve("bunx")
			if bunx == "" {
				bunx = "bunx"
			}
			base = []string{bunx, "@deepseek-ai/dsh"}
		}
		cmd = append(append([]string{}, base...), "--profile", "headless")
		if opts.Stream {
			// dsh has no stdout event mode; what it has is a session log, and
			// this overlay is what makes that log readable (see dsh.go).
			patch, err := dshPlainSessionsPatch()
			if err != nil {
				return nil, err
			}
			cmd = append(cmd, "--patch", patch)
		}
		if spec.Model != "" {
			provider, model := splitProvider(spec.Model)
			if provider == "" {
				// One probe keeps the provider and its failure together:
				// re-invoking to recover a dropped error is only safe while
				// the memo holds, and pairs two outcomes with one condition.
				p, perr := dshDefaultProvider(base)
				if perr != nil {
					return nil, fmt.Errorf("cannot determine the dsh provider for model %q: %w; "+
						"use dsh:<provider>/<model>", spec.Model, perr)
				}
				provider = p
			}
			if provider == "" {
				return nil, fmt.Errorf("cannot determine the dsh provider for model %q; use dsh:<provider>/<model>", spec.Model)
			}
			patch, err := dshModelPatch(provider, model)
			if err != nil {
				return nil, err
			}
			cmd = append(cmd, "--patch", patch)
		}
		cmd = append(cmd, prompt)
	case "clanker":
		// Model and permissions come from clanker's own config; resuming needs
		// an explicit session id, so it stays out of continueFlags.
		cmd = []string{"clanker", "run", prompt}
	case "opencode":
		cmd = []string{"opencode", "run", "--auto"}
		if spec.Model != "" {
			cmd = append(cmd, "-m", spec.Model)
		}
		cmd = append(cmd, prompt)
	case "crush":
		// `crush run` is the non-interactive mode, and it auto-approves the
		// session's tool permissions itself (app.RunNonInteractive calls
		// AutoApproveSession); the interactive --yolo flag is not accepted
		// here and would make it exit instead of run. --quiet drops the
		// spinner, whose repaints are noise in a captured stream.
		cmd = []string{"crush", "run", "--quiet"}
		if spec.Model != "" {
			cmd = append(cmd, "-m", spec.Model)
		}
		cmd = append(cmd, prompt)
	case "kimi":
		// -p refuses --auto/--yolo; prompt mode is already non-interactive and
		// auto-approves tool calls.
		cmd = []string{"kimi"}
		if spec.Model != "" {
			cmd = append(cmd, "-m", spec.Model)
		}
		cmd = append(cmd, "-p", prompt)
	default:
		return nil, fmt.Errorf("unknown tool: %s", spec.Tool)
	}
	return cmd, nil
}

// flagInsertAt is where output and session flags go: directly after the
// executable, or after its subcommand when the CLI takes one.
func flagInsertAt(tool string) int {
	if _, sub := subcommand[tool]; sub {
		return 2
	}
	return 1
}

// splice inserts flags after the first at elements of cmd, returning a fresh
// slice so the caller's argv is never aliased or overwritten in place.
func splice(cmd []string, at int, flags []string) []string {
	out := make([]string, 0, len(cmd)+len(flags))
	out = append(out, cmd[:at]...)
	out = append(out, flags...)
	return append(out, cmd[at:]...)
}

func splitProvider(model string) (provider, name string) {
	provider, name, ok := strings.CutLast(model, "/")
	if !ok {
		return "", model
	}
	return provider, name
}

// ParseBin parses a TOOL=PATH override into (tool, absolute executable).
func ParseBin(s string) (string, string, error) {
	tool, path, ok := strings.Cut(s, "=")
	tool, path = strings.TrimSpace(tool), strings.TrimSpace(path)
	if !ok || tool == "" || path == "" {
		return "", "", fmt.Errorf("expected TOOL=PATH, got: %q", s)
	}
	tool = strings.ToLower(tool)
	if !isValid(tool) {
		hint := ""
		if c := fuzzy.Closest(tool, AllNames()); c != "" {
			hint = fmt.Sprintf(" (did you mean %q?)", c)
		}
		return "", "", fmt.Errorf("unknown agent: %q%s (valid: %s)", tool, hint,
			strings.Join(AllNames(), ", "))
	}
	expanded := gauntlethome.ExpandPath(path)
	// A path with a directory component is used as is; a bare name searches
	// PATH without cwd-relative entries. Either way the result is absolute
	// before any chdir, so a relative --bin cannot retarget into the tree.
	var resolved string
	if strings.ContainsRune(expanded, os.PathSeparator) {
		if p, err := exec.LookPath(expanded); err == nil {
			resolved, _ = filepath.Abs(p)
		}
	} else {
		resolved = Resolve(expanded)
	}
	if resolved == "" {
		extra := ""
		if expanded != path {
			extra = fmt.Sprintf(" (expanded to %s)", expanded)
		}
		return "", "", fmt.Errorf("not an executable: %s%s", path, extra)
	}
	return tool, resolved, nil
}
