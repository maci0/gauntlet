// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildTimeout bounds the one build this test needs.
const buildTimeout = 5 * time.Minute

// inventoryBinary builds a binary to inventory. A test binary cannot stand
// in for one: this package links nothing third-party, so its own build info
// lists zero modules and every assertion over the component set passes
// vacuously. The CLI is what a release actually ships.
func inventoryBinary(t *testing.T, pkg, name string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the go tool is required to build the binary under inventory")
	}
	bin := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(t.Context(), buildTimeout)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, pkg)
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, out)
	}
	return bin
}

// The release passes the built binaries and the tagged version, and gets a
// document a scanner can read. The CLI stands in for a release binary: it is
// the program every release ships, and it is the one that links modules.
func TestRunWritesCycloneDXDocument(t *testing.T) {
	self := inventoryBinary(t, "./cmd/gauntlet", "gauntlet")
	out := filepath.Join(t.TempDir(), "sbom.json")
	if err := run([]string{"-o", out, "-version", "9.9.9", self}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		BOMFormat string `json:"bomFormat"`
		Metadata  struct {
			Component struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"component"`
		} `json:"metadata"`
		Components []struct {
			PURL     string `json:"purl"`
			Licenses []struct {
				License struct {
					ID string `json:"id"`
				} `json:"license"`
			} `json:"licenses"`
		} `json:"components"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the document is not valid JSON: %v\n%s", err, body)
	}
	if doc.BOMFormat != "CycloneDX" {
		t.Errorf("bomFormat is %q, want CycloneDX", doc.BOMFormat)
	}
	if doc.Metadata.Component.Name != "gauntlet" || doc.Metadata.Component.Version != "9.9.9" {
		t.Errorf("subject is %+v, want gauntlet 9.9.9", doc.Metadata.Component)
	}
	// An empty component set satisfies the loop below without running it
	// once, and a release that ships dist/sbom.json naming no dependency
	// would still be green. This tool exists to list them.
	if len(doc.Components) == 0 {
		t.Fatal("the document lists no components: a release would ship an sbom.json naming nothing")
	}
	for _, c := range doc.Components {
		if !strings.HasPrefix(c.PURL, "pkg:golang/") {
			t.Errorf("component purl %q is not a Go package URL", c.PURL)
		}
		if len(c.Licenses) == 0 {
			t.Errorf("component %q ships no resolved license; an inventory silent about a module's terms is what this tool exists to prevent", c.PURL)
		}
	}
}

func TestRunRejectsIncompleteInvocation(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-version", "1.0.0", self},
		{"-o", filepath.Join(t.TempDir(), "sbom.json"), self},
		{"-o", filepath.Join(t.TempDir(), "sbom.json"), "-version", "1.0.0"},
		{"-o", filepath.Join(t.TempDir(), "sbom.json"), "-version", "1.0.0", filepath.Join(t.TempDir(), "absent")},
	} {
		if err := run(args); err == nil {
			t.Errorf("run(%q) succeeded; a missing flag or a missing binary must fail", args)
		}
	}
}

// Two programs in one release would produce one inventory naming the
// union of their modules, so the mismatch is refused instead.
func TestRunRejectsMixedBinaries(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"-o", filepath.Join(t.TempDir(), "sbom.json"), "-version", "1.0.0", self, other})
	if err == nil || !strings.Contains(err.Error(), "build info") {
		t.Fatalf("run on a mixed set returned %v, want a build info failure", err)
	}
}
