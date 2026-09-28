// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sbom renders the modules a built binary links as a CycloneDX 1.5
// document. The inventory is read out of the binary's own build info, so it
// describes what shipped rather than what the current tree would resolve to,
// and it is produced with the standard library alone: a supply-chain artifact
// must not add a supply-chain dependency.
package sbom

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// Module is one third-party module a binary links, as recorded in its build
// info. Sum is the go.sum hash of the module zip, empty when the build
// recorded none. License is the SPDX identifier of the module's grant, empty
// when no license was resolved for it.
type Module struct {
	Path    string
	Version string
	Sum     string
	License string
}

// Document is a CycloneDX 1.5 software bill of materials. Only the fields
// this inventory can fill truthfully are declared: a release knows the
// modules, their versions, their go.sum hashes, and the license each one
// ships, and claims nothing about the files a module contributed.
type Document struct {
	BOMFormat    string      `json:"bomFormat"`
	SpecVersion  string      `json:"specVersion"`
	SerialNumber string      `json:"serialNumber"`
	Version      int         `json:"version"`
	Metadata     Metadata    `json:"metadata"`
	Components   []Component `json:"components"`
}

// Metadata describes the subject the components belong to: the CLI, at the
// released version.
type Metadata struct {
	Component Component `json:"component"`
}

// Component is one entry of the inventory.
type Component struct {
	Type       string          `json:"type"`
	BOMRef     string          `json:"bom-ref"`
	Name       string          `json:"name"`
	Group      string          `json:"group,omitempty"`
	Version    string          `json:"version"`
	PURL       string          `json:"purl"`
	Licenses   []LicenseChoice `json:"licenses,omitempty"`
	Properties []Property      `json:"properties,omitempty"`
}

// LicenseChoice is the CycloneDX wrapper around one license entry. The
// licenses field is a list because a component may be offered under more than
// one grant, and a single-element list is how CycloneDX 1.5 spells one.
type LicenseChoice struct {
	License License `json:"license"`
}

// License is the SPDX identifier of a module's grant, which is what a
// scanner resolves a component by. A module whose grant was not resolved
// carries no license field at all rather than a name this package guessed.
type License struct {
	ID string `json:"id"`
}

// Property carries a value CycloneDX has no field for. The go.sum hash is
// one: h1 is a dirhash over the module zip, not one of the hash algorithms
// CycloneDX names, so writing it into hashes would be a claim a scanner
// cannot verify.
type Property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

const (
	// specVersion is the CycloneDX release this document conforms to.
	specVersion = "1.5"

	// sumProperty names the go.sum hash of a module's zip.
	sumProperty = "go:go.sum"
)

// FromBinary reads the modules a built binary links, together with the main
// module's path, from the build info the compiler stamped into it.
func FromBinary(path string) (mainPath string, mods []Module, err error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read build info from %s: %w", path, err)
	}
	if info.GoVersion == "" {
		return "", nil, fmt.Errorf("%s carries no Go build info", path)
	}
	for _, dep := range info.Deps {
		if dep == nil {
			continue
		}
		// A replaced module records the replacement under Replace and the
		// original path under Path, so the inventory has to follow it or it
		// names a version no binary contains.
		if dep.Replace != nil {
			dep = dep.Replace
		}
		mods = append(mods, Module{Path: dep.Path, Version: dep.Version, Sum: dep.Sum})
	}
	return info.Main.Path, mods, nil
}

// Merge unions the per-binary module lists of a release, sorted by module
// path. Two binaries of the same release differ only by platform, so a
// module that links into one but not the other is still part of what the
// release ships. One path recorded at two versions means the binaries were
// not built from one tree, and picking either would put a version in the
// inventory that no binary carries.
func Merge(perBinary [][]Module) ([]Module, error) {
	seen := make(map[string]Module)
	for _, mods := range perBinary {
		for _, m := range mods {
			if prev, ok := seen[m.Path]; ok {
				if prev.Version != m.Version {
					return nil, fmt.Errorf("%s is recorded at %s and %s; one release is one dependency graph", m.Path, prev.Version, m.Version)
				}
				continue
			}
			seen[m.Path] = m
		}
	}
	out := make([]Module, 0, len(seen))
	for _, m := range seen {
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b Module) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// New builds the document for a release: the CLI at the given version, and
// the given modules. modulePath is the main module the binaries were built
// from, so the subject carries the same package URL the components do. The
// serial number is derived from the contents, not drawn at random, so two
// runs over the same binaries write the same bytes and a rebuilt release
// produces a comparable inventory.
func New(name, modulePath, version string, mods []Module) *Document {
	subject := Component{
		Type:    "application",
		BOMRef:  purlOf(modulePath, version),
		Name:    name,
		Version: version,
		PURL:    purlOf(modulePath, version),
	}
	components := make([]Component, 0, len(mods))
	for _, m := range mods {
		c := Component{
			Type:    "library",
			BOMRef:  purl(m),
			Name:    componentName(m.Path),
			Group:   componentGroup(m.Path),
			Version: m.Version,
			PURL:    purl(m),
		}
		if m.Sum != "" {
			c.Properties = []Property{{Name: sumProperty, Value: m.Sum}}
		}
		if m.License != "" {
			c.Licenses = []LicenseChoice{{License: License{ID: m.License}}}
		}
		components = append(components, c)
	}
	doc := &Document{
		BOMFormat:   "CycloneDX",
		SpecVersion: specVersion,
		Version:     1,
		Metadata:    Metadata{Component: subject},
		Components:  components,
	}
	doc.SerialNumber = serialNumber(doc)
	return doc
}

// Write encodes the document as indented JSON with a trailing newline, the
// shape a file a release ships is read as.
func (d *Document) Write(w io.Writer) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the SBOM: %w", err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write the SBOM: %w", err)
	}
	return nil
}

// serialNumber is the CycloneDX document identifier, a UUID in the urn:uuid
// namespace. A release must be byte-reproducible, so the bytes are hashed
// rather than generated and the version and variant bits are set to say a
// name-based UUID.
func serialNumber(d *Document) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s@%s\n", d.Metadata.Component.Name, d.Metadata.Component.Version)
	for _, c := range d.Components {
		fmt.Fprintf(h, "%s\t%s\n", c.PURL, d.Metadata.Component.Name)
		for _, l := range c.Licenses {
			fmt.Fprintf(h, "\tlicense=%s\n", l.License.ID)
		}
		for _, p := range c.Properties {
			fmt.Fprintf(h, "\t%s=%s\n", p.Name, p.Value)
		}
	}
	sum := h.Sum(nil)
	var u [16]byte
	copy(u[:], sum[:16])
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	hexed := hex.EncodeToString(u[:])
	return "urn:uuid:" + hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32]
}

// purl is the package URL a scanner resolves a Go module by: the module
// path is the namespace and the name, and the version follows an @.
func purl(m Module) string {
	return purlOf(m.Path, m.Version)
}

func purlOf(path, version string) string {
	return "pkg:golang/" + path + "@" + version
}

// majorVersionSuffix is the /v2 a module path ends in for a major version
// above 1. The suffix is part of the path, not the name.
var majorVersionSuffix = regexp.MustCompile(`^v[2-9][0-9]*$`)

// componentName and componentGroup split a module path into the CycloneDX
// name and group, dropping a major-version suffix from both.
func componentName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 && majorVersionSuffix.MatchString(path[i+1:]) {
		return moduleName(path[:i])
	}
	return moduleName(path)
}

func componentGroup(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 && majorVersionSuffix.MatchString(path[i+1:]) {
		return moduleGroup(path[:i])
	}
	return moduleGroup(path)
}

func moduleName(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}

func moduleGroup(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return ""
	}
	return path[:i]
}
