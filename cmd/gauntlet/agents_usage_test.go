// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/agent"
)

// A definitions file is where a custom agent's transcript location is
// configured, and the reader it is handed expands neither $VAR nor anything
// else: a root written "$AGENT_HOME/sessions" used to reach the walk verbatim
// and the agent's live token counts stayed at zero with nothing reporting why.
// Every other operator path expands through ExpandPath, so a root naming an
// unset variable is refused at startup like the rest of them, rather than
// resolving to an empty segment that names a directory belonging to nobody.
func TestAgentsFileUsageRootExpandsEnvironmentVariables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_TEST_ROOT", home)
	t.Setenv("GAUNTLET_HOME", home)
	// The registry is process-wide, so a definition left in it is seen by
	// every later test in this binary, under whatever environment it runs
	// with.
	t.Cleanup(func() { agent.Unregister("rootagent") })
	def := `{"rootagent":{"argv":["rootagent","-p","{prompt}"],` +
		`"usage":{"roots":["$GAUNTLET_TEST_ROOT/sessions"]}}}`
	if err := os.WriteFile(filepath.Join(home, "agents.json"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := parseFlags([]string{"doctor"}); err != nil {
		t.Fatalf("a definitions file whose usage.roots names a set variable was refused: %v", err)
	}
}

// The refusal has to arrive while parsing rather than from the transcript walk
// minutes into a run: a root that resolved to an empty segment would otherwise
// be reported as a directory that does not exist, which names the wrong thing.
func TestAgentsFileUsageRootWithUnsetVariableIsRefusedAtStartup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_TEST_ROOT", "")
	t.Setenv("GAUNTLET_HOME", home)
	def := `{"rootagent":{"argv":["rootagent","-p","{prompt}"],` +
		`"usage":{"roots":["$GAUNTLET_TEST_ROOT/sessions"]}}}`
	if err := os.WriteFile(filepath.Join(home, "agents.json"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := parseFlags([]string{"doctor"})
	if err == nil {
		t.Fatal("a definitions file whose usage.roots names an unset variable started a run")
	}
	if !strings.Contains(err.Error(), "GAUNTLET_TEST_ROOT") {
		t.Errorf("error %q does not name the variable it refused", err)
	}
}
