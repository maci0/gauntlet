// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/report"
)

// The schema is checked by hand. Fetching the document at test time, or
// pulling in a JSON Schema library, would make the suite depend on a network
// and on a parser this module does not otherwise have. The rules below are
// the ones the binary claims: flat commands, the three output kinds, and the
// references an argument name has to resolve.
func TestSchemaDocumentFollowsClispec(t *testing.T) {
	raw, err := json.Marshal(buildSchema())
	if err != nil {
		t.Fatal(err)
	}
	checkClispec(t, raw)

	code, out, errOut := runBoth(t, []string{"schema"})
	if code != exitOK {
		t.Fatalf("schema exited %d\n%s", code, errOut)
	}
	noEnvelope(t, errOut)
	checkClispec(t, []byte(out))

	var fromCode, fromStdout map[string]any
	if err := json.Unmarshal(raw, &fromCode); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &fromStdout); err != nil {
		t.Fatalf("schema stdout is not JSON: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(fromCode, fromStdout) {
		t.Fatal("gauntlet schema printed a different document than buildSchema")
	}
}

func TestSchemaFilterKeepsTheTopLevel(t *testing.T) {
	code, out, errOut := runBoth(t, []string{"schema", "runs"})
	if code != exitOK {
		t.Fatalf("schema runs exited %d\n%s", code, errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("schema runs: %v\n%s", err, out)
	}
	for _, key := range []string{"clispec", "name", "version", "global_args", "errors"} {
		if doc[key] == nil {
			t.Errorf("schema runs dropped top-level %s", key)
		}
	}
	commands := asSlice(t, doc["commands"], "commands")
	if len(commands) != 1 || asMap(t, commands[0], "command")["name"] != "runs" {
		t.Fatalf("schema runs kept %#v", doc["commands"])
	}

	// An explicit text format does not re-encode a successful schema print.
	// The document is the output, and a second encoding would be a different
	// contract than the one the command declares.
	code, out, errOut = runBoth(t, []string{"schema", "-o", "text"})
	if code != exitOK {
		t.Fatalf("schema -o text exited %d\n%s", code, errOut)
	}
	if !strings.Contains(out, `"clispec"`) {
		t.Fatalf("schema -o text did not print the document:\n%s", out)
	}
	noEnvelope(t, errOut)
}

func TestSchemaRejectsAnUnknownCommand(t *testing.T) {
	code, _, errOut := runBoth(t, []string{"schema", "no-such"})
	if code != exitUsage {
		t.Fatalf("schema no-such exited %d, want %d\n%s", code, exitUsage, errOut)
	}
	if !strings.Contains(errOut, "no such command") {
		t.Fatalf("stderr is %q", errOut)
	}
	noEnvelope(t, errOut)

	code, _, errOut = runBoth(t, []string{"schema", "no-such", "-o", "json"})
	if code != exitUsage {
		t.Fatalf("schema no-such -o json exited %d\n%s", code, errOut)
	}
	if kind := errorEnvelope(t, errOut)["kind"]; kind != "usage" {
		t.Fatalf("kind %v, want usage\n%s", kind, errOut)
	}
}

// schema returns before the state root is checked, the same way version does.
// A GAUNTLET_HOME that is a file is a usage error for a review and must not
// be one for a description of the binary.
func TestSchemaDoesNotNeedAStateHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(home, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAUNTLET_HOME", home)
	code, out, errOut := runBoth(t, []string{"schema"})
	if code != exitOK {
		t.Fatalf("schema with an unusable state root exited %d\n%s", code, errOut)
	}
	if !strings.Contains(out, `"clispec": "0.3"`) {
		t.Fatalf("stdout:\n%s", out)
	}
}

func TestHelpNamesSchema(t *testing.T) {
	var b strings.Builder
	printUsage(&b, report.Palette{}, 100)
	if !strings.Contains(b.String(), "schema") {
		t.Fatal("the help screen does not name schema")
	}
	code, out, errOut := runBoth(t, []string{"help", "--json"})
	if code != exitOK {
		t.Fatalf("help --json exited %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "schema") || strings.Contains(out, `"clispec"`) {
		t.Fatalf("help --json was not the help screen:\n%s", out)
	}
	noEnvelope(t, errOut)
}

// review is the named form of a bare gauntlet. It is not its own flag list:
// a flag the default run accepts has to parse here too, and the word is not
// an unknown command the way `run` is.
func TestReviewParsesAsTheNamedRun(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	opts, err := parseFlags([]string{"review", "--list"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "review" || !opts.list {
		t.Fatalf("command %q list %v", opts.command, opts.list)
	}
}

func TestOutputSelectionAndEnvelopes(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())

	code, out, errOut := runBoth(t, []string{"version", "-o", "json"})
	if code != exitOK {
		t.Fatalf("version -o json exited %d\n%s", code, errOut)
	}
	var doc struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("version -o json: %v\n%s", err, out)
	}
	if doc.Name != "gauntlet" || doc.Version != version {
		t.Fatalf("version document %+v, want gauntlet %s", doc, version)
	}
	noEnvelope(t, errOut)

	code, out, errOut = runBoth(t, []string{"version", "-o", "text"})
	if code != exitOK || out != "gauntlet "+version+"\n" {
		t.Fatalf("version -o text: exit %d stdout %q stderr %q", code, out, errOut)
	}
	noEnvelope(t, errOut)

	code, out, _ = runBoth(t, []string{"version"})
	if code != exitOK || out != "gauntlet "+version+"\n" {
		t.Fatalf("version auto: exit %d stdout %q", code, out)
	}

	// --json=false is a boolean value, not a request for the envelope.
	code, out, errOut = runBoth(t, []string{"--json=false", "version"})
	if code != exitOK || out != "gauntlet "+version+"\n" {
		t.Fatalf("--json=false version: exit %d stdout %q stderr %q", code, out, errOut)
	}
	noEnvelope(t, errOut)

	code, _, errOut = runBoth(t, []string{"--output", "foo", "version"})
	if code != exitUsage {
		t.Fatalf("--output foo exited %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "auto, text, or json") {
		t.Fatalf("stderr is %q", errOut)
	}
	noEnvelope(t, errOut)

	// JSON was asked for, so the same refusal still carries the envelope,
	// and the envelope is the last line rather than a line inside the help.
	code, _, errOut = runBoth(t, []string{"--output", "foo", "--json", "version"})
	if code != exitUsage {
		t.Fatalf("--output foo --json exited %d\n%s", code, errOut)
	}
	if kind := errorEnvelope(t, errOut)["kind"]; kind != "usage" {
		t.Fatalf("kind %v\n%s", kind, errOut)
	}

	code, _, errOut = runBoth(t, []string{"--json", "--output", "text", "version"})
	if code != exitUsage || !strings.Contains(errOut, "conflicts") {
		t.Fatalf("--json --output text: exit %d\n%s", code, errOut)
	}
	if kind := errorEnvelope(t, errOut)["kind"]; kind != "usage" {
		t.Fatalf("kind %v\n%s", kind, errOut)
	}

	code, _, errOut = runBoth(t, []string{"--json", "--output", "auto", "version"})
	if code != exitUsage || !strings.Contains(errOut, "conflicts") {
		t.Fatalf("--json --output auto: exit %d\n%s", code, errOut)
	}
	errorEnvelope(t, errOut)

	// The flag parser accepts every strconv.ParseBool spelling. A later parse
	// failure still owes the envelope, because the request was visible.
	code, _, errOut = runBoth(t, []string{"--json=1", "--not-a-flag"})
	if code != exitUsage {
		t.Fatalf("--json=1 --not-a-flag exited %d\n%s", code, errOut)
	}
	if kind := errorEnvelope(t, errOut)["kind"]; kind != "usage" {
		t.Fatalf("--json=1 kind %v\n%s", kind, errOut)
	}

	// --json=0 is a false value, and --json after --log is the log path.
	code, _, errOut = runBoth(t, []string{"--json=0", "--not-a-flag"})
	if code != exitUsage {
		t.Fatalf("--json=0 --not-a-flag exited %d\n%s", code, errOut)
	}
	noEnvelope(t, errOut)
	code, _, errOut = runBoth(t, []string{"--log", "--json", "--not-a-flag"})
	if code != exitUsage {
		t.Fatalf("--log --json --not-a-flag exited %d\n%s", code, errOut)
	}
	noEnvelope(t, errOut)

	// The last --json wins. A later false clears an earlier request, and a
	// later --output json still asks when --json=false only clears the alias.
	code, _, errOut = runBoth(t, []string{"--json", "--json=false", "--not-a-flag"})
	if code != exitUsage {
		t.Fatalf("--json --json=false exited %d\n%s", code, errOut)
	}
	noEnvelope(t, errOut)
	code, _, errOut = runBoth(t, []string{"--output=json", "--json=false", "--not-a-flag"})
	if code != exitUsage {
		t.Fatalf("--output=json --json=false exited %d\n%s", code, errOut)
	}
	if kind := errorEnvelope(t, errOut)["kind"]; kind != "usage" {
		t.Fatalf("--output=json --json=false kind %v\n%s", kind, errOut)
	}

	// -json is -j with the value son. A single-letter flag that takes a
	// value is split before the flag parser, so this is not the json flag.
	// One dash with an equals sign is that flag: -json=1.
	code, _, errOut = runBoth(t, []string{"-json", "--not-a-flag"})
	if code != exitUsage || !strings.Contains(errOut, `invalid value "son"`) {
		t.Fatalf("-json exited %d\n%s", code, errOut)
	}
	noEnvelope(t, errOut)
	code, _, errOut = runBoth(t, []string{"-json=1", "--not-a-flag"})
	if code != exitUsage {
		t.Fatalf("-json=1 exited %d\n%s", code, errOut)
	}
	if kind := errorEnvelope(t, errOut)["kind"]; kind != "usage" {
		t.Fatalf("-json=1 kind %v\n%s", kind, errOut)
	}
	code, _, errOut = runBoth(t, []string{"-output=json", "--not-a-flag"})
	if code != exitUsage {
		t.Fatalf("-output=json exited %d\n%s", code, errOut)
	}
	errorEnvelope(t, errOut)
	code, _, errOut = runBoth(t, []string{"-ojson", "--not-a-flag"})
	if code != exitUsage {
		t.Fatalf("-ojson exited %d\n%s", code, errOut)
	}
	errorEnvelope(t, errOut)
	code, _, errOut = runBoth(t, []string{"-json=false", "--not-a-flag"})
	if code != exitUsage {
		t.Fatalf("-json=false exited %d\n%s", code, errOut)
	}
	noEnvelope(t, errOut)
}

func TestRestoreRejectsAPageOrAProjection(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	code, _, errOut := runBoth(t, []string{"runs", "--json", "--fields", "run_id", "--restore", "20260102T150405Z-1"})
	if code != exitUsage || !strings.Contains(errOut, "--fields does not apply with --restore") {
		t.Fatalf("--fields --restore: exit %d\n%s", code, errOut)
	}
	errorEnvelope(t, errOut)
	code, _, errOut = runBoth(t, []string{"runs", "--json", "--offset", "1", "--restore", "20260102T150405Z-1"})
	if code != exitUsage || !strings.Contains(errOut, "--offset does not apply with --restore") {
		t.Fatalf("--offset --restore: exit %d\n%s", code, errOut)
	}
	errorEnvelope(t, errOut)
}

func TestLogOpenFailureKeepsItsMessageInTheEnvelope(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a directory mode 0555")
	}
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	code, _, errOut := runBoth(t, []string{"version", "--output", "json", "--log", filepath.Join(dir, "run.log")})
	if code != exitUsage {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	msg, _ := errorEnvelope(t, errOut)["message"].(string)
	if !strings.Contains(msg, "cannot write log file") || strings.Contains(msg, "usage error") {
		t.Fatalf("envelope message %q\n%s", msg, errOut)
	}
}

func TestRunsJSONPagesAndSelectsFields(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	older := finishRun(t, base, 0)
	newer := finishRun(t, base.Add(time.Hour), 0)

	code, out, errOut := runBoth(t, []string{"runs", "--json", "--limit", "1"})
	if code != exitOK {
		t.Fatalf("first page exited %d\n%s", code, errOut)
	}
	noEnvelope(t, errOut)
	page := decodeRunsPage(t, out)
	if page.Total != 2 || page.Offset != 0 || page.Limit != 1 || !page.Truncated {
		t.Fatalf("first page = %+v", page)
	}
	if page.Home == "" || page.Journals == "" || page.Pruned == nil || page.History == nil {
		t.Fatalf("page dropped a listing key: %+v", page)
	}
	if len(page.Runs) != 1 || page.Runs[0]["run_id"] != newer || !reflect.DeepEqual(page.Runs, page.Items) {
		t.Fatalf("first page rows = %#v items %#v, want %s", page.Runs, page.Items, newer)
	}

	code, out, errOut = runBoth(t, []string{"runs", "--json", "--limit", "1", "--offset", "1"})
	if code != exitOK {
		t.Fatalf("second page exited %d\n%s", code, errOut)
	}
	page = decodeRunsPage(t, out)
	if page.Total != 2 || page.Offset != 1 || page.Truncated || len(page.Runs) != 1 || page.Runs[0]["run_id"] != older {
		t.Fatalf("second page = %+v, want %s", page, older)
	}

	code, out, errOut = runBoth(t, []string{"runs", "--json", "--offset", "5"})
	if code != exitOK {
		t.Fatalf("offset past the end exited %d\n%s", code, errOut)
	}
	page = decodeRunsPage(t, out)
	if page.Truncated || len(page.Runs) != 0 || page.Total != 2 {
		t.Fatalf("empty page = %+v", page)
	}

	code, out, _ = runBoth(t, []string{"runs", "--offset", "5"})
	if code != exitOK || !strings.Contains(out, "No runs in this page.") {
		t.Fatalf("text page: exit %d\n%s", code, out)
	}

	code, out, errOut = runBoth(t, []string{"runs", "--json", "--fields", "run_id", "--limit", "1"})
	if code != exitOK {
		t.Fatalf("--fields exited %d\n%s", code, errOut)
	}
	page = decodeRunsPage(t, out)
	if len(page.Runs) != 1 || len(page.Runs[0]) != 1 || page.Runs[0]["run_id"] != newer || !reflect.DeepEqual(page.Runs, page.Items) {
		t.Fatalf("projected page = %#v", page.Runs)
	}

	code, _, errOut = runBoth(t, []string{"runs", "--json", "--fields", "nope"})
	if code != exitUsage {
		t.Fatalf("--fields nope exited %d\n%s", code, errOut)
	}
	if kind := errorEnvelope(t, errOut)["kind"]; kind != "usage" {
		t.Fatalf("kind %v\n%s", kind, errOut)
	}

	code, _, errOut = runBoth(t, []string{"runs", "--fields", "run_id"})
	if code != exitUsage || !strings.Contains(errOut, "--json") {
		t.Fatalf("--fields without JSON: exit %d\n%s", code, errOut)
	}
	noEnvelope(t, errOut)
}

func TestPickJSONRequiresATTY(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	code, _, errOut := runBoth(t, []string{"pick", "-o", "json"})
	if code != exitUsage {
		t.Fatalf("pick -o json exited %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "pick needs a terminal on stdin and stdout") {
		t.Fatalf("the prose refusal is gone:\n%s", errOut)
	}
	env := errorEnvelope(t, errOut)
	if env["kind"] != "tty_required" {
		t.Fatalf("kind %v, want tty_required\n%s", env["kind"], errOut)
	}
}

func TestTUIConflictsWithJSONBeforeTheTerminalCheck(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	code, _, errOut := runBoth(t, []string{"--tui", "--json"})
	if code != exitUsage || !strings.Contains(errOut, "--tui conflicts with --output json") {
		t.Fatalf("--tui --json: exit %d\n%s", code, errOut)
	}
	if strings.Contains(errOut, "plain log output") {
		t.Fatal("the terminal refusal ran instead of the format conflict")
	}
	if kind := errorEnvelope(t, errOut)["kind"]; kind != "usage" {
		t.Fatalf("kind %v, want usage", kind)
	}

	code, _, errOut = runBoth(t, []string{"--tui"})
	if code != exitUsage {
		t.Fatalf("--tui exited %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "stdin and stdout") || !strings.Contains(errOut, "plain log output") {
		t.Fatalf("the terminal refusal changed:\n%s", errOut)
	}
	noEnvelope(t, errOut)
}

// Text and JSON doctor share an exit. The JSON document is a second probe,
// and a drift in the usable-agent rule or the state-root rule would let one
// of them report a working machine the other refuses.
func TestDoctorJSONAgreesOnExit(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	codeText, _, _ := runBoth(t, []string{"doctor"})
	codeJSON, out, errOut := runBoth(t, []string{"doctor", "--json"})
	if codeText != codeJSON {
		t.Fatalf("doctor exit %d, doctor --json exit %d", codeText, codeJSON)
	}
	var doc struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, out)
	}
	if doc.OK != (codeJSON == exitOK) {
		t.Fatalf("ok is %v with exit %d", doc.OK, codeJSON)
	}
	if codeJSON == exitOK {
		noEnvelope(t, errOut)
	} else if kind := errorEnvelope(t, errOut)["kind"]; kind != "failed" {
		t.Fatalf("kind %v, want failed", kind)
	}

	// run() refuses a root it cannot use before doctor, so the unusable
	// case calls the two probes directly. A mode-0500 directory is not
	// writable, which is the failure both exits have to report.
	if os.Geteuid() == 0 {
		return
	}
	state := t.TempDir()
	if err := os.Chmod(state, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(state, 0o700) })
	t.Setenv("GAUNTLET_HOME", state)
	t.Cleanup(resetExit)
	var text, js bytes.Buffer
	textCode := doctor(&text, report.Palette{}, nil, 80)
	jsonCode := doctorAt(&js, report.Palette{}, nil, 80, true)
	if textCode != exitFail || jsonCode != exitFail {
		t.Fatalf("unusable root: text %d, json %d, want both %d", textCode, jsonCode, exitFail)
	}
	var bad struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(js.Bytes(), &bad); err != nil {
		t.Fatal(err)
	}
	if bad.OK {
		t.Fatal("doctor --json reported ok on a root that cannot be written")
	}
}

func runBoth(t *testing.T, argv []string) (code int, stdout, stderr string) {
	t.Helper()
	code, stderr = captureFD(t, &os.Stderr, func() int {
		var c int
		c, stdout = captureFD(t, &os.Stdout, func() int { return run(argv) })
		return c
	})
	return code, stdout, stderr
}

func errorEnvelope(t *testing.T, stderr string) map[string]any {
	t.Helper()
	text := strings.TrimRight(stderr, "\n")
	if text == "" {
		t.Fatal("stderr is empty, want an error envelope")
	}
	line := text[strings.LastIndex(text, "\n")+1:]
	var doc map[string]any
	if err := json.Unmarshal([]byte(line), &doc); err != nil {
		t.Fatalf("last stderr line is not JSON: %s\n%s", line, stderr)
	}
	errObj, _ := doc["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("last stderr line has no error object: %s", line)
	}
	return errObj
}

func noEnvelope(t *testing.T, stderr string) {
	t.Helper()
	text := strings.TrimRight(stderr, "\n")
	if text == "" {
		return
	}
	line := text[strings.LastIndex(text, "\n")+1:]
	var doc map[string]any
	if json.Unmarshal([]byte(line), &doc) == nil {
		if _, ok := doc["error"]; ok {
			t.Fatalf("wrote an error envelope: %s", line)
		}
	}
}

type runsPage struct {
	Home      string           `json:"home"`
	Journals  string           `json:"journals"`
	Runs      []map[string]any `json:"runs"`
	Items     []map[string]any `json:"items"`
	Pruned    []string         `json:"pruned"`
	History   map[string]any   `json:"history"`
	Total     int              `json:"total"`
	Offset    int              `json:"offset"`
	Limit     int              `json:"limit"`
	Truncated bool             `json:"truncated"`
}

func decodeRunsPage(t *testing.T, out string) runsPage {
	t.Helper()
	var page runsPage
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("runs JSON: %v\n%s", err, out)
	}
	return page
}

func checkClispec(t *testing.T, raw []byte) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["clispec"] != "0.3" {
		t.Errorf("clispec = %v, want 0.3", doc["clispec"])
	}
	for _, key := range []string{"name", "version", "description", "output", "global_args", "commands", "errors"} {
		if doc[key] == nil || doc[key] == "" {
			t.Errorf("missing %s", key)
		}
	}
	if doc["name"] != "gauntlet" {
		t.Errorf("name = %v", doc["name"])
	}
	if _, ok := doc["subcommands"]; ok {
		t.Error("the document has a subcommands key; commands are a flat list")
	}
	if _, ok := doc["command_layout"]; ok {
		t.Error("the document has command_layout; commands are a flat list")
	}
	output := asMap(t, doc["output"], "output")
	if output["tty"] != "text" || output["piped"] != "text" {
		t.Errorf("output = %#v, want tty and piped text", output)
	}
	status, _ := asMap(t, doc["extensions"], "extensions")["clispec_status"].(string)
	if !strings.Contains(status, "candidate") || !strings.Contains(status, "not a conformance badge") {
		t.Errorf("clispec_status = %q", status)
	}

	globals := checkArgs(t, "global", doc["global_args"])
	if !globals["--output"] || !globals["--json"] {
		t.Errorf("global args = %#v", globals)
	}
	assertDefault(t, doc["global_args"], "--json", false)
	assertDefault(t, doc["global_args"], "--output", "auto")

	wantKinds := map[string]struct {
		code      int
		retryable bool
	}{
		"usage":                 {exitUsage, false},
		"tty_required":          {exitUsage, false},
		"confirmation_required": {exitUsage, false},
		"failed":                {exitFail, false},
		"internal":              {exitFail, false},
		"locked":                {exitLocked, true},
		"interrupted":           {exitInterrupted, false},
	}
	declared := map[string]bool{}
	for _, item := range asSlice(t, doc["errors"], "errors") {
		errObj := asMap(t, item, "error")
		kind, _ := errObj["kind"].(string)
		if kind == "" {
			t.Fatal("an error has no kind")
		}
		if declared[kind] {
			t.Errorf("error kind %s is declared twice", kind)
		}
		declared[kind] = true
		want, ok := wantKinds[kind]
		if !ok {
			t.Errorf("unexpected error kind %s", kind)
			continue
		}
		code, ok := asInt(errObj["exit_code"])
		if !ok || code != want.code {
			t.Errorf("%s exit_code = %v, want %d", kind, errObj["exit_code"], want.code)
		}
		if errObj["retryable"] != want.retryable {
			t.Errorf("%s retryable = %v, want %v", kind, errObj["retryable"], want.retryable)
		}
		delete(wantKinds, kind)
	}
	for kind := range wantKinds {
		t.Errorf("missing error kind %s", kind)
	}

	namePattern := regexp.MustCompile(`^[^\s]+( [^\s]+)*$`)
	fieldTypes := map[string]bool{
		"string": true, "integer": true, "number": true,
		"boolean": true, "object": true, "array": true,
	}
	commands := asSlice(t, doc["commands"], "commands")
	seen := map[string]map[string]any{}
	for _, item := range commands {
		cmd := asMap(t, item, "command")
		name, _ := cmd["name"].(string)
		if !namePattern.MatchString(name) {
			t.Errorf("command name %q is not a flat path", name)
		}
		if seen[name] != nil {
			t.Errorf("command %s is declared twice", name)
		}
		seen[name] = cmd
		if _, ok := cmd["subcommands"]; ok {
			t.Errorf("%s has a subcommands key", name)
		}
		if cmd["description"] == "" || cmd["description"] == nil {
			t.Errorf("%s has no description", name)
		}
		effects, _ := cmd["effects"].(string)
		switch effects {
		case "read_only", "idempotent", "non_idempotent":
		default:
			t.Errorf("%s effects = %v", name, cmd["effects"])
		}
		if effects != "non_idempotent" && cmd["idempotency_key_arg"] != nil {
			t.Errorf("%s declares an idempotency key on %s", name, effects)
		}
		local := checkArgs(t, name, cmd["args"])
		known := map[string]bool{}
		for arg := range globals {
			known[arg] = true
		}
		for arg := range local {
			known[arg] = true
		}
		for _, ref := range argRefs(t, name, cmd) {
			if !known[ref] {
				t.Errorf("%s references %s, which is not an argument", name, ref)
			}
		}
		if cmd["requires_tty"] == true && !stringListHas(cmd["errors"], "tty_required") {
			t.Errorf("%s requires a terminal and does not declare tty_required", name)
		}
		if bypass, _ := cmd["confirmation_bypass_arg"].(string); bypass != "" {
			if !stringListHas(cmd["errors"], "confirmation_required") {
				t.Errorf("%s has a confirmation bypass and does not declare confirmation_required", name)
			}
		}
		for _, errName := range stringList(cmd["errors"]) {
			if !declared[errName] {
				t.Errorf("%s lists error %s, which the document does not declare", name, errName)
			}
		}
		switch cmd["output_kind"] {
		case "data":
			switch cmd["cardinality"] {
			case "single", "bounded", "unbounded":
			default:
				t.Errorf("%s cardinality = %v", name, cmd["cardinality"])
			}
			_, hasFields := cmd["output_fields"]
			_, hasSchema := cmd["stdout_schema"]
			if !hasFields && !hasSchema {
				t.Errorf("%s is data and declares neither output_fields nor stdout_schema", name)
			}
			if hasSchema {
				if _, ok := cmd["stdout_schema"].(map[string]any); !ok {
					t.Errorf("%s stdout_schema is %T, want an object", name, cmd["stdout_schema"])
				}
			}
			if hasFields {
				checkOutputFields(t, name, cmd["output_fields"], fieldTypes)
			}
		case "stream":
			if cmd["stream_format"] != "ndjson" {
				t.Errorf("%s stream_format = %v, want ndjson", name, cmd["stream_format"])
			}
			for _, forbidden := range []string{"media_type", "cardinality", "pagination"} {
				if cmd[forbidden] != nil {
					t.Errorf("%s is a stream and declares %s", name, forbidden)
				}
			}
		case "opaque":
			media, _ := cmd["media_type"].(string)
			if media == "" {
				t.Errorf("%s is opaque and has no media_type", name)
			}
			for _, forbidden := range []string{"cardinality", "pagination", "fields_arg", "output_fields", "stdout_schema", "stream_format"} {
				if cmd[forbidden] != nil {
					t.Errorf("%s is opaque and declares %s", name, forbidden)
				}
			}
		default:
			t.Errorf("%s output_kind = %v", name, cmd["output_kind"])
		}
	}

	wantCommands := []string{"review", "pick", "doctor", "update", "runs", "show", "resume", "schema", "version", "help"}
	if len(seen) != len(wantCommands) {
		t.Errorf("commands = %d, want %d", len(seen), len(wantCommands))
	}
	for _, name := range wantCommands {
		if seen[name] == nil {
			t.Errorf("missing command %s", name)
		}
	}
	if seen["run"] != nil {
		t.Error("a run command would shadow the runs hint")
	}
	assertCommand(t, seen["review"], "non_idempotent", "stream")
	if seen["review"]["stream_format"] != "ndjson" || seen["review"]["idempotency_key_arg"] != nil || seen["review"]["confirmation_bypass_arg"] != nil {
		t.Errorf("review declaration drifted: idempotency or confirmation key is set, or the stream is not ndjson")
	}
	if ext := asMap(t, seen["review"]["extensions"], "review extensions"); ext["confirmation"] == nil || ext["retry"] == nil {
		t.Error("review does not record why it has no idempotency key and no confirmation bypass")
	}
	assertCommand(t, seen["pick"], "non_idempotent", "opaque")
	if seen["pick"]["requires_tty"] != true || seen["pick"]["media_type"] != "text/plain" {
		t.Errorf("pick = requires_tty %v media %v", seen["pick"]["requires_tty"], seen["pick"]["media_type"])
	}
	assertCommand(t, seen["doctor"], "read_only", "data")
	assertCommand(t, seen["update"], "idempotent", "data")
	assertCommand(t, seen["runs"], "non_idempotent", "data")
	if seen["runs"]["cardinality"] != "unbounded" || seen["runs"]["fields_arg"] != "--fields" {
		t.Errorf("runs cardinality %v fields %v", seen["runs"]["cardinality"], seen["runs"]["fields_arg"])
	}
	page := asMap(t, seen["runs"]["pagination"], "runs pagination")
	if page["style"] != "offset" || page["offset_arg"] != "--offset" || page["limit_arg"] != "--limit" {
		t.Errorf("runs pagination = %#v", page)
	}
	assertDefault(t, seen["runs"]["args"], "--offset", float64(0))
	assertCommand(t, seen["show"], "read_only", "stream")
	assertCommand(t, seen["resume"], "non_idempotent", "data")
	if seen["resume"]["cardinality"] != "bounded" {
		t.Errorf("resume cardinality = %v", seen["resume"]["cardinality"])
	}
	if schema, ok := seen["resume"]["stdout_schema"].(map[string]any); !ok || len(schema) != 0 {
		t.Errorf("resume stdout_schema = %#v, want {}", seen["resume"]["stdout_schema"])
	}
	assertCommand(t, seen["schema"], "read_only", "opaque")
	if seen["schema"]["media_type"] != "application/json" {
		t.Errorf("schema media_type = %v", seen["schema"]["media_type"])
	}
	assertCommand(t, seen["version"], "read_only", "data")
	assertCommand(t, seen["help"], "read_only", "opaque")
	if seen["help"]["media_type"] != "text/plain" {
		t.Errorf("help media_type = %v", seen["help"]["media_type"])
	}
}

func assertCommand(t *testing.T, cmd map[string]any, effects, kind string) {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing command")
	}
	if cmd["effects"] != effects || cmd["output_kind"] != kind {
		t.Errorf("%s effects %v output_kind %v, want %s %s", cmd["name"], cmd["effects"], cmd["output_kind"], effects, kind)
	}
}

func assertDefault(t *testing.T, args any, name string, want any) {
	t.Helper()
	for _, item := range asSlice(t, args, name) {
		arg := asMap(t, item, name)
		if arg["name"] != name {
			continue
		}
		if !reflect.DeepEqual(arg["default"], want) {
			t.Errorf("%s default = %#v, want %#v", name, arg["default"], want)
		}
		return
	}
	t.Errorf("argument %s is missing", name)
}

func checkArgs(t *testing.T, where string, v any) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	if v == nil {
		return names
	}
	for _, item := range asSlice(t, v, where+" args") {
		arg := asMap(t, item, where+" arg")
		name, _ := arg["name"].(string)
		typ, _ := arg["type"].(string)
		if name == "" || typ == "" {
			t.Errorf("%s arg has name %q type %q", where, name, typ)
			continue
		}
		if names[name] {
			t.Errorf("%s declares %s twice", where, name)
		}
		names[name] = true
	}
	return names
}

func argRefs(t *testing.T, name string, cmd map[string]any) []string {
	t.Helper()
	var refs []string
	for _, key := range []string{"fields_arg", "idempotency_key_arg", "confirmation_bypass_arg"} {
		if ref, _ := cmd[key].(string); ref != "" {
			refs = append(refs, ref)
		}
	}
	if cmd["pagination"] == nil {
		return refs
	}
	page := asMap(t, cmd["pagination"], name+" pagination")
	for _, key := range []string{"offset_arg", "limit_arg", "cursor_arg"} {
		if ref, _ := page[key].(string); ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func checkOutputFields(t *testing.T, name string, v any, types map[string]bool) {
	t.Helper()
	seen := map[string]bool{}
	for _, item := range asSlice(t, v, name+" output_fields") {
		field := asMap(t, item, name+" field")
		fieldName, _ := field["name"].(string)
		typ, _ := field["type"].(string)
		if fieldName == "" || !types[typ] {
			t.Errorf("%s field %q has type %q", name, fieldName, typ)
		}
		if seen[fieldName] {
			t.Errorf("%s field %s is declared twice", name, fieldName)
		}
		seen[fieldName] = true
		if n, ok := field["nullable"]; ok && n != true {
			t.Errorf("%s field %s nullable = %v", name, fieldName, n)
		}
		if typ == "array" {
			items, ok := field["items"].(map[string]any)
			if !ok {
				t.Errorf("%s field %s items = %#v", name, fieldName, field["items"])
				continue
			}
			itemType, _ := items["type"].(string)
			if !types[itemType] {
				t.Errorf("%s field %s items type %q", name, fieldName, itemType)
			}
		}
	}
}

func stringList(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

func stringListHas(v any, want string) bool {
	return slices.Contains(stringList(v), want)
}

func asMap(t *testing.T, v any, what string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want an object", what, v)
	}
	return m
}

func asSlice(t *testing.T, v any, what string) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("%s is %T, want an array", what, v)
	}
	return s
}

func asInt(v any) (int, bool) {
	n, ok := v.(float64)
	if !ok || float64(int(n)) != n {
		return 0, false
	}
	return int(n), true
}
