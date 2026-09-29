// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The notice ruff's CPY rule holds a Python file to, applied to the language
// carrying the rest of the tree: AGENTS.md opens every .go file with these two
// lines, and nothing checked it, so a file added without them read the same as
// one that carried them. The year is a pattern, as ruff's notice-rgx is, so
// the next one does not turn the rule red on every file at once.
var goCopyrightLine = regexp.MustCompile(`^// Copyright \(C\) \d{4} Marcel W\. Wysocki$`)

const goSPDXLine = "// SPDX-License-Identifier: AGPL-3.0-or-later"

// Directories that are not this module's source: a review lane's checkout
// beside it, a scratch tree, or a build's output. A walk that read them would
// hold the convention to files no release ships, and one that ran from a lane
// would report on the lane's own tree.
var goHeaderSkippedDirs = map[string]bool{
	".gauntlet": true,
	".scratch":  true,
	"dist":      true,
}

func TestEveryGoFileCarriesTheLicenseHeader(t *testing.T) {
	root := filepath.Join("..", "..")
	var missing []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || goHeaderSkippedDirs[name]) {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if ok, err := carriesLicenseHeader(path); err != nil || !ok {
			missing = append(missing, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) == 0 {
		return
	}
	// The module's own files, relative to where the test runs, so the message
	// names something `go build ./...` would also name.
	for i, path := range missing {
		if rel, err := filepath.Rel(root, path); err == nil {
			missing[i] = rel
		}
	}
	t.Errorf("%d Go file(s) do not open with the two-line license header:\n%s",
		len(missing), strings.Join(missing, "\n"))
}

func carriesLicenseHeader(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	for want := range 2 {
		if !lines.Scan() {
			return false, nil
		}
		got := strings.TrimRight(lines.Text(), "\r")
		if want == 0 {
			if !goCopyrightLine.MatchString(got) {
				return false, nil
			}
			continue
		}
		if got != goSPDXLine {
			return false, nil
		}
	}
	return true, nil
}
