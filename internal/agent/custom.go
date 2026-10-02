// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/maci0/gauntlet/internal/fuzzy"
	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/safefile"
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

	// Effort, when set, is appended (with {effort} expanded) when a spec pins
	// a reasoning effort, the way Model handles a pinned model.
	Effort []string `json:"effort,omitempty"`

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
	// token counts work for it. Roots may use ~ and $VAR, expanded by
	// UsageSpec.ResolvedRoots; records are parsed generically, so any JSONL
	// carrying recognizable counters works.
	Usage *UsageSpec `json:"usage,omitempty"`

	// Note is shown by doctor, for definitions that need explaining.
	Note string `json:"note,omitempty"`
}

// UsageSpec locates a defined agent's transcripts. It mirrors the reader's
// own spec, which the CLI hands it to; keeping it here means the whole
// definition lives in one JSON object.
//
// Roots may name the reader's own {dir} placeholder, which it substitutes with
// the working directory on every poll and which therefore has to survive
// ResolvedRoots untouched.
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
	effortPlaceholder = "{effort}"
)

// allPlaceholders is every placeholder a definition may mention, in the order
// errors name them.
var allPlaceholders = []string{promptPlaceholder, modelPlaceholder, effortPlaceholder}

// validate reports whether a definition can actually launch something.
func (c Custom) validate(name string) error {
	if name == "" {
		return fmt.Errorf("custom agent needs a name")
	}
	if strings.ContainsAny(name, " \t,:=@") {
		return fmt.Errorf("invalid agent name %q: no spaces, commas, colons, equals signs, or at signs", name)
	}
	if err := namePrintable(name); err != nil {
		return err
	}
	if err := c.validateArgv(name); err != nil {
		return err
	}
	if len(c.Model) > 0 {
		if err := c.validatePinned(name, "model", modelPlaceholder, c.Model); err != nil {
			return err
		}
	}
	if len(c.Effort) > 0 {
		if err := c.validatePinned(name, "effort", effortPlaceholder, c.Effort); err != nil {
			return err
		}
	}
	if err := validateFixedArgs(name, "stream", c.Stream); err != nil {
		return err
	}
	if err := validateFixedArgs(name, "continue", c.Continue); err != nil {
		return err
	}
	if c.Usage != nil {
		if err := c.Usage.validate(name); err != nil {
			return err
		}
	}
	if c.Note != "" && strings.TrimSpace(c.Note) == "" {
		return fmt.Errorf("custom agent %q: note cannot be whitespace only", name)
	}
	// A note is printed after the agent's name in a doctor row and on the
	// launcher's own line, so the characters namePrintable refuses break a row
	// here too: a newline starts one the reader did not ask for, an escape
	// sequence colors text the run did not style (and does so under
	// --no-color, which suppresses only this program's own styling), and a
	// bidi override reverses the rest of the line. Refused rather than
	// stripped, the way a name is.
	if c.Note != "" && (!utf8.ValidString(c.Note) || strings.ContainsFunc(c.Note, hiddenRune)) {
		return fmt.Errorf("custom agent %q: note cannot hold control or formatting characters", name)
	}
	return nil
}

// namePrintable reports whether a name can be shown wherever it appears: as an
// agent label in the dashboard and the launcher, in a log line, and in the
// journal a run leaves behind.
//
// A name is the one string every other lookup keys on, and a name that is not
// valid UTF-8 makes each of those a byte comparison nobody can reproduce: it
// reaches a terminal as bytes no renderer can show, and it reads back from the
// journal repaired to U+FFFD, so the agent a run selected is not the agent the
// record names. A name holding a control character or a Unicode formatting
// character draws as itself where it is placed: a newline breaks a row of the
// launcher, a right-to-left override reverses the rest of the line, and a
// zero-width joiner renders two different names identically. Both are refused
// here rather than rewritten, the way every other untrusted display string in
// this project is: a name that has to be changed is not the name the operator
// wrote.
func namePrintable(name string) error {
	if !utf8.ValidString(name) {
		return fmt.Errorf("invalid agent name %q: not valid UTF-8", name)
	}
	if strings.ContainsFunc(name, hiddenRune) {
		return fmt.Errorf("invalid agent name %q: no control or formatting characters", name)
	}
	return nil
}

// hiddenRune reports a character that changes what a terminal draws rather than
// which word is read.
func hiddenRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
		unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
}

// validateArgv checks the launch line itself: a real executable, no
// placeholder in it, and exactly one argument carrying the prompt.
func (c Custom) validateArgv(name string) error {
	if len(c.Argv) == 0 {
		return fmt.Errorf("custom agent %q has no argv", name)
	}
	if strings.TrimSpace(c.Argv[0]) == "" {
		return fmt.Errorf("custom agent %q has no executable", name)
	}
	for _, p := range allPlaceholders {
		if strings.Contains(c.Argv[0], p) {
			return fmt.Errorf("custom agent %q: argv executable cannot contain %s", name, p)
		}
	}
	prompts := 0
	for _, a := range c.Argv {
		if strings.TrimSpace(a) == "" {
			return fmt.Errorf("custom agent %q: argv contains an empty argument", name)
		}
		if strings.Contains(a, promptPlaceholder) {
			prompts++
		}
	}
	if prompts == 0 {
		return fmt.Errorf("custom agent %q: argv must contain %s", name, promptPlaceholder)
	}
	if prompts > 1 {
		return fmt.Errorf("custom agent %q: argv must contain %s exactly once", name, promptPlaceholder)
	}
	return nil
}

// validatePinned checks a field that carries exactly one placeholder of its
// own, model or effort. The prompt belongs in argv and the other placeholder
// to the other field, and argv may not repeat what the field already supplies:
// the two would both pin the same thing.
func (c Custom) validatePinned(name, field, own string, args []string) error {
	if err := noEmptyArgs(name, field, args); err != nil {
		return err
	}
	if !containsPlaceholder(args, own) {
		return fmt.Errorf("custom agent %q: %s must contain %s", name, field, own)
	}
	for _, p := range allPlaceholders {
		if p == own {
			continue
		}
		if containsPlaceholder(args, p) {
			return fmt.Errorf("custom agent %q: %s cannot contain %s", name, field, p)
		}
	}
	if containsPlaceholder(c.Argv, own) {
		return fmt.Errorf("custom agent %q: %s cannot be specified when argv contains %s", name, field, own)
	}
	return nil
}

// noEmptyArgs rejects a field carrying a blank argument. An empty argument
// would exec as an empty string, which a CLI reads as a missing value.
func noEmptyArgs(name, field string, args []string) error {
	for _, a := range args {
		if strings.TrimSpace(a) == "" {
			return fmt.Errorf("custom agent %q: %s contains an empty argument", name, field)
		}
	}
	return nil
}

// validateFixedArgs checks a flag field the expansion never fills, so a
// placeholder in it would reach the CLI verbatim.
func validateFixedArgs(name, field string, args []string) error {
	if err := noEmptyArgs(name, field, args); err != nil {
		return err
	}
	for _, p := range allPlaceholders {
		if containsPlaceholder(args, p) {
			return fmt.Errorf("custom agent %q: %s cannot contain %s", name, field, p)
		}
	}
	return nil
}

// validate checks the transcript locator: at least one root, none of them
// blank, and no placeholder in any of them, since the reader resolves them as
// given. Nothing here touches the filesystem.
func (u UsageSpec) validate(name string) error {
	if len(u.Roots) == 0 {
		return fmt.Errorf("custom agent %q: usage.roots must name at least one directory", name)
	}
	for _, r := range u.Roots {
		if strings.TrimSpace(r) == "" {
			return fmt.Errorf("custom agent %q: usage.roots contains an empty directory path", name)
		}
		for _, p := range allPlaceholders {
			if strings.Contains(r, p) {
				return fmt.Errorf("custom agent %q: usage.roots cannot contain %s", name, p)
			}
		}
	}
	if u.Suffix != "" && strings.TrimSpace(u.Suffix) == "" {
		return fmt.Errorf("custom agent %q: usage.suffix cannot be whitespace only", name)
	}
	for _, p := range allPlaceholders {
		if strings.Contains(u.Suffix, p) {
			return fmt.Errorf("custom agent %q: usage.suffix cannot contain %s", name, p)
		}
	}
	if _, err := u.ResolvedRoots(); err != nil {
		return fmt.Errorf("custom agent %q: %w", name, err)
	}
	return nil
}

// ResolvedRoots expands ~ and $VAR in every transcript root, so the caller
// handing them to the session reader gives it a path it can open.
//
// The transcript reader expands a leading ~ itself and substitutes {dir} per
// working directory, but it does not expand $VAR: a root written
// "$AGENT_HOME/sessions" reached it verbatim and the walk then looked for a
// directory literally named that, found nothing, and the agent's live token
// counts stayed at zero with no error anywhere. Every other operator-supplied
// path in this program -- --bin, --log, --dir, --prompt-dir,
// --sandbox-write, GAUNTLET_HOME -- expands $VAR through ExpandPath, so this
// is the same rule applied to the one path that had none.
//
// A root carrying {dir} still has its variables expanded: only the placeholder
// itself is left alone, since the reader substitutes the working directory on
// every poll and no expansion turns it into a path. ExpandPath already leaves
// {dir} in place (it is neither a $VAR nor a leading ~), so the two can be
// mixed freely: "$AGENT_HOME/{dir}/sessions" resolves the variable and keeps
// the placeholder.
//
// An unset or empty $VAR is an error, the same answer ExpandPath gives every
// other path: silently dropping the variable would turn "$AGENT_HOME/sessions"
// into "/sessions", a directory that exists on many machines and belongs to
// nobody.
func (u UsageSpec) ResolvedRoots() ([]string, error) {
	out := make([]string, 0, len(u.Roots))
	for _, r := range u.Roots {
		expanded, err := gauntlethome.ExpandPath(r)
		if err != nil {
			return nil, fmt.Errorf("usage.roots %s: %w", r, err)
		}
		out = append(out, expanded)
	}
	return out, nil
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

// Register adds a custom agent definition. It refuses a name that is already
// defined, by any spelling, so a redefinition is a visible error rather than a
// silent last-one-wins; Unregister first if replacing one is the intent.
func Register(name string, def Custom) error {
	name = fuzzy.NFC(name)
	if err := def.validate(name); err != nil {
		return err
	}
	if isBuiltinTool(name) {
		return fmt.Errorf("%q is a built-in agent and cannot be redefined", name)
	}
	customMu.Lock()
	defer customMu.Unlock()
	if prev, clash := lookupCustom(name); clash {
		return fmt.Errorf("%q is already defined as %q; agent names differ by case alone, so only one spelling is selectable", name, prev)
	}
	custom[name] = def
	return nil
}

// Unregister removes a custom agent definition.
func Unregister(name string) {
	customMu.Lock()
	defer customMu.Unlock()
	if prev, ok := lookupCustom(fuzzy.NFC(name)); ok {
		delete(custom, prev)
	}
}

// CustomDef returns the definition for a custom agent. The name folds case,
// the way every other lookup of an agent name does: -a folds the spelling
// before it reaches here, so a definition written "MyAgent" is still found
// under "myagent" and vice versa.
func CustomDef(name string) (Custom, bool) {
	customMu.RLock()
	defer customMu.RUnlock()
	key, ok := lookupCustom(fuzzy.NFC(name))
	if !ok {
		return Custom{}, false
	}
	return custom[key], true
}

// lookupCustom finds the stored key for a name, matching it through foldName
// so a definition is found by the same spelling every other agent-name lookup
// produces. The caller holds customMu. A name differing from an existing one
// only by case is refused at Register, so at most one key can match.
func lookupCustom(name string) (string, bool) {
	if _, ok := custom[name]; ok {
		return name, true
	}
	key := foldName(name)
	for k := range custom {
		if foldName(k) == key {
			return k, true
		}
	}
	return "", false
}

// CustomNames lists the defined custom agents, in name order.
func CustomNames() []string {
	customMu.RLock()
	defer customMu.RUnlock()
	out := make([]string, 0, len(custom))
	for n := range custom {
		out = append(out, n)
	}
	fuzzy.Sort(out)
	return out
}

// isBuiltinTool reports whether a name is compiled in, as opposed to defined.
// Case folds, because a redefinition of a built-in under another spelling is
// still a redefinition of it.
func isBuiltinTool(name string) bool {
	return slices.ContainsFunc(Valid, func(v string) bool { return strings.EqualFold(v, name) })
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
	// Compose before returning, for the reason Register does it before storing.
	// The caller folds the name it gets back to notice a duplicate --agent-cmd,
	// and folds it as text; a decomposed spelling folded as text never meets its
	// composed twin. So the name spelled café (e plus a combining acute) and the
	// same name spelled with the precomposed é both reached Register as two
	// spellings of one name, and the loser was refused for colliding with the
	// first, by a message quoting two names that print identically. Returning the
	// composed form leaves every fold downstream agreeing on the one spelling the
	// registry is keyed by.
	name = fuzzy.NFC(name)
	def := Custom{Argv: strings.Fields(rest)}
	if err := def.validate(name); err != nil {
		return "", Custom{}, err
	}
	return name, def, nil
}

// maxCustomFileBytes bounds the definitions file. A definition is a few
// hundred bytes and agents.json holds a handful, so a legitimate file is
// kilobytes; anything larger is not a definitions file, and reading it whole
// would size this process's heap from a file the user never meant to open
// this way. The bound is the same one prompt.readNoFollow and LoadState put
// on the other operator-supplied files, so every one of them refuses the same
// way.
const maxCustomFileBytes = 4 << 20

// readCustomFile reads the definitions file, refusing a symlink and anything
// that is not a regular file, and bounding how much of it is read.
//
// os.ReadFile follows a symlink and allocates whatever the file holds, and
// this is the one operator-supplied file that carries executable argv: the
// definition's Argv is exec'd as-is. CustomFilePath resolves under
// GAUNTLET_HOME, which gauntlethome.Dir deliberately permits to point inside
// the reviewed tree, so a repository that ships .gauntlet/agents.json as a
// link would otherwise have its target's argv registered and run. safefile's
// guarded open refuses the link at the last component and a FIFO or device in
// its place, the same refusal prompt.readNoFollow applies for the same reason.
//
// A missing file is not an error; the caller has nothing to load. Anything
// else wrong is reported here, with the path named, because silently running
// with the wrong agent set is worse than refusing to start.
func readCustomFile(path string) ([]byte, error) {
	f, _, err := safefile.OpenRead(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read agent definitions %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCustomFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read agent definitions %s: %w", path, err)
	}
	if len(data) > maxCustomFileBytes {
		return nil, fmt.Errorf("agent definitions %s exceeds %d bytes", path, maxCustomFileBytes)
	}
	return data, nil
}

// LoadCustomFile reads agent definitions from a JSON file, if it exists. The
// file maps a name to a definition:
//
//	{"pi": {"argv": ["pi","-p","{prompt}"], "stream": ["--json"]}}
//
// A missing file is not an error; anything else wrong is, because silently
// running with the wrong agent set is worse than refusing to start. Unknown
// keys are refused like syntax errors: a misspelled one ("optin" for
// "opt_in") would otherwise be dropped on the floor and quietly change what
// the definition does.
func LoadCustomFile(path string) error {
	// A missing file is nil data and no error: there is nothing to load, and
	// readCustomFile has already said so with the path it would have refused.
	data, err := readCustomFile(path)
	if err != nil || data == nil {
		return err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var defs map[string]Custom
	if err := unmarshalStrict(data, &defs); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if defs == nil {
		return fmt.Errorf("%s: agent definitions must be a JSON object, not null", path)
	}
	// Validate the complete file before changing the registry. Otherwise map
	// iteration can install some definitions before a later invalid one makes
	// startup fail, leaving callers that recover from the error half-configured.
	names := slices.Sorted(maps.Keys(defs))
	for _, name := range names {
		if err := defs[name].validate(name); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if isBuiltinTool(name) {
			return fmt.Errorf("%s: %q is a built-in agent and cannot be redefined", path, name)
		}
	}
	for _, name := range names {
		if err := Register(name, defs[name]); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

// unmarshalStrict decodes exactly one JSON value: unknown fields are errors,
// so a typo cannot drop part of a definition, and trailing data is rejected
// as encoding/json.Unmarshal rejects it.
func unmarshalStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Decoder.More treats a leftover '}' or ']' as the end of a value, so
	// "{}}" would load as {} and a corrupt agents.json would start the
	// process with an empty definition set. Token is the same check
	// Unmarshal uses: only EOF after whitespace is a clean end.
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return errors.New("unexpected data after the JSON value")
		}
		return err
	}
	return rejectDuplicateKeys(json.NewDecoder(bytes.NewReader(data)), false)
}

func rejectDuplicateKeys(dec *json.Decoder, foldCase bool) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	keys := make(map[string]bool)
	for dec.More() {
		if delim == '{' {
			token, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok {
				return fmt.Errorf("expected string key, got %T", token)
			}
			normKey := fuzzy.NFC(key)
			if keys[normKey] {
				return fmt.Errorf("duplicate key %q", key)
			}
			if foldCase {
				for previous := range keys {
					if strings.EqualFold(previous, normKey) {
						return fmt.Errorf("duplicate key %q", key)
					}
				}
			}
			keys[normKey] = true
		}
		if err := rejectDuplicateKeys(dec, true); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

// CustomFilePath is where agent definitions live by default: agents.json in
// the state root gauntlethome.Dir resolves (GAUNTLET_HOME, else
// $HOME/.gauntlet). Unlike the journal, a missing HOME yields "" instead of a
// path in the working directory: definitions carry executable argv, and
// picking one up from ./.gauntlet would let the reviewed tree define its own
// agents.
func CustomFilePath() string {
	root, ok := gauntlethome.Dir()
	if !ok {
		return ""
	}
	return filepath.Join(root, "agents.json")
}

// DefinitionsOnDisk reports how many definitions the file at path holds.
//
// This is the file as an archive carried it, not the registry: a restored
// tree whose agents.json was lost reads here as zero definitions while the
// process keeps running the agents it registered at startup, and the next run
// quietly reviews with the built-ins. Counting the file is what makes that
// visible, which is why nothing here consults `custom`.
//
// path is an argument rather than CustomFilePath() so a caller checking a
// restored tree reads the tree it was told about rather than whatever this
// process's own state root resolved to. The count is the file's top-level
// entries and nothing more: it answers "did this archive carry the definitions
// it was supposed to", which is a question about the copy. Whether each
// definition is one this build can run is LoadCustomFile's question, and a
// file that cannot be parsed at all is reported as an error beside the count so
// a corrupt copy is not read as a tree with no agents.
func DefinitionsOnDisk(path string) (int, error) {
	data, err := readCustomFile(path)
	if err != nil || data == nil {
		return 0, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var defs map[string]json.RawMessage
	if err := unmarshalStrict(data, &defs); err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	if defs == nil {
		return 0, fmt.Errorf("%s: agent definitions must be a JSON object, not null", path)
	}
	return len(defs), nil
}

// buildCustom expands a custom definition into an argv.
func buildCustom(def Custom, spec Spec, prompt string, opts BuildOpts) []string {
	// One replacer over every placeholder, rather than a chain that stops at
	// the first kind an argument mentions: nothing says an argument carries
	// only one. A definition that packs its settings into a single option
	// ("--opts=model={model},effort={effort}") needs every placeholder in
	// that one argument expanded, not just the leading one.
	//
	// A Replacer scans the input once and never rescans what it substituted,
	// so a prompt that happens to contain "{model}" stays the text the review
	// wrote; a sequence of ReplaceAll calls would not hold that.
	rep := strings.NewReplacer(
		promptPlaceholder, prompt,
		modelPlaceholder, spec.Model,
		effortPlaceholder, spec.Effort,
	)
	// An argument mentioning a placeholder the spec did not pin is dropped
	// whole rather than expanded to nothing. That is what the Model and
	// Effort blocks already do -- they are appended only when the value is
	// there -- and the two spellings of "where the model goes" have to agree.
	// They did not: a definition using the block ran as `myagent -p PROMPT`,
	// while one writing {model} into its argv ran as `myagent --model= -p
	// PROMPT`, handing the CLI an empty value to reject, or to take as one.
	//
	// The argument carrying the prompt is never dropped: it is the task, and
	// validate guarantees exactly one argv entry holds it. A definition that
	// packs a setting into that same argument keeps its unexpanded shape
	// rather than losing the review; settings that vary independently belong
	// in arguments of their own.
	unset := func(a string) bool {
		if strings.Contains(a, promptPlaceholder) {
			return false
		}
		return (spec.Model == "" && strings.Contains(a, modelPlaceholder)) ||
			(spec.Effort == "" && strings.Contains(a, effortPlaceholder))
	}
	expand := func(in []string) []string {
		out := make([]string, 0, len(in))
		for _, a := range in {
			if unset(a) {
				continue
			}
			out = append(out, rep.Replace(a))
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
	if spec.Effort != "" && len(def.Effort) > 0 {
		argv = append(argv, def.Effort...)
	}
	argv = append(argv, head[promptAt:]...)

	return expand(argv)
}
