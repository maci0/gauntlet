// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package sbom

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The inventory records the license a module ships under, so the grant a
// consumer reads has to be the one its LICENSE file actually grants. A
// license named by proximity rather than by its terms is a false claim in a
// document whose whole purpose is traceability.
func TestSPDXIDNamesTheGrant(t *testing.T) {
	const mit = "MIT License\n\nPermission is hereby granted, free of charge, to any person obtaining a copy,\n" +
		"to deal in the Software without restriction.\n"
	const bsd3 = "Redistribution and use in source and binary forms, with or without modification,\n" +
		"are permitted provided that the following conditions are met:\n\n" +
		"3. Neither the name of the copyright holder nor the names of its contributors\n"
	const bsd2 = "Redistribution and use in source and binary forms, with or without modification,\n" +
		"are permitted provided that the following conditions are met:\n\n" +
		"1. Redistributions of source code must retain the above copyright notice.\n"
	const isc = "Permission to use, copy, modify, and/or distribute this software for any purpose\n" +
		"with or without fee is hereby granted, provided that the above copyright notice and\n" +
		"this permission notice appear in all copies.\n"
	const zeroBSD = "Permission to use, copy, modify, and/or distribute this software for any purpose\n" +
		"with or without fee is hereby granted.\n"
	for _, tc := range []struct{ name, body, want string }{
		{"mit", mit, "MIT"},
		{"mit reworded", "MIT License\n\nPermission is hereby granted, free of charge, to any person obtaining a copy of\nthis software, to deal in the Software without restriction.\n", "MIT"},
		{"bsd3", bsd3, "BSD-3-Clause"},
		{"bsd2", bsd2, "BSD-2-Clause"},
		{"isc", isc, "ISC"},
		{"0bsd", zeroBSD, "0BSD"},
		{"unknown", "This software is provided as-is, and the author makes no promises about it.\n", ""},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := spdxID(tc.body); got != tc.want {
				t.Errorf("spdxID = %q, want %q", got, tc.want)
			}
		})
	}
}

// A module whose grant is not resolved carries no license field: the
// document says what is known, and the missing field is the honest record of
// what is not.
func TestDocumentOmitsAnUnresolvedLicense(t *testing.T) {
	doc := New("gauntlet", "github.com/maci0/gauntlet", "1.2.3", []Module{
		{Path: "github.com/rivo/uniseg", Version: "v0.4.7", License: "MIT"},
		{Path: "github.com/only-owner/thing", Version: "v1.0.0"},
	})
	if len(doc.Components[0].Licenses) != 1 || doc.Components[0].Licenses[0].License.ID != "MIT" {
		t.Errorf("a resolved license is %+v, want one MIT entry", doc.Components[0].Licenses)
	}
	if doc.Components[1].Licenses != nil {
		t.Errorf("an unresolved license is %+v; the component must claim none", doc.Components[1].Licenses)
	}
}

// The licenses are read from the module cache of the tree the binaries were
// built from, so every module that links into a build has to resolve to a
// license this package names. A new dependency whose grant is missing or
// unrecognized fails here rather than shipping an inventory that says
// nothing about its terms. The module set is the one the CLI links under the
// tags this run is built with, which is what a release built the same way
// inventories.
func TestResolveLicensesCoversEveryLinkedModule(t *testing.T) {
	mods := linkedModules(t)
	if len(mods) == 0 {
		t.Fatal("the CLI links no third-party module, so nothing is covered")
	}
	licensed, err := ResolveLicenses(mods)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range licensed {
		if m.License == "" {
			t.Errorf("%s resolved no license; the shipped inventory would claim none", m.Path)
		}
	}
}

// linkedModules reports the third-party modules the CLI links under the build
// tags this test runs with, the way the dependency tests in cmd/gauntlet read
// the same graph. A test binary of this package links nothing third-party, so
// the CLI is what has to be asked.
func linkedModules(t *testing.T) []Module {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), goListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-deps",
		"-f", "{{with .Module}}{{if .Version}}{{.Path}}{{end}}{{end}}", "./cmd/gauntlet")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/gauntlet: %v", err)
	}
	var mods []Module
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if path := strings.TrimSpace(line); path != "" {
			mods = append(mods, Module{Path: path})
		}
	}
	return mods
}

// The first grant file found is the one the module ships as its license; a
// second file beside it (a bundled notice, a font license) is not read.
func TestResolveLicensesReadsTheGrantFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/README.md", []byte("nothing here"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/COPYING", []byte("MIT License\n\nPermission is hereby granted, free of charge,\nto deal in the Software without restriction.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, ok := readLicenseFile(dir)
	if !ok || spdxID(body) != "MIT" {
		t.Errorf("readLicenseFile = %q, %v; want the MIT text in COPYING", body, ok)
	}
	empty := t.TempDir()
	if _, ok := readLicenseFile(empty); ok {
		t.Error("a module with no grant file reported one")
	}
}
