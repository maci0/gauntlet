// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package sbom

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDocumentIsCycloneDX(t *testing.T) {
	doc := New("gauntlet", "github.com/maci0/gauntlet", "1.2.3", []Module{
		{Path: "github.com/rivo/uniseg", Version: "v0.4.7", Sum: "h1:zr9cD9j/aJFAoFtrx8XhdrLtwZ0ZUQRzYpA="},
		{Path: "github.com/aymanbagabas/go-osc52/v2", Version: "v2.0.1"},
		{Path: "golang.org/x/text", Version: "v0.42.0", Sum: "h1:a94Extn1e/a2Kg8Za4dbXX7dQxdZ4heY="},
	})
	var got struct {
		BOMFormat    string `json:"bomFormat"`
		SpecVersion  string `json:"specVersion"`
		SerialNumber string `json:"serialNumber"`
		Metadata     struct {
			Component Component `json:"component"`
		} `json:"metadata"`
		Components []Component `json:"components"`
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Error("the document must end with a newline")
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("the document is not valid JSON: %v\n%s", err, buf.String())
	}
	if got.BOMFormat != "CycloneDX" || got.SpecVersion != "1.5" {
		t.Errorf("document is %s %s, want CycloneDX 1.5", got.BOMFormat, got.SpecVersion)
	}
	if !strings.HasPrefix(got.SerialNumber, "urn:uuid:") {
		t.Errorf("serialNumber is %q, want a urn:uuid identifier", got.SerialNumber)
	}
	if got.Metadata.Component.Version != "1.2.3" || got.Metadata.Component.Type != "application" {
		t.Errorf("subject is %+v, want the application at 1.2.3", got.Metadata.Component)
	}
	if got.Metadata.Component.PURL != "pkg:golang/github.com/maci0/gauntlet@1.2.3" {
		t.Errorf("subject purl is %q, want the package URL of the main module", got.Metadata.Component.PURL)
	}
	if len(got.Metadata.Component.Licenses) != 1 ||
		got.Metadata.Component.Licenses[0].License.ID != subjectLicense {
		t.Errorf("subject licenses are %+v, want the release's own %s grant", got.Metadata.Component.Licenses, subjectLicense)
	}
	if len(got.Components) != 3 {
		t.Fatalf("document has %d components, want 3", len(got.Components))
	}
	major := got.Components[1]
	if major.Name != "go-osc52" || major.Group != "github.com/aymanbagabas" {
		t.Errorf("a /v2 module is named %q in %q; the major version belongs to the path, not the name", major.Name, major.Group)
	}
	if major.PURL != "pkg:golang/github.com/aymanbagabas/go-osc52/v2@v2.0.1" {
		t.Errorf("purl is %q, want the full module path", major.PURL)
	}
	if major.Properties != nil {
		t.Errorf("a module with no recorded hash carries %+v; an invented one would be a false claim", major.Properties)
	}
	first := got.Components[0]
	if first.Name != "uniseg" || first.Group != "github.com/rivo" {
		t.Errorf("component name/group is %q/%q, want uniseg/github.com/rivo", first.Name, first.Group)
	}
	if first.PURL != "pkg:golang/github.com/rivo/uniseg@v0.4.7" {
		t.Errorf("purl is %q, want the Go package URL of the module", first.PURL)
	}
	if first.BOMRef != first.PURL {
		t.Errorf("bom-ref is %q, want the purl %q", first.BOMRef, first.PURL)
	}
	if len(first.Properties) != 1 || first.Properties[0].Name != "go:go.sum" ||
		!strings.HasPrefix(first.Properties[0].Value, "h1:") {
		t.Errorf("component properties are %+v, want the go.sum hash", first.Properties)
	}
}

// A rebuilt release must produce the same bytes, or a diff between two
// inventories of the same version says nothing about what changed.
func TestDocumentIsReproducible(t *testing.T) {
	mods := []Module{{Path: "golang.org/x/sys", Version: "v0.48.0", Sum: "h1:h1NjLce9..."}}
	first, second := New("gauntlet", "github.com/maci0/gauntlet", "1.2.3", mods), New("gauntlet", "github.com/maci0/gauntlet", "1.2.3", mods)
	if first.SerialNumber != second.SerialNumber {
		t.Errorf("serial numbers differ across runs: %s and %s", first.SerialNumber, second.SerialNumber)
	}
	other := New("gauntlet", "github.com/maci0/gauntlet", "1.2.4", mods)
	if other.SerialNumber == first.SerialNumber {
		t.Error("a different version produced the same serial number")
	}
	bumped := New("gauntlet", "github.com/maci0/gauntlet", "1.2.3", []Module{{Path: "golang.org/x/sys", Version: "v0.49.0"}})
	if bumped.SerialNumber == first.SerialNumber {
		t.Error("a different module version produced the same serial number")
	}
}

// The release ships one inventory for every platform it builds, so a module
// that links into the darwin binary and not the Linux one is still part of
// what shipped.
func TestMergeUnionsPlatforms(t *testing.T) {
	linux := []Module{{Path: "golang.org/x/text", Version: "v0.42.0"}}
	darwin := []Module{{Path: "ncruces/go-strftime", Version: "v1.0.0"}, {Path: "golang.org/x/text", Version: "v0.42.0"}}
	got, err := Merge([][]Module{linux, darwin})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, m := range got {
		paths = append(paths, m.Path)
	}
	want := []string{"golang.org/x/text", "ncruces/go-strftime"}
	if !slices.Equal(paths, want) {
		t.Errorf("merged modules are %v, want %v sorted by path", paths, want)
	}
}

// Binaries from two different trees are not one release, and an inventory
// that quietly picked one version of the module would name a version no
// binary carries.
func TestMergeRejectsConflictingVersions(t *testing.T) {
	_, err := Merge([][]Module{
		{{Path: "golang.org/x/text", Version: "v0.42.0"}},
		{{Path: "golang.org/x/text", Version: "v0.43.0"}},
	})
	if err == nil {
		t.Fatal("merging two versions of one module succeeded; the inventory would name a version no binary has")
	}
}

// The inventory is read out of the binaries themselves, so the reader has
// to work on a real one. The running test binary is a Go binary with build
// info, and it is the only one this test can rely on being present.
func TestFromBinaryReadsBuildInfo(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mainPath, _, err := FromBinary(self)
	if err != nil {
		t.Fatal(err)
	}
	if mainPath != "github.com/maci0/gauntlet" {
		t.Errorf("main module is %q, want github.com/maci0/gauntlet", mainPath)
	}
}

func TestFromBinaryRejectsNonGoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("not a binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := FromBinary(path); err == nil {
		t.Fatal("reading a file that is not a Go binary must fail, not report an empty inventory")
	}
	if _, _, err := FromBinary(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("reading a missing file must fail")
	}
}
