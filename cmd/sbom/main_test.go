// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The release passes the built binaries and the tagged version, and gets a
// document a scanner can read. The running test binary stands in for a
// release binary: it is a Go binary with build info, and it is the one this
// test can rely on being present.
func TestRunWritesCycloneDXDocument(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
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
			PURL string `json:"purl"`
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
	for _, c := range doc.Components {
		if !strings.HasPrefix(c.PURL, "pkg:golang/") {
			t.Errorf("component purl %q is not a Go package URL", c.PURL)
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
