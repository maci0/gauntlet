// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/runx"
)

// dsh has no model flag: the headless profile's agent-default-model plugin
// decides. A --patch overlay overrides that plugin's config per run, and the
// override replaces the config object, so it must carry the required provider
// alongside the model. dsh:provider/model states both; bare dsh:model reuses
// the provider probed once from --dump-config.
var (
	dshPatchMu sync.Mutex
	dshPatches = map[string]string{}
)

var dshProviderRe = regexp.MustCompile(`^\s+provider:\s*['"]?([\w.-]+)['"]?\s*$`)

// parseDshProvider reads the provider of the agent-default-model entry from a
// `dsh --dump-config` listing.
func parseDshProvider(dump string) string {
	inEntry := false
	for line := range strings.SplitSeq(dump, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "- id:") {
			_, id, _ := strings.Cut(line, ":")
			inEntry = strings.TrimSpace(id) == "agent-default-model"
			continue
		}
		if inEntry {
			if m := dshProviderRe.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// dshProbeGrace is how long Wait may outlive the deadline kill before it
// gives up on an unreapable child.
const dshProbeGrace = 10 * time.Second

// dshProbeTimeout bounds the config dump. A hung launcher must not park the
// first dsh:model launch for the rest of the run.
const dshProbeTimeout = 120 * time.Second

// dshDumpMaxBytes bounds stdout and stderr of dsh --dump-config. A YAML dump
// is tens of kilobytes; an unbounded read must not exhaust RAM.
const dshDumpMaxBytes = 4 << 20

// dshProbe memoizes one provider probe per process. A var so a caller that
// needs the probe run again replaces the whole memo rather than reaching into
// fields the probe owns.
var dshProbe = &dshProviderProbe{}

// dshProviderProbe is one memoized probe: a result and its error, taken once.
type dshProviderProbe struct {
	once     sync.Once
	provider string
	err      error
}

// dumpDshConfig runs the launcher's config dump and returns the provider the
// headless profile's agent-default-model entry names. A var because the call
// leaves the process: a test supplies a listing, or the failure a missing
// binary produces, without a launcher on PATH and without the fetch the bunx
// fallback would make.
var dumpDshConfig = func(base []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dshProbeTimeout)
	defer cancel()
	argv := append(append([]string{}, base...), "--profile", "headless", "--dump-config")
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = os.TempDir()
	cmd.Env = runx.AbsPATHEnv()
	cmd.Stdin = nil
	out, errOut := runx.Bound(cmd, dshDumpMaxBytes, dshProbeGrace)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	if err := runx.Outcome(ctx, cmd.Run()); err != nil {
		if detail := strings.TrimSpace(errOut.String()); detail != "" {
			return "", fmt.Errorf("%s --dump-config failed: %w: %s", argv[0], err, runx.FirstLine(detail))
		}
		return "", fmt.Errorf("%s --dump-config failed: %w", argv[0], err)
	}
	if out.Hit || errOut.Hit {
		return "", fmt.Errorf("--dump-config output exceeded %d bytes", dshDumpMaxBytes)
	}
	provider := parseDshProvider(out.String())
	if provider == "" {
		return "", errors.New("the headless profile config has no agent-default-model provider")
	}
	return provider, nil
}

// dshDefaultProvider probes the headless profile's configured provider once
// per process. A failed probe keeps its error, not just an empty result, so
// the caller can say why a bare dsh:model could not be resolved.
//
// The probe runs in its own process group and the deadline kill takes down the
// whole group: Output reads through a pipe, and a grandchild that outlived the
// killed child would hold that pipe open and hang this call forever. KillGroup
// is deferred so grandchildren are reaped on normal exit as well.
func dshDefaultProvider(base []string) (string, error) {
	dshProbe.once.Do(func() {
		dshProbe.provider, dshProbe.err = dumpDshConfig(base)
	})
	return dshProbe.provider, dshProbe.err
}

// dshPatchKey names one overlay file. Every spelling two distinct
// provider/model pairs could share is percent-encoded: "@" separates the two
// halves, "/" and ":" are encoded so foo/bar cannot collide with foo_bar, and
// uppercase letters are encoded because a case-insensitive volume (the macOS
// default) gives "gpt-5" and "GPT-5" one file, where the second pin would launch
// with the first pair's model. "%" is outside dshModelRe's charset, so an
// encoded character is never confused with a literal one.
func dshPatchKey(provider, model string) string {
	return dshKeyPart(provider) + "@" + dshKeyPart(model)
}

func dshKeyPart(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c == '/', c == ':':
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// dshModelPatch writes (once per provider/model pair) the YAML overlay that
// pins dsh's model, and returns its path. It lives in the user cache dir, not
// a temp filesystem: it is small, reusable across runs, and never secret.
func dshModelPatch(provider, model string) (string, error) {
	if !dshModelRe.MatchString(provider) || !dshModelRe.MatchString(model) {
		return "", fmt.Errorf("invalid provider %q or model %q", provider, model)
	}
	body := fmt.Sprintf("- id: agent-default-model\n  config:\n    provider: '%s'\n    model: '%s'\n",
		provider, model)
	return writeDshPatch(dshPatchKey(provider, model), body)
}

// writeDshPatch stores one overlay under the user cache dir and returns its
// path. A process writes each key once unless the file has since vanished,
// in which case it is rewritten so dsh is never pointed at a missing path.
//
// The file is renamed over any existing copy rather than truncated in place:
// the cache is shared across gauntlet processes, and another run's dsh child
// may be reading this exact overlay as it starts. A reader that catches a
// truncate sees an empty or half-written config; a rename is atomic, so every
// reader gets one whole file, and identical content makes old and new
// interchangeable.
func writeDshPatch(key, body string) (string, error) {
	if key == "" || key == "." || strings.ContainsAny(key, "/\\") || strings.Contains(key, "..") {
		return "", errors.New("invalid overlay key")
	}
	dshPatchMu.Lock()
	defer dshPatchMu.Unlock()
	if p, ok := dshPatches[key]; ok {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		delete(dshPatches, key)
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "gauntlet", "dsh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, key+".yml")
	if err := gauntlethome.WriteFileAtomic(dir, "."+key+".yml-", path, []byte(body)); err != nil {
		return "", err
	}
	dshPatches[key] = path
	return path, nil
}
