// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maci0/gauntlet/internal/agent"
)

// agents.example.json is the template an operator copies into agents.json
// under the state root. No code path reads it and no analyzer judges it: the
// Go tree is vetted, scripts/ is ruffed and mypy-checked, the workflows are
// yamllinted, and this file, the one hand-maintained data file in the
// repository, had nothing. So every key in it is checked by whoever copies it,
// and a field the parser stopped accepting, a pinned list without its
// placeholder, or an argv with no {prompt} would ship as a template the loader
// refuses at startup.
//
// LoadCustomFile is the check, because it is the loader the CLI runs, and it
// is the strict one: an unknown field, a duplicate key, a missing {prompt}, a
// blank argument, and a model or effort list that omits its placeholder are all
// refusals rather than something dropped on the floor.
func TestAgentsExampleLoadsAsADefinitionFile(t *testing.T) {
	path := filepath.Join("..", "..", "agents.example.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the agent template: %v", err)
	}
	var names map[string]json.RawMessage
	if err := json.Unmarshal(data, &names); err != nil {
		t.Fatalf("agents.example.json is not a JSON object of agent definitions: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("agents.example.json defines no agent; a template that loads to an empty registry teaches nothing")
	}

	if err := agent.LoadCustomFile(path); err != nil {
		t.Fatalf("agents.example.json is refused by the loader: %v", err)
	}
	for name := range names {
		t.Cleanup(func() { agent.Unregister(name) })
		if _, ok := agent.CustomDef(name); !ok {
			t.Errorf("agents.example.json defines %q, which the loader did not install", name)
		}
	}
}
