// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DefinitionsOnDisk answers "did the copy I am checking carry the definitions
// it was supposed to", which the registry cannot answer. A restored tree whose
// agents.json never arrived still runs, on the built-in agents, with nothing
// anywhere saying a definition is gone, and the registry is read from the
// startup that loaded it rather than from the tree a restore is checking. The
// count is what two trees are compared on, so it has to come from the file.
func TestDefinitionsOnDiskCountsTheFileRatherThanTheRegistry(t *testing.T) {
	t.Cleanup(resetCustom(t))
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")

	// A file nothing has registered is still counted. Registering one first
	// would make this pass for the wrong reason, which is the confusion the
	// function exists to keep out of a restore check.
	body := `{"pione":{"argv":["pione","-p","{prompt}"]},"ptwo":{"argv":["ptwo","-p","{prompt}"]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DefinitionsOnDisk(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Errorf("DefinitionsOnDisk counted %d definitions in the file, want 2", got)
	}
	// The registry is not what was read: nothing in the file reached it, which
	// is what a restored tree does before its next run registers them.
	if _, ok := CustomDef("pione"); ok {
		t.Error("counting a file registered its definitions")
	}

	// A missing file is zero and not an error: it is what a fresh install
	// holds, and the ordinary answer has to look like the ordinary answer.
	got, err = DefinitionsOnDisk(filepath.Join(dir, "nothing-here.json"))
	if err != nil {
		t.Errorf("a missing definitions file is an error: %v", err)
	}
	if got != 0 {
		t.Errorf("a missing definitions file counted %d, want 0", got)
	}
}

// A copy that cannot be parsed is an error beside the count, never zero on its
// own. A tree whose definitions were lost and a tree whose definitions cannot
// be read need opposite responses: the first is a backup that did not carry
// them, the second is a backup to take again from somewhere that has them, and
// a count that reads zero for both sends an operator to the wrong one.
func TestDefinitionsOnDiskRefusesACorruptCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")

	for _, broken := range []string{
		`{`,
		`null`,
		`[]`,
		`{"a":1} trailing`,
	} {
		if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := DefinitionsOnDisk(path)
		if err == nil {
			t.Errorf("a definitions file of %q was counted as %d definitions", broken, got)
			continue
		}
		if !strings.Contains(err.Error(), path) {
			t.Errorf("error %q for %q does not name the file it refused", err, broken)
		}
	}
}

// A leading byte order mark is the one encoding difference between two files
// holding the same definitions, and the loader strips it, so the counter has
// to strip it too: a restore that reports fewer definitions after a round trip
// through a writer that added one is a false alarm about an archive that is
// fine.
func TestDefinitionsOnDiskIgnoresALeadingByteOrderMark(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(path, []byte("\xef\xbb\xbf"+`{"pione":{"argv":["pione","-p","{prompt}"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DefinitionsOnDisk(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("a file with a leading byte order mark counted %d definitions, want 1", got)
	}
}
