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
	doc := sbom.New(name, modulePath, *version, mods)
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
