// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/gitx"
)

// resolveDirs expands and validates the target directories.
//
// A pattern is expanded here as well as by the shell, so a quoted --dirs
// '~/src/*' works. Non-directories among a pattern's matches are skipped
// (a glob over a source tree hits files too), but a literal path that is not
// a directory is a usage error: the user named something specific.
func resolveDirs(opts *options) ([]string, error) {
	// Name the flag the user actually passed: a bad --dir value must not be
	// reported as a --dirs problem.
	label := "--dir"
	list := opts.dirs
	if len(list) == 0 {
		list = []string{opts.dir}
	} else {
		label = "--dirs"
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, entry := range list {
		expanded, err := gauntlethome.ExpandPath(entry)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		if expanded == "" {
			return nil, fmt.Errorf("%s is empty", label)
		}
		matches := []string{expanded}
		globbed := isGlob(expanded)
		if globbed {
			found, err := filepath.Glob(expanded)
			if err != nil {
				return nil, fmt.Errorf("%s: bad pattern %q: %w", label, entry, err)
			}
			if len(found) == 0 {
				return nil, fmt.Errorf("%s: %q matched nothing", label, entry)
			}
			matches = found
		}
		added := 0
		for _, m := range matches {
			abs, err := filepath.Abs(m)
			if err != nil {
				return nil, err
			}
			fi, err := os.Stat(abs)
			if err != nil {
				if globbed {
					continue
				}
				return nil, fmt.Errorf("%s: %s: %w", label, abs, err)
			}
			if !fi.IsDir() {
				if globbed {
					continue
				}
				return nil, fmt.Errorf("%s: not a directory: %s", label, abs)
			}
			added++
			identity := gitx.RealPath(abs)
			if seen[identity] {
				continue // one tree twice would just block on its own lock
			}
			seen[identity] = true
			out = append(out, abs)
		}
		if globbed && added == 0 {
			return nil, fmt.Errorf("%s: %q matched no directories", label, entry)
		}
	}
	return out, nil
}

// isGlob reports whether a path needs expansion. filepath.Match's
// metacharacters are the ones that matter here.
func isGlob(p string) bool { return strings.ContainsAny(p, "*?[") }
