// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/normalize"
)

// clispecVersion is the candidate this binary describes itself against.
// v0.3 is not frozen: the document names it so a consumer can read the
// contract, and nothing in the binary claims a badge.
const clispecVersion = "0.3"

// flaggedError is a usage failure whose envelope kind is narrower than usage.
// confirmation_bypass_arg is deliberately unset: that key promises every
// prompt refuses without a terminal, and the plan prompt proceeds.
type flaggedError struct {
	error
	kind string
	hint string
}

func (e flaggedError) Unwrap() error { return e.error }

// exitRecord is what the process-exit envelope is built from. run resets it
// on entry so one test's failure cannot leak into the next call.
type exitRecord struct {
	json bool
	kind string
	msg  string
	hint string
}

var exitState exitRecord

func resetExit() { exitState = exitRecord{} }

func noteExit(kind, msg, hint string) {
	if exitState.kind != "" {
		return
	}
	exitState.kind = kind
	exitState.msg = msg
	exitState.hint = hint
}

func (o *options) wantsJSON() bool { return o != nil && o.output == "json" }

// jsonRequested reports whether argv asked for JSON even when parsing did
// not finish. The envelope is owed whenever that request is visible.
func jsonRequested(argv []string) bool {
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			return false
		}
		switch a {
		case "--json", "--json=true", "--output=json", "-ojson", "-o=json":
			return true
		case "--output", "-o":
			if i+1 < len(argv) && argv[i+1] == "json" {
				return true
			}
		}
	}
	return false
}

func kindForCode(code int) string {
	switch code {
	case exitUsage:
		return "usage"
	case exitLocked:
		return "locked"
	case exitInterrupted:
		return "interrupted"
	case exitFail:
		return "failed"
	default:
		return "internal"
	}
}

func defaultExitMessage(kind string, code int) string {
	switch kind {
	case "usage":
		return "usage error"
	case "failed":
		return "command failed"
	case "locked":
		return "another instance holds the lock"
	case "interrupted":
		return "interrupted"
	case "tty_required":
		return "a terminal is required"
	case "confirmation_required":
		return "confirmation required"
	case "internal":
		return "internal error"
	default:
		return fmt.Sprintf("exit %d", code)
	}
}

// writeExitEnvelope appends the structured error as the last line of stderr
// when the caller asked for JSON and the process is failing. Text mode keeps
// the prose already written. Exit 0 writes nothing.
func writeExitEnvelope(code int) {
	if code == 0 || !exitState.json {
		return
	}
	kind := exitState.kind
	if kind == "" {
		kind = kindForCode(code)
	}
	msg := exitState.msg
	if msg == "" {
		msg = defaultExitMessage(kind, code)
	}
	msg = normalize.Sanitize(msg)
	errObj := map[string]any{"kind": kind, "message": msg}
	if hint := normalize.Sanitize(exitState.hint); hint != "" {
		errObj["hint"] = hint
	}
	enc := json.NewEncoder(os.Stderr)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]any{"error": errObj})
}

// ndjsonText turns the review command's existing lines into one JSON object
// per line. The log file still receives the bytes that were written, because
// the wrapper sits on the console side of the tee.
type ndjsonText struct {
	dst io.Writer
	buf []byte
}

func (n *ndjsonText) Write(p []byte) (int, error) {
	n.buf = append(n.buf, p...)
	for {
		i := bytes.IndexByte(n.buf, '\n')
		if i < 0 {
			break
		}
		line := n.buf[:i]
		n.buf = append([]byte(nil), n.buf[i+1:]...)
		if err := n.emit(line); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}

func (n *ndjsonText) Flush() error {
	if len(bytes.TrimSpace(n.buf)) == 0 {
		n.buf = nil
		return nil
	}
	err := n.emit(n.buf)
	n.buf = nil
	return err
}

func (n *ndjsonText) emit(line []byte) error {
	s := strings.TrimRight(string(line), "\r")
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(struct {
		Text string `json:"text"`
	}{normalize.Sanitize(s)}); err != nil {
		return err
	}
	_, err := n.dst.Write(buf.Bytes())
	return err
}

// schemaValue is a default that stays in the document when the value is
// false or 0. encoding/json treats those as empty inside an any field, and
// a default of 0 is still a default.
type schemaValue struct {
	v   any
	set bool
}

func (s schemaValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.v)
}

func (s schemaValue) IsZero() bool { return !s.set }

type schemaArg struct {
	Name        string      `json:"name"`
	Short       string      `json:"short,omitempty"`
	Type        string      `json:"type"`
	Enum        []string    `json:"enum,omitempty"`
	Default     schemaValue `json:"default,omitempty"`
	Description string      `json:"description,omitempty"`
	Required    bool        `json:"required,omitempty"`
}

type schemaField struct {
	Name     string       `json:"name"`
	Type     string       `json:"type"`
	Nullable bool         `json:"nullable,omitempty"`
	Items    *schemaItems `json:"items,omitempty"`
}

type schemaItems struct {
	Type string `json:"type"`
}

type schemaPagination struct {
	Style       string `json:"style"`
	OffsetArg   string `json:"offset_arg,omitempty"`
	LimitArg    string `json:"limit_arg,omitempty"`
	CursorArg   string `json:"cursor_arg,omitempty"`
	CursorField string `json:"cursor_field,omitempty"`
}

type schemaCommand struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Effects      string            `json:"effects"`
	OutputKind   string            `json:"output_kind,omitempty"`
	StreamFormat string            `json:"stream_format,omitempty"`
	MediaType    string            `json:"media_type,omitempty"`
	Cardinality  string            `json:"cardinality,omitempty"`
	Pagination   *schemaPagination `json:"pagination,omitempty"`
	FieldsArg    string            `json:"fields_arg,omitempty"`
	RequiresTTY  bool              `json:"requires_tty,omitempty"`
	Args         []schemaArg       `json:"args,omitempty"`
	OutputFields []schemaField     `json:"output_fields,omitempty"`
	StdoutSchema json.RawMessage   `json:"stdout_schema,omitempty"`
	Errors       []string          `json:"errors,omitempty"`
	Extensions   map[string]any    `json:"extensions,omitempty"`
}

type schemaError struct {
	Kind        string `json:"kind"`
	ExitCode    int    `json:"exit_code"`
	Retryable   bool   `json:"retryable"`
	Description string `json:"description,omitempty"`
}

type schemaDoc struct {
	Clispec     string            `json:"clispec"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Output      map[string]string `json:"output"`
	GlobalArgs  []schemaArg       `json:"global_args"`
	Commands    []schemaCommand   `json:"commands"`
	Errors      []schemaError     `json:"errors"`
	Extensions  map[string]any    `json:"extensions,omitempty"`
}

// schemaSkip are flags that are global, runs-only, or update-only, so the
// review command's argument list does not repeat them. update-repo stays:
// --auto-update reads it during a review.
var schemaSkip = map[string]bool{
	"h": true, "help": true, "V": true, "version": true,
	"log": true, "no-color": true, "o": true, "output": true, "json": true,
	"limit": true, "offset": true, "restore": true, "fields": true,
	"check": true,
}

func buildSchema() schemaDoc {
	return schemaDoc{
		Clispec:     clispecVersion,
		Name:        "gauntlet",
		Version:     version,
		Description: "Run a codebase through bundled review prompts on installed coding-agent CLIs.",
		Output:      map[string]string{"tty": "text", "piped": "text"},
		GlobalArgs: []schemaArg{
			{
				Name: "--output", Short: "-o", Type: "string",
				Enum: []string{"auto", "text", "json"}, Default: schemaValue{v: "auto", set: true},
				Description: "Output format. auto keeps text on a terminal and when piped. Explicit json or --json selects JSON.",
			},
			{Name: "--json", Type: "boolean", Default: schemaValue{v: false, set: true}, Description: "Alias of --output json."},
			{Name: "--no-color", Type: "boolean", Description: "Disable color. NO_COLOR does the same."},
			{Name: "--log", Type: "path", Description: "Also write output to FILE."},
			{Name: "--help", Short: "-h", Type: "boolean", Description: "Show help and exit."},
			{Name: "--version", Short: "-V", Type: "boolean", Description: "Print the version and exit."},
		},
		Commands: []schemaCommand{
			reviewCommand(),
			{
				Name: "pick", Effects: "non_idempotent", OutputKind: "opaque",
				MediaType: "text/plain", RequiresTTY: true,
				Description: "Compose a run on a terminal, then run the command it built. The format flag does not re-encode a successful launch.",
				Errors:      []string{"usage", "tty_required", "failed", "interrupted"},
				Args:        argsByName([]string{"dir", "dirs", "target-dirs", "prompt-dir"}),
			},
			{
				Name: "doctor", Effects: "read_only", OutputKind: "data", Cardinality: "single",
				Description:  "Report which agent CLIs and helper tools are installed, and whether the state root can hold a journal.",
				OutputFields: doctorOutputFields(),
				Errors:       []string{"failed", "usage"},
				Args:         argsByName([]string{"agent-cmd", "bin"}),
			},
			{
				Name: "update", Effects: "idempotent", OutputKind: "data", Cardinality: "single",
				Description: "Replace this binary with the latest verified release. A second run exits 0 when the binary is already current. --check prints the release and does not install.",
				OutputFields: []schemaField{
					{Name: "current", Type: "string"},
					{Name: "latest", Type: "string"},
					{Name: "changed", Type: "boolean"},
					{Name: "url", Type: "string"},
					{Name: "installed_to", Type: "string"},
				},
				Errors: []string{"failed", "usage", "interrupted"},
				Args:   argsByName([]string{"check", "update-repo"}),
			},
			runsCommand(),
			{
				Name: "show", Effects: "read_only", OutputKind: "stream", StreamFormat: "ndjson",
				Description: "Replay one run's journal. The run id is a required positional argument. Text mode prints a timestamp, the event kind, and the remaining fields. JSON mode prints one object per event, including ts and ev.",
				Errors:      []string{"usage", "failed"},
			},
			{
				Name: "resume", Effects: "non_idempotent", OutputKind: "data", Cardinality: "bounded",
				StdoutSchema: json.RawMessage("{}"),
				Description:  "With no id, list runs a crash cut off. With an id, continue that run. The two successes have different shapes, so the stdout schema accepts either document.",
				Errors:       []string{"usage", "failed", "locked"},
			},
			{
				Name: "schema", Effects: "read_only", OutputKind: "opaque", MediaType: "application/json",
				Description: "Print this clispec document. The format flag does not re-encode a successful print. An optional command path narrows commands and keeps the top-level fields.",
				Errors:      []string{"usage"},
			},
			{
				Name: "version", Effects: "read_only", OutputKind: "data", Cardinality: "single",
				Description: "Print the program name and version.",
				OutputFields: []schemaField{
					{Name: "name", Type: "string"},
					{Name: "version", Type: "string"},
				},
				Errors: []string{"usage", "failed"},
			},
			{
				Name: "help", Effects: "read_only", OutputKind: "opaque", MediaType: "text/plain",
				Description: "Show the help screen. The format flag does not re-encode it.",
				Errors:      []string{"usage"},
			},
		},
		Errors: []schemaError{
			{Kind: "usage", ExitCode: exitUsage, Description: "The arguments were not a command this binary accepts."},
			{Kind: "tty_required", ExitCode: exitUsage, Description: "The command needs a terminal and does not have one."},
			{Kind: "confirmation_required", ExitCode: exitUsage, Description: "A confirmation that already refuses unattended input was not given."},
			{Kind: "failed", ExitCode: exitFail, Description: "The command ran and did not succeed."},
			{Kind: "internal", ExitCode: exitFail, Description: "The process failed in a way that has no more specific kind."},
			{Kind: "locked", ExitCode: exitLocked, Retryable: true, Description: "Another instance holds the directory lock."},
			{Kind: "interrupted", ExitCode: exitInterrupted, Description: "The process was interrupted."},
		},
		Extensions: map[string]any{
			"clispec_status": "0.3 is a candidate (August 2026). This document follows https://clispec.dev. It is not a conformance badge.",
		},
	}
}

func reviewCommand() schemaCommand {
	return schemaCommand{
		Name: "review", Effects: "non_idempotent", OutputKind: "stream", StreamFormat: "ndjson",
		Description: "Run bundled review prompts on the working tree. The same command is a bare gauntlet. The tree is the input. --dry-run, --list, --once, and --seed are the safe moves. A retry can change more files, so there is no idempotency key.",
		Errors:      []string{"usage", "tty_required", "confirmation_required", "failed", "locked", "interrupted"},
		Args:        reviewArgs(),
		Extensions: map[string]any{
			"retry":        "The tree is the input. --dry-run, --list, --once, and --seed are the safe moves. A retry can change more files.",
			"confirmation": "The plan prompt proceeds when stdin is not a terminal. Commit confirmation and a stacked run's dirty-checkout exclusion refuse unless --yes or --yolo is set. confirmation_bypass_arg is omitted because it would promise that every prompt refuses without a terminal.",
			"invocation":   "Also invocable as a bare gauntlet, with no subcommand.",
			"stream":       "JSON mode emits one {\"text\": \"...\"} record per line of the existing human feed. Text mode stays that feed.",
		},
	}
}

func runsCommand() schemaCommand {
	return schemaCommand{
		Name: "runs", Effects: "non_idempotent", OutputKind: "data", Cardinality: "unbounded",
		FieldsArg:    "--fields",
		Pagination:   &schemaPagination{Style: "offset", OffsetArg: "--offset", LimitArg: "--limit"},
		Description:  "List recent runs. Listing is read-only. --restore writes a pruned journal back into the listing and exits 2 when that run is already listed, so the command is non_idempotent. The run id identifies the mutation.",
		OutputFields: summaryFieldDocs(),
		Errors:       []string{"usage", "failed"},
		Args:         argsByName([]string{"limit", "offset", "restore", "fields"}),
		Extensions: map[string]any{
			"restore":  "Listing is read-only. --restore is the mutation. A second restore of a run that is already listed exits 2.",
			"envelope": "The document keeps home, journals, runs, pruned, and history, and adds items, total, offset, limit, and truncated. items is the same records as runs after --fields.",
		},
	}
}

func doctorOutputFields() []schemaField {
	return []schemaField{
		{Name: "agents", Type: "array", Items: &schemaItems{Type: "object"}},
		{Name: "core", Type: "array", Items: &schemaItems{Type: "object"}},
		{Name: "git", Type: "object", Nullable: true},
		{Name: "sandbox", Type: "object"},
		{Name: "recommended_missing", Type: "array", Items: &schemaItems{Type: "string"}},
		{Name: "state", Type: "object"},
		{Name: "history", Type: "object", Nullable: true},
		{Name: "unlanded", Type: "array", Items: &schemaItems{Type: "string"}},
		{Name: "ok", Type: "boolean"},
	}
}

func summaryFieldDocs() []schemaField {
	var out []schemaField
	typ := reflect.TypeFor[journal.Summary]()
	for field := range typ.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out = append(out, schemaFieldFor(name, field.Type))
	}
	return out
}

func summaryFieldNames() map[string]bool {
	names := map[string]bool{}
	for _, f := range summaryFieldDocs() {
		names[f.Name] = true
	}
	return names
}

func schemaFieldFor(name string, t reflect.Type) schemaField {
	f := schemaField{Name: name}
	if t.Kind() == reflect.Pointer {
		f.Nullable = true
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		f.Type = "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		f.Type = "integer"
	case reflect.Float32, reflect.Float64:
		f.Type = "number"
	case reflect.String:
		f.Type = "string"
	case reflect.Slice:
		f.Type = "array"
		elem := schemaFieldFor("", t.Elem())
		f.Items = &schemaItems{Type: elem.Type}
	default:
		f.Type = "string"
	}
	return f
}

// schemaFlagSet registers the real flag defaults. Duration flags report
// whatever value they already hold as their default text, so this matches
// the options parseFlags builds before it registers them.
func schemaFlagSet() *flag.FlagSet {
	o := &options{
		bin:            map[string]string{},
		timeout:        defaultTimeout,
		suggestTimeout: defaultTimeout,
		jobs:           1,
		retries:        defaultRetries,
		hotReload:      true,
		stream:         true,
		runsLimit:      defaultRunsLimit,
		keepRuns:       defaultKeepRuns,
	}
	fs, _ := buildFlagSet(o)
	return fs
}

func reviewArgs() []schemaArg {
	fs := schemaFlagSet()
	var out []schemaArg
	for _, g := range helpGroups {
		for _, f := range g.Flags {
			if schemaSkip[f.Long] {
				continue
			}
			out = append(out, flagArg(fs, f))
		}
	}
	return out
}

func argsByName(names []string) []schemaArg {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	fs := schemaFlagSet()
	var out []schemaArg
	for _, g := range helpGroups {
		for _, f := range g.Flags {
			if want[f.Long] {
				out = append(out, flagArg(fs, f))
			}
		}
	}
	return out
}

func flagArg(fs *flag.FlagSet, f flagDoc) schemaArg {
	a := schemaArg{Name: "--" + f.Long, Type: "string", Description: f.Help}
	if f.Short != "" {
		a.Short = "-" + f.Short
	}
	rf := fs.Lookup(f.Long)
	if rf == nil {
		return a
	}
	if isBoolFlag(rf) {
		a.Type = "boolean"
		if rf.DefValue == "true" {
			a.Default = schemaValue{v: true, set: true}
		}
		return a
	}
	if _, ok := rf.Value.(*listFlag); ok {
		a.Type = "string[]"
		return a
	}
	if rf.DefValue == "" {
		return a
	}
	if n, err := strconv.Atoi(rf.DefValue); err == nil {
		a.Type = "integer"
		a.Default = schemaValue{v: n, set: true}
		return a
	}
	a.Default = schemaValue{v: rf.DefValue, set: true}
	return a
}

func checkSummaryFields(fields []string) error {
	known := summaryFieldNames()
	for _, f := range fields {
		if !known[f] {
			return fmt.Errorf("--fields: unknown field %q", f)
		}
	}
	return nil
}

// cmdSchema prints the clispec document. It does not read the state root,
// the network, or a config file. An unknown command path is a usage error.
func cmdSchema(out io.Writer, filter []string) int {
	doc := buildSchema()
	if len(filter) == 1 {
		kept := doc.Commands[:0]
		for _, c := range doc.Commands {
			if c.Name == filter[0] || strings.HasPrefix(c.Name, filter[0]+" ") {
				kept = append(kept, c)
			}
		}
		if len(kept) == 0 {
			msg := fmt.Sprintf("no such command: %q (see: gauntlet schema)", filter[0])
			fmt.Fprintln(os.Stderr, msg)
			noteExit("usage", msg, "Run gauntlet schema with no path to list every command.")
			return exitUsage
		}
		doc.Commands = kept
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write the schema: %v\n", err)
		noteExit("failed", "cannot write the schema", "")
		return exitFail
	}
	return exitOK
}

func writeVersionJSON(out io.Writer) int {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}{"gauntlet", version}); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write the version: %v\n", err)
		noteExit("failed", "cannot write the version", "")
		return exitFail
	}
	return exitOK
}
