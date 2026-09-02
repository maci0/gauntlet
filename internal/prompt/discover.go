// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package prompt

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maci0/gauntlet/internal/gitx"
)

// skipDirs are never walked when looking for project prompts: build output,
// dependency trees, and tool caches only ever hold copies.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	".next": true, "target": true, ".git": true, "__pycache__": true,
	".venv": true, "venv": true, ".tox": true, ".cache": true,
	".ruff_cache": true, ".pytest_cache": true, ".mypy_cache": true,
	".nox": true, ".hypothesis": true, ".eggs": true, "htmlcov": true,
	"bower_components": true, ".turbo": true, ".parcel-cache": true,
	".gradle": true, "Pods": true, ".terraform": true,
}

// Discover maps review name to prompt for one target tree.
//
// The bundled prompts come first (from the binary, or from promptDir when one
// is given), then any *-review.md found in the project tree, except files git
// ignores. A project-local prompt wins over a bundled one of the same name;
// among project duplicates the first found in the walk (depth first,
// directories and files sorted) wins, silently when the copies are
// byte-identical. Symlinks are never followed: prompts must be regular files
// inside the tree, so a link cannot pull out-of-tree content into a
// permission-bypassed AI run.
//
// Warnings are display text for the user, already sanitized.
func Discover(ctx context.Context, promptDir, projectRoot string) (Set, []string, error) {
	byName := map[string]Review{}
	var warnings []string

	if promptDir == "" {
		for _, name := range BundledNames() {
			byName[name] = Review{Name: name, Origin: Bundled}
		}
	} else {
		entries, err := os.ReadDir(promptDir)
		if err != nil {
			return Set{}, nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, "-review.md") {
				continue
			}
			full := filepath.Join(promptDir, name)
			fi, err := os.Lstat(full)
			// A symlink would fail every run at read time; skip it here.
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			stem := nfc(strings.TrimSuffix(name, ".md"))
			if sanitize(stem) != stem {
				warnings = append(warnings, "ignoring prompt with control characters in its name: "+sanitize(full))
				continue
			}
			byName[stem] = Review{Name: stem, Path: full, Origin: Dir}
		}
		if len(byName) == 0 {
			return Set{}, warnings, fmt.Errorf("no *-review.md files found in: %s", promptDir)
		}
	}

	candidates := walkProject(projectRoot, promptDir)
	ignored := (&gitx.Repo{Dir: projectRoot}).CheckIgnore(ctx, candidates)

	seen := map[string]string{} // name -> winning path
	for _, path := range candidates {
		if ignored[path] {
			continue
		}
		stem := nfc(strings.TrimSuffix(filepath.Base(path), ".md"))
		if sanitize(stem) != stem {
			warnings = append(warnings, "ignoring project prompt with control characters in its name: "+sanitize(path))
			continue
		}
		if prev, dup := seen[stem]; dup {
			// Byte-identical copies (vendored snapshots, staging dirs) are
			// harmless: first wins silently. Warn only when the ignored file
			// disagrees with the one in use, or cannot be compared.
			if !sameFile(prev, path) {
				warnings = append(warnings, fmt.Sprintf(
					"conflicting duplicate project prompt %q: using %s, ignoring %s",
					stem, sanitize(prev), sanitize(path)))
			}
			continue
		}
		if prev, shadows := byName[stem]; shadows && !sameContent(prev, path) {
			// A byte-identical copy (a vendored snapshot of the bundled set,
			// or this project's own prompt sources) changes nothing worth
			// reporting; only a divergent override is news.
			warnings = append(warnings, fmt.Sprintf("project prompt %s overrides the bundled one", sanitize(path)))
		}
		seen[stem] = path
		byName[stem] = Review{Name: stem, Path: path, Origin: Project}
	}

	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return Set{Names: names, byName: byName}, warnings, nil
}

// walkProject lists *-review.md files in the tree, skipping hidden and
// generated directories and anything inside promptDir.
func walkProject(root, promptDir string) []string {
	absPromptDir := ""
	if promptDir != "" {
		absPromptDir, _ = filepath.Abs(promptDir)
	}
	// One Abs for the whole walk, not one per directory: filepath.Abs reads
	// the working directory every call, and the trees under review can hold
	// tens of thousands of them. WalkDir yields root-joined paths, so a
	// cleaned root makes the prefix cut below exact.
	root = filepath.Clean(root)
	absRoot, _ := filepath.Abs(root)
	abspath := func(path string) string {
		return filepath.Join(absRoot, strings.TrimPrefix(path, root))
	}
	var found []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: nothing to discover in it
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if skipDirs[name] || strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			if absPromptDir != "" && abspath(path) == absPromptDir {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "-review.md") {
			return nil
		}
		// Lstat, not the DirEntry type alone: FIFOs and devices are not
		// prompts, and readNoFollow enforces this again at open time, where
		// it is not racy.
		fi, e := os.Lstat(path)
		if e != nil || !fi.Mode().IsRegular() {
			return nil
		}
		found = append(found, path)
		return nil
	})
	sort.Strings(found)
	return found
}

// sameContent reports whether a project file matches the review it shadows.
//
// The size check settles most pairs without reading anything, and what is
// read is capped at one body plus a sentinel byte. Both files come from the
// reviewed tree; discovery only runs here to decide whether a warning is
// worth printing, and must not buffer an arbitrary file to decide that.
func sameContent(prev Review, path string) bool {
	want, err := prev.Body()
	if err != nil {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	// Body strips a UTF-8 BOM, so a file that is the body plus that mark is
	// the same text. Any other size cannot match.
	switch fi.Size() {
	case int64(len(want)), int64(len(want) + len("\xef\xbb\xbf")):
	default:
		return false
	}
	got, ok := readBounded(path, fi.Size()+1)
	return ok && stripBOM(string(got)) == want
}

// sameFile reports whether two prompt copies are byte-identical.
//
// Equal sizes are the common case for real duplicates and are required for
// identical content anyway, so differing sizes answer without a read. A pair
// over maxBytes is refused unread: neither copy could ever load as a prompt,
// and reporting them as conflicting keeps two multi-gigabyte files out of
// memory.
func sameFile(a, b string) bool {
	sa, oka := fileSize(a)
	sb, okb := fileSize(b)
	if !oka || !okb || sa != sb || sa > maxBytes {
		return false
	}
	ab, oka := readBounded(a, sa+1)
	bb, okb := readBounded(b, sb+1)
	return oka && int64(len(ab)) == sa && int64(len(bb)) == sb && bytes.Equal(ab, bb)
}

func fileSize(path string) (int64, bool) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < 0 {
		return 0, false
	}
	return fi.Size(), true
}

// readBounded reads at most limit bytes of path. ok is false on an error, a
// non-regular file (symlink, FIFO, device), or when the file holds limit
// bytes or more, so a caller that stats size n and passes n+1 can tell a
// full read from truncation.
func readBounded(path string, limit int64) ([]byte, bool) {
	f, err := openNoFollow(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	got, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil || int64(len(got)) >= limit {
		return nil, false
	}
	return got, true
}
