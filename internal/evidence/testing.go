// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package evidence

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
)

// TDD needs a runner already present in the project, not a dependency name or
// an echo of a command. Recognize common invocations without executing them.
func testCommand(command string) bool {
	words := strings.Fields(strings.TrimLeft(command, " \t@-+"))
	for len(words) > 0 {
		if strings.Contains(words[0], "=") || words[0] == "env" || words[0] == "npx" || words[0] == "bunx" {
			words = words[1:]
			continue
		}
		if len(words) > 1 && (words[0] == "uv" || words[0] == "poetry" || words[0] == "pnpm" || words[0] == "npm") && (words[1] == "run" || words[1] == "exec") {
			words = words[2:]
			continue
		}
		break
	}
	if len(words) == 0 {
		return false
	}
	switch filepath.Base(words[0]) {
	case "pytest", "jest", "vitest", "mocha", "ava", "bats":
		return true
	case "go", "cargo", "bun", "deno":
		return len(words) > 1 && words[1] == "test"
	case "node":
		for _, word := range words[1:] {
			if word == "--test" {
				return true
			}
			if !strings.HasPrefix(word, "-") {
				return false
			}
		}
	case "python", "python3":
		return len(words) > 2 && words[1] == "-m" && (words[2] == "pytest" || words[2] == "unittest")
	}
	return false
}

func testConfig(name string) bool {
	switch name {
	case "makefile", "justfile", "pytest.ini", "tox.ini", "deno.json":
		return true
	}
	return false
}

func testFixture(rel string) bool {
	for part := range strings.SplitSeq(strings.ToLower(filepath.ToSlash(rel)), "/") {
		if part == "testdata" || part == "fixtures" || part == "examples" {
			return true
		}
	}
	return false
}

// testSignals shares the bounded head scan. Comments, quoted examples, test
// fixtures, and empty files cannot establish a production path. Config-only
// heads do not enter the other marker scans.
func testSignals(s *signals, rel string, head []byte) {
	if testFixture(rel) {
		return
	}
	name := strings.ToLower(filepath.Base(rel))
	if name == "deno.json" {
		var config struct{ Tasks map[string]string }
		if json.Unmarshal(head, &config) == nil && testCommand(config.Tasks["test"]) {
			s.mark["test_runner"] = 1
		}
		return
	}
	if testConfig(name) {
		for line := range strings.SplitSeq(string(head), "\n") {
			trim := strings.TrimSpace(line)
			if (name == "pytest.ini" || name == "tox.ini") && trim == "[pytest]" ||
				(name == "makefile" || name == "justfile") && len(line) > 0 && (line[0] == '\t' || line[0] == ' ') && testCommand(trim) {
				s.mark["test_runner"] = 1
			}
		}
		return
	}
	ext := strings.ToLower(filepath.Ext(rel))
	if !sourceExts[ext] || ext == ".sql" || ext == ".h" || ext == ".hpp" || strings.HasSuffix(name, ".d.ts") {
		return
	}
	code := domainNonCode.ReplaceAll(head, []byte(" "))
	if len(bytes.TrimSpace(code)) == 0 {
		return
	}
	test := isTestFile(name)
	base := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	test = test || strings.HasSuffix(base, "Test") || strings.HasSuffix(base, "Tests") || strings.HasSuffix(base, "Spec") || strings.HasSuffix(base, "Specs")
	for part := range strings.SplitSeq(strings.ToLower(filepath.ToSlash(rel)), "/") {
		switch part {
		case "test", "tests", "spec", "specs", "__tests__":
			test = true
		}
	}
	if test {
		s.mark["test_contract"] = 1
		return
	}
	s.mark["test_production"] = 1
	if ext == ".go" && s.goTests || ext == ".rs" && s.rustTests {
		s.mark["test_runner"] = 1
	}
}
