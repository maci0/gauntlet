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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/maci0/gauntlet/internal/sbom"
)

// Exit codes, the same convention docs/CLI.md states for the CLI: 2 for an
// invocation this command refuses to run, 1 for a run that failed.
const (
	exitFail  = 1
	exitUsage = 2
)

func main() {
	err := run(os.Args[1:])
	var refused usageError
	switch {
	case err == nil:
	case errors.As(err, &refused):
		fmt.Fprintln(os.Stderr, "sbom:", refused.err)
		fmt.Fprint(os.Stderr, refused.usage)
		os.Exit(exitUsage)
	default:
		fmt.Fprintln(os.Stderr, "sbom:", err)
		os.Exit(exitFail)
	}
}

// usageError is a mistake in the invocation rather than a failure of the run:
// a flag that does not exist, one that is required and missing, or nothing to
// inventory. It is what separates exit 2 from exit 1, so a make recipe can
// tell a typo from a release that could not be inventoried. The usage screen
// rides along so the reader does not have to run the command a second time to
// find the shape of it.
type usageError struct {
	err   error
	usage string
}

func (u usageError) Error() string { return u.err.Error() }
func (u usageError) Unwrap() error { return u.err }

func run(args []string) error {
	fs := flag.NewFlagSet("sbom", flag.ContinueOnError)
	out := fs.String("o", "", "write the document here")
	version := fs.String("version", "", "released version, recorded as the subject's version")
	// The flag package sends both its usage screen and its parse error to one
	// stream it picks for itself, which put -h on stderr behind an error and
	// exit 1. Each goes where it belongs here instead: help on stdout with no
	// error at all, the error and the usage explaining it on stderr.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, usageText(fs))
			return nil
		}
		return usageError{err, usageText(fs)}
	}
	binaries := fs.Args()
	switch {
	case *out == "":
		return refuse(fs, errors.New("no output path; pass -o FILE"))
	case *version == "":
		return refuse(fs, errors.New("no release version; pass -version VERSION"))
	case len(binaries) == 0:
		return refuse(fs, errors.New("no binaries; pass the built paths to inventory"))
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
	if err := checkInventory(licensed); err != nil {
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

// usageLead is the synopsis and example the flag package's own defaults cannot
// carry: a reader who mistyped a flag needs the shape of the command as well
// as its flags.
const usageLead = `usage: sbom -o FILE -version VERSION BINARY [BINARY...]

Write the CycloneDX inventory of the modules linked into each binary, read
from the binary's own build info rather than the module graph.

example: sbom -o dist/sbom.json -version 1.2.3 dist/gauntlet_1.2.3_linux_amd64

flags:
`

// usageText renders the usage screen. The flag list comes from the
// registered flags, so it cannot drift from what is accepted.
func usageText(fs *flag.FlagSet) string {
	var buf bytes.Buffer
	fmt.Fprint(&buf, usageLead)
	fs.SetOutput(&buf)
	fs.PrintDefaults()
	return buf.String()
}

// refuse returns the error main classifies as a usage error, carrying the
// usage screen that explains it. An error that does not say how to call the
// command properly leaves the reader to guess at the missing piece.
func refuse(fs *flag.FlagSet, err error) error {
	return usageError{err, usageText(fs)}
}

// checkInventory fails when a module in the inventory carries no resolved
// license or no go.sum hash, naming every one of them. The document is written
// only after this passes, so a release never ships a CycloneDX file that is
// silent about the terms or the provenance of something it links: `make
// artifacts` runs this command, and the tagged release that calls it, so an
// unresolved grant or an unhashed module fails the run rather than riding
// along in a document a consumer reads as complete.
//
// The hash is the other half of the same claim. Every other field is copied
// from the binary's own build info and so is as trustworthy as the binary,
// but a module reached through a local-path or directory replace carries no
// go.sum entry, and sbom.New then writes a component with no `go:go.sum`
// property at all: a component in the inventory that no scanner can check
// against anything. Nothing here says the module is untrustworthy, it says
// the inventory cannot account for it, which is the gap this refuses.
func checkInventory(mods []sbom.Module) error {
	var unlicensed, unhashed []string
	for _, m := range mods {
		if m.License == "" {
			unlicensed = append(unlicensed, m.Path+" "+m.Version)
		}
		if m.Sum == "" {
			unhashed = append(unhashed, m.Path+" "+m.Version)
		}
	}
	var problems []string
	if len(unlicensed) > 0 {
		problems = append(problems, fmt.Sprintf("no license resolved for %d of %d modules: %s",
			len(unlicensed), len(mods), strings.Join(unlicensed, ", ")))
	}
	if len(unhashed) > 0 {
		problems = append(problems, fmt.Sprintf("no go.sum hash recorded for %d of %d modules: %s",
			len(unhashed), len(mods), strings.Join(unhashed, ", ")))
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}
