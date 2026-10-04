// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package evidence

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// An actual source import is stronger evidence than a dependency declaration.
// Manifests never establish an implemented HTTP, model, cache, or UI subject.
var importSubjects = map[string]string{
	"pytest": "test_runner", "unittest": "test_runner",
	"node:test": "test_runner", "bun:test": "test_runner",
	"vitest": "test_runner", "@jest/globals": "test_runner", "mocha": "test_runner",
	"fastapi": "http", "flask": "http", "starlette": "http",
	"django.http": "http", "django.urls": "http",
	"express": "http", "fastify": "http", "hono": "http",
	"axum": "http", "actix_web": "http",
	"github.com/gin-gonic/gin": "http", "github.com/labstack/echo": "http",
	"github.com/gofiber/fiber": "http",
	"openai":                   "model", "anthropic": "model", "ollama": "model",
	"@anthropic-ai/sdk": "model", "@google/genai": "model",
	"google.genai": "model", "google.generativeai": "model", "llama_cpp": "model",
	"github.com/openai/openai-go": "model", "github.com/anthropics/anthropic-sdk-go": "model",
	"github.com/sashabaranov/go-openai": "model",
	"redis":                             "cache", "ioredis": "cache", "pymemcache": "cache", "memcache": "cache",
	"github.com/redis/go-redis": "cache", "github.com/gomodule/redigo": "cache",
	"github.com/bradfitz/gomemcache": "cache",
	"sqlalchemy":                     "sql", "sqlite3": "sql", "psycopg": "sql", "psycopg2": "sql",
	"better-sqlite3": "sql", "pg": "sql", "@prisma/client": "sql", "django.db": "sql",
	"rusqlite": "sql", "sqlx": "sql", "database/sql": "sql",
	"numpy": "numeric", "scipy": "numeric",
	"github.com/charmbracelet/bubbletea": "tui", "textual": "tui",
	"prompt_toolkit": "tui", "ratatui": "tui",
}

var moduleImport = regexp.MustCompile(`(?m)^\s*(?:import\s+(?:[\w{},*\s$]+\s+from\s+)?["']([^"']+)["']|(?:const|let|var)\s+[^=\n]+\s*=\s*(?:await\s+)?(?:require|@?import)\(\s*["']([^"']+)["']\s*\)|(?:use|extern\s+crate)\s+([a-zA-Z_]\w*))`)
var moduleVersion = regexp.MustCompile(`(?m)^(?:__version__\s*=|export\s+const\s+(?:VERSION|version)\s*=)\s*["'][^"'\r\n]+["']`)

func imported(s *signals, rel, module string) {
	for name, subject := range importSubjects {
		if module == name || strings.HasPrefix(module, name+".") || strings.HasPrefix(module, name+"/") {
			if subject == "test_runner" && testFixture(rel) {
				continue
			}
			s.mark[subject] = 1
		}
	}
}

func sourceImports(s *signals, rel string, head []byte) {
	ext := strings.ToLower(filepath.Ext(rel))
	if ext == ".go" {
		// A partial AST preserves complete imports and top-level declarations
		// before a cut-off body. Comments and strings never become declarations.
		file, _ := parser.ParseFile(token.NewFileSet(), "", head, parser.AllErrors)
		if file != nil {
			for _, spec := range file.Imports {
				if module, err := strconv.Unquote(spec.Path.Value); err == nil {
					imported(s, rel, module)
				}
			}
			if !isTestFile(filepath.Base(rel)) {
				publicRoot := file.Name.Name != "main" && filepath.Dir(rel) == "."
				for _, decl := range file.Decls {
					if fn, ok := decl.(*ast.FuncDecl); publicRoot && ok && fn.Name.IsExported() {
						s.mark["library"] = 1
					}
					group, ok := decl.(*ast.GenDecl)
					if !ok {
						continue
					}
					for _, spec := range group.Specs {
						if kind, ok := spec.(*ast.TypeSpec); publicRoot && ok && kind.Name.IsExported() {
							s.mark["library"] = 1
						}
						value, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for i, name := range value.Names {
							if !strings.EqualFold(name.Name, "version") || i >= len(value.Values) {
								continue
							}
							if literal, ok := value.Values[i].(*ast.BasicLit); ok && literal.Kind == token.STRING {
								if text, err := strconv.Unquote(literal.Value); err == nil && text != "" {
									s.mark["release"] = 1
								}
							}
						}
					}
				}
			}
		}
		return
	}
	if ext != ".py" && ext != ".js" && ext != ".mjs" && ext != ".ts" && ext != ".tsx" && ext != ".jsx" && ext != ".rs" && ext != ".zig" {
		return
	}
	// These languages use bounded declaration recognition, not full parsers.
	// Skip standalone block comments and docstrings before considering imports.
	var code strings.Builder
	quoted := ""
	for line := range strings.SplitSeq(string(head), "\n") {
		trim := strings.TrimSpace(line)
		if quoted != "" {
			if strings.Contains(trim, quoted) {
				quoted = ""
			}
			continue
		}
		if strings.HasPrefix(trim, "/*") {
			if !strings.Contains(trim, "*/") {
				quoted = "*/"
			}
			continue
		}
		if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "#") {
			continue
		}
		for _, q := range []string{`"""`, `'''`, "`"} {
			if strings.Count(trim, q)%2 == 1 {
				quoted = q
				break
			}
		}
		if quoted != "" {
			continue
		}
		if ext == ".py" {
			if !isTestFile(filepath.Base(rel)) && moduleVersion.MatchString(line) {
				s.mark["release"] = 1
			}
			fields := strings.Fields(trim)
			if len(fields) >= 3 && fields[0] == "from" && fields[2] == "import" {
				imported(s, rel, fields[1])
			} else if len(fields) >= 2 && fields[0] == "import" {
				for module := range strings.SplitSeq(strings.TrimPrefix(trim, "import"), ",") {
					if parts := strings.Fields(module); len(parts) > 0 {
						imported(s, rel, parts[0])
					}
				}
			}
		} else {
			code.WriteString(line)
			code.WriteByte('\n')
		}
	}
	if !isTestFile(filepath.Base(rel)) && moduleVersion.MatchString(code.String()) {
		s.mark["release"] = 1
	}
	for _, match := range moduleImport.FindAllStringSubmatch(code.String(), -1) {
		if strings.HasPrefix(strings.TrimSpace(match[0]), "import type ") {
			continue
		}
		for _, module := range match[1:] {
			if module != "" {
				imported(s, rel, module)
			}
		}
	}
}

func packageTarget(raw json.RawMessage) bool {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text != ""
	}
	var entries map[string]json.RawMessage
	return json.Unmarshal(raw, &entries) == nil && len(entries) > 0
}

// packageMetadata decodes complete JSON heads and recognizes bounded TOML/INI
// section declarations. A truncated JSON manifest supplies no invented fields.
func packageMetadata(s *signals, rel string, head []byte) {
	name := strings.ToLower(filepath.Base(rel))
	switch name {
	case "package.json", "plugin.json":
		var pkg struct {
			Version              string
			Private              bool
			Types                string
			Typings              string
			Exports              json.RawMessage
			Files                []string
			Bin                  json.RawMessage
			Dependencies         map[string]string
			DevDependencies      map[string]string
			PeerDependencies     map[string]string
			OptionalDependencies map[string]string
			Scripts              map[string]string
		}
		if json.Unmarshal(head, &pkg) != nil {
			return
		}
		if !testFixture(rel) && testCommand(pkg.Scripts["test"]) {
			s.mark["test_runner"] = 1
		}
		if pkg.Version != "" {
			s.mark["release"] = 1
		}
		if !pkg.Private && (pkg.Types != "" || pkg.Typings != "" || packageTarget(pkg.Exports)) {
			s.mark["library"] = 1
		}
		if len(pkg.Files) > 0 {
			s.mark["package"] = 1
		}
		if packageTarget(pkg.Bin) {
			s.mark["cli"] = 1
		}
		if len(pkg.Dependencies)+len(pkg.DevDependencies)+len(pkg.PeerDependencies)+len(pkg.OptionalDependencies) > 0 {
			s.mark["dependency"] = 1
		}
	case "pyproject.toml", "cargo.toml", "setup.cfg":
		section := ""
		for line := range strings.SplitSeq(string(head), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				section = line
				if !testFixture(rel) && (section == "[tool.pytest.ini_options]" || section == "[tool:pytest]") {
					s.mark["test_runner"] = 1
				}
				if section == "[build-system]" || section == "[metadata]" {
					s.mark["package"] = 1
				}
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			if strings.TrimSpace(key) == "version" && strings.TrimSpace(value) != "" && (section == "[project]" || section == "[package]" || section == "[metadata]" || section == "[tool.poetry]") {
				s.mark["release"] = 1
			}
			dependencySection := section == "[dependencies]" || section == "[dev-dependencies]" || section == "[build-dependencies]" || strings.HasPrefix(section, "[dependencies.") ||
				strings.HasPrefix(section, "[target.") && strings.HasSuffix(section, ".dependencies]") || section == "[project.optional-dependencies]" || section == "[dependency-groups]" || section == "[tool.poetry.dependencies]" ||
				strings.HasPrefix(section, "[tool.poetry.group.") && strings.HasSuffix(section, ".dependencies]")
			if dependencySection || section == "[project]" && strings.TrimSpace(key) == "dependencies" || section == "[options]" && strings.TrimSpace(key) == "install_requires" {
				s.mark["dependency"] = 1
			}
		}
	}
}
