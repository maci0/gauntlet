// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package sbom

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/maci0/gauntlet/internal/runx"
)

// licenseFileNames are the names a module ships its grant under, in the order
// they are read. A module that carries none of them has no grant to record.
var licenseFileNames = []string{"LICENSE", "LICENSE.txt", "LICENSE.md", "LICENCE", "COPYING"}

// LicenseFileNames returns the names a grant is read from, in order. The
// license gate on the pull request reads the same list rather than a list of
// its own: it runs long before the release does, and a module whose grant is
// filed under a name only the release knew about passed that gate and then
// stopped the tag with an inventory that could not name its license.
func LicenseFileNames() []string {
	return slices.Clone(licenseFileNames)
}

// goListTimeout bounds the `go list` that locates the module directories.
// It answers from the module cache the release build just filled; the bound
// is there so a wedged toolchain fails the inventory instead of hanging the
// release.
const goListTimeout = 2 * time.Minute

// goListMaxBytes caps what `go list` may print. One line per module path and
// directory, so a graph with thousands of modules stays far below the cap; a
// module path the toolchain echoes back arbitrarily long is a corrupt graph,
// and the inventory says so rather than building a map out of it.
// licenseFileMax is the same bound on a grant read whole: a LICENSE is a few
// tens of kilobytes, and a module shipping something else is not carrying a
// license this can classify. The wait on the pipes a grandchild of the
// toolchain still holds is runx.WaitGrace, the bound every other child gets.
const (
	goListMaxBytes = 8 << 20
	licenseFileMax = 1 << 20
)

// ResolveLicenses returns mods with License set to the SPDX identifier of
// each module's grant, read from the LICENSE file in the module directory the
// toolchain reports. A module whose grant is missing, or is not one of the
// identifiers in spdxID, comes back with License empty: the inventory then
// carries no license claim for it rather than a guessed one, and the caller
// names the modules that are missing so a release cannot pass unnoticed.
func ResolveLicenses(mods []Module) ([]Module, error) {
	if len(mods) == 0 {
		return mods, nil
	}
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(mods))
	for _, m := range mods {
		paths = append(paths, m.Path)
	}
	dirs, err := moduleDirs(root, paths)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(mods)
	for i := range out {
		dir := dirs[out[i].Path]
		if dir == "" {
			continue
		}
		body, ok := readLicenseFile(dir)
		if !ok {
			continue
		}
		out[i].License = spdxID(body)
	}
	return out, nil
}

// moduleRoot walks up from the working directory to the go.mod of the module
// the binaries were built from, the same way the release Makefile reaches the
// tree. A build started outside any module has no graph to ask, and asking
// anyway would resolve licenses against whatever module happened to be there.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("read the working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s: the module graph to read licenses from is not reachable", dir)
		}
		dir = parent
	}
}

// moduleDirs maps each module path to the directory its sources are in. The
// toolchain owns that mapping: the module cache is escaped and versioned, so
// a path spelled out here would be a guess about a layout the tool defines.
// A module in the graph that was never downloaded reports no directory, and
// the caller leaves its license empty.
//
// The child gets the same treatment every other subprocess in this tree gets,
// through runx: its own process group so the deadline kill takes any
// grandchild with it, a WaitDelay so a grandchild holding the output pipe
// cannot park this call past the kill, and a cap so a listing the toolchain
// would keep extending costs a reported failure instead of memory.
func moduleDirs(root string, paths []string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), goListTimeout)
	defer cancel()
	args := append([]string{"list", "-m", "-f", "{{.Path}}\t{{.Dir}}"}, paths...)
	// The toolchain is resolved the way every other subprocess in this tree
	// resolves one, through runx.LookPath over the absolute-only PATH, rather
	// than handed to exec.Command as a bare name. exec.Command looks a bare
	// name up in the ambient PATH of this process, so a release built by a
	// program launchd or systemd started, which is handed none, failed to
	// find go there even though AbsPATHEnv below would have given the child
	// the fallback list that does hold it. The error names the missing
	// toolchain instead of a bare exec.ErrNotFound.
	goBin := runx.LookPath("go")
	if goBin == "" {
		return nil, fmt.Errorf("go: not found on PATH")
	}
	cmd := exec.CommandContext(ctx, goBin, args...)
	cmd.Dir = root
	cmd.Env = runx.AbsPATHEnv()
	cmd.Stdin = nil
	out, errOut := runx.Bound(cmd, goListMaxBytes, runx.WaitGrace)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	if err := runx.Outcome(ctx, cmd.Run()); err != nil {
		if detail := strings.TrimSpace(errOut.String()); detail != "" {
			return nil, fmt.Errorf("go list -m: %w: %s", err, runx.FirstLine(detail))
		}
		return nil, fmt.Errorf("go list -m: %w", err)
	}
	if out.Hit {
		return nil, fmt.Errorf("go list -m: output exceeded %d bytes", goListMaxBytes)
	}
	dirs := make(map[string]string, len(paths))
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		path, dir, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			return nil, fmt.Errorf("go list -m: unexpected line %q", line)
		}
		dirs[path] = dir
	}
	return dirs, nil
}

// readLicenseFile returns the contents of the module's grant, and whether it
// shipped one at all. A missing file is the answer "no grant recorded", not a
// failure to read: the module may carry its terms in a README instead, and
// there is nothing this package can call a license from that.
//
// The read is capped because the answer is a substring match against a short
// table: a file past the cap cannot classify any better than its first
// licenseFileMax bytes, so reading the rest buys nothing and lets whatever the
// module ships decide how much memory the inventory uses.
func readLicenseFile(dir string) (string, bool) {
	for _, name := range licenseFileNames {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(f, licenseFileMax))
		f.Close()
		if err == nil {
			return string(b), true
		}
	}
	return "", false
}

// spdxID names the license a grant text is, as the SPDX identifier a scanner
// resolves it by. The markers are the clauses that identify the license
// rather than a project name in the header: a module that rewords its
// copyright line still resolves, and one whose text this table does not
// recognize returns "", which the inventory records as no license rather than
// as the nearest identifier it could think of.
func spdxID(body string) string {
	switch {
	case strings.Contains(body, "Apache License") && strings.Contains(body, "Version 2.0"):
		return "Apache-2.0"
	case strings.Contains(body, "MIT License"),
		strings.Contains(body, "Permission is hereby granted") && strings.Contains(body, "without restriction"):
		return "MIT"
	case strings.Contains(body, "Redistribution and use in source and binary forms"):
		if nonEndorsement.MatchString(body) {
			return "BSD-3-Clause"
		}
		return "BSD-2-Clause"
	case strings.Contains(body, "Permission to use, copy, modify, and/or distribute this software for any purpose"):
		// The ISC text is the 0BSD text plus the notice-retention clause.
		if strings.Contains(body, "provided that the above copyright notice") {
			return "ISC"
		}
		return "0BSD"
	default:
		return ""
	}
}

// nonEndorsement is the third BSD condition, the one that separates
// BSD-3-Clause from BSD-2-Clause. Projects word it with a singular or a
// plural name, so the marker is the endorsement clause, not the count of
// bullets.
var nonEndorsement = regexp.MustCompile(`(?i)neither the names? of`)
