// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Command sbom writes the CycloneDX inventory of the modules linked into
// the binaries it is given, so every release ships one a scanner can read.
//
//	sbom -version 1.2.3 -o dist/sbom.json dist/gauntlet_1.2.3_linux_amd64 ...
//
// The modules come from each binary's own build info, not from the current
// module graph, so the document describes what shipped.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maci0/gauntlet/internal/sbom"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sbom:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("sbom", flag.ContinueOnError)
	out := fs.String("o", "", "write the document here")
	version := fs.String("version", "", "released version, recorded as the subject's version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	binaries := fs.Args()
	switch {
	case *out == "":
		return fmt.Errorf("no output path; pass -o FILE")
	case *version == "":
		return fmt.Errorf("no release version; pass -version VERSION")
	case len(binaries) == 0:
		return fmt.Errorf("no binaries; pass the built paths to inventory")
	}

	perBinary := make([][]sbom.Module, 0, len(binaries))
	name, modulePath := "", ""
	for _, path := range binaries {
		mainPath, mods, err := sbom.FromBinary(path)
		if err != nil {
			return err
		}
		perBinary = append(perBinary, mods)
		if name == "" {
			name, modulePath = filepath.Base(mainPath), mainPath
		} else if mainPath != modulePath {
			return fmt.Errorf("%s is built from %s, not %s; one release is one program", path, mainPath, modulePath)
		}
	}

	mods, err := sbom.Merge(perBinary)
	if err != nil {
		return err
	}
	// The license of each module is read out of the module cache the build
	// that produced these binaries just filled, so the inventory records the
	// grant that shipped rather than one a consumer would have to rebuild to
	// find. A module whose grant is missing or unrecognized stops the run
	// here: an inventory that says nothing about a module's terms is what
	// this closes, and a document carrying one anyway closed nothing.
	licensed, err := sbom.ResolveLicenses(mods)
	if err != nil {
		return err
	}
	if err := checkLicenses(licensed); err != nil {
		return err
	}
	doc := sbom.New(name, modulePath, *version, licensed)
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		return err
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	fmt.Fprintf(os.Stderr, "sbom: %s lists %d modules\n", *out, len(doc.Components))
	return nil
}

// checkLicenses fails when a module in the inventory carries no resolved
// license, naming every one of them. The document is written only after this
// passes, so a release never ships a CycloneDX file that is silent about the
// terms of something it links: `make artifacts` runs this command, and the
// tagged release that calls it, so an unresolvable grant fails the run rather
// than riding along in a document a consumer reads as complete.
func checkLicenses(mods []sbom.Module) error {
	var unresolved []string
	for _, m := range mods {
		if m.License == "" {
			unresolved = append(unresolved, m.Path+" "+m.Version)
		}
	}
	if len(unresolved) == 0 {
		return nil
	}
	return fmt.Errorf("no license resolved for %d of %d modules: %s",
		len(unresolved), len(mods), strings.Join(unresolved, ", "))
}
