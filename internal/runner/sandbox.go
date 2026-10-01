// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/runx"
)

const sandboxExecArg = "--internal-sandbox-exec"

// Re-exec confines only the agent, never the scheduler. Lock the thread before
// applying Landlock: its policy is per-thread and exec must inherit that thread.
func init() {
	if len(os.Args) < 2 || os.Args[1] != sandboxExecArg {
		return
	}
	runtime.LockOSThread()
	var roots []string
	var err error
	if len(os.Args) < 5 {
		err = fmt.Errorf("invalid sandbox launch")
	} else {
		err = json.Unmarshal([]byte(os.Args[2]), &roots)
		if err == nil {
			err = enforceSandbox(roots)
		}
		if err == nil {
			err = syscall.Exec(os.Args[3], os.Args[4:], os.Environ())
		}
	}
	fmt.Fprintf(os.Stderr, "sandbox: agent not started: %s\n", runx.FirstLine(err.Error()))
	os.Exit(125)
}

// sandboxCommand preserves the agent's argv[0] and environment. It does not
// fall back to an unrestricted launch when the kernel refuses confinement.
func sandboxCommand(cmd *exec.Cmd, o procOpts) (*exec.Cmd, error) {
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	roots, err := sandboxRoots(o)
	if err != nil {
		return nil, err
	}
	path := cmd.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(o.Dir, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return confinedCommand(cmd, roots, path)
}

func sandboxRoots(o procOpts) ([]string, error) {
	cwd, err := filepath.Abs(o.Dir)
	if err != nil {
		return nil, err
	}
	// os.TempDir, not a hardcoded /tmp: it is the platform's own answer
	// (TMPDIR when that is absolute, /tmp otherwise) rather than an
	// assumption that one directory exists. The earlier literal also named
	// the wrong place on macOS, where /tmp is a symlink to the shared
	// /private/tmp while every process's own temporary directory is the
	// per-user TMPDIR, so the grant was broader than the run needed. When
	// the resolved directory is absent the grant is skipped rather than
	// fatal: a root that is not there is not a write an agent can make, and
	// refusing the launch over it would strand a run on a host configured
	// without a system temp directory.
	roots := []string{cwd}
	optional := 0
	if tmp := os.TempDir(); tmp != "" && filepath.IsAbs(tmp) {
		roots = append(roots, tmp)
		optional = len(roots) // the temp root, if it resolves, is dropped rather than fatal
	}
	// A linked checkout's objects and refs live outside its writable worktree.
	// Only conventional .git metadata is granted automatically, never an
	// arbitrary directory named by a repository-controlled gitdir file.
	if _, err := os.Lstat(filepath.Join(cwd, ".git")); err == nil {
		common, err := (&gitx.Repo{Dir: cwd}).CommonDir(context.Background())
		if err != nil {
			return nil, fmt.Errorf("sandbox git metadata: %w", err)
		}
		if filepath.Base(common) == ".git" {
			roots = append(roots, common)
		}
	}
	if tmp := os.Getenv("TMPDIR"); filepath.IsAbs(tmp) {
		roots = append(roots, tmp)
	}
	// Agent-owned state is writable; the rest of HOME remains read-only. Custom
	// agents and nonstandard state/cache locations use --sandbox-write.
	home, _ := os.UserHomeDir()
	if filepath.IsAbs(home) {
		state := map[string][]string{
			"claude": {".claude"}, "codex": {".codex"}, "gemini": {".gemini"},
			"qwen": {".qwen"}, "grok": {".grok"}, "agy": {".gemini/antigravity-cli"},
			"cursor-agent": {".cursor"}, "kimi": {".kimi-code"},
			"microagent": {".microagent"}, "dsh": {".dsh"},
			"opencode": {".local/share/opencode", ".local/state/opencode", ".cache/opencode"},
			"crush":    {".local/share/crush", ".local/state/crush", ".cache/crush"},
		}
		for _, name := range state[o.Tool] {
			root := filepath.Join(home, name)
			if err := os.MkdirAll(root, 0700); err != nil {
				return nil, fmt.Errorf("sandbox state directory: %w", err)
			}
			roots = append(roots, root)
		}
	}
	for _, raw := range o.SandboxWrite {
		root, err := gauntlethome.ExpandPath(raw)
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(cwd, root)
		}
		roots = append(roots, root)
	}
	kept := roots[:0]
	for i, root := range roots {
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			if i < optional {
				continue // a temp directory this host does not have; nothing to grant
			}
			return nil, fmt.Errorf("sandbox writable root %q: %w", root, err)
		}
		info, err := os.Stat(real)
		if err != nil {
			if i < optional {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			if i < optional {
				continue
			}
			return nil, fmt.Errorf("sandbox writable root %q is not a directory", root)
		}
		kept = append(kept, real)
	}
	slices.Sort(kept)
	return slices.Compact(kept), nil
}

func seatbeltProfile(roots []string) (string, error) {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n(allow file-write*\n  (literal \"/dev/null\")\n  (literal \"/dev/tty\")\n  (literal \"/dev/dtracehelper\")\n")
	for _, root := range roots {
		for _, c := range root {
			if c < 32 || c == 127 {
				return "", fmt.Errorf("sandbox root %q contains a control character", root)
			}
		}
		escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(root)
		fmt.Fprintf(&b, "  (subpath \"%s\")\n", escaped)
	}
	b.WriteString(")\n")
	return b.String(), nil
}
