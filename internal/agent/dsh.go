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
	"time"
)

// dsh has no model flag: the headless profile's agent-default-model plugin
// decides. A --patch overlay overrides that plugin's config per run, and the
// override replaces the config object, so it must carry the required provider
// alongside the model. dsh:provider/model states both; bare dsh:model reuses
// the provider probed once from --dump-config.
var (
	dshPatchMu sync.Mutex
	dshPatches = map[string]string{}

	dshProbeOnce sync.Once
	dshProvider  string
	dshProbeErr  error
)

var dshProviderRe = regexp.MustCompile(`^\s+provider:\s*['"]?([\w.-]+)['"]?\s*$`)

// ParseDshProvider reads the provider of the agent-default-model entry from a
// `dsh --dump-config` listing.
func ParseDshProvider(dump string) string {
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

// dshDefaultProvider probes the headless profile's configured provider once
// per process. A failed probe keeps its error, not just an empty result, so
// the caller can say why a bare dsh:model could not be resolved.
func dshDefaultProvider(base []string) (string, error) {
	dshProbeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		argv := append(append([]string{}, base...), "--profile", "headless", "--dump-config")
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		out, err := cmd.Output()
		if err != nil {
			dshProbeErr = fmt.Errorf("%s --dump-config failed: %w", argv[0], err)
			return
		}
		dshProvider = ParseDshProvider(string(out))
		if dshProvider == "" {
			dshProbeErr = errors.New("the headless profile config has no agent-default-model provider")
		}
	})
	return dshProvider, dshProbeErr
}

// dshPlainSessionsPatch writes the overlay that turns off session compression.
//
// dsh stores its session log as zstd frames by default, which no reader here
// can follow. The uncompressed spelling is the same JSONL, so asking for it is
// what makes live token counts possible for dsh at all. It applies only to the
// runs gauntlet launches, since the overlay is passed per invocation.
func dshPlainSessionsPatch() (string, error) {
	return writeDshPatch("plain-sessions", "- id: session-persistence-jsonl\n"+
		"  config:\n    compression: 'none'\n")
}

// dshModelPatch writes (once per provider/model pair) the YAML overlay that
// pins dsh's model, and returns its path. It lives in the user cache dir, not
// a temp filesystem: it is small, reusable across runs, and never secret.
func dshModelPatch(provider, model string) (string, error) {
	key := strings.NewReplacer("/", "_", ":", "_").Replace(provider + "/" + model)
	body := fmt.Sprintf("- id: agent-default-model\n  config:\n    provider: '%s'\n    model: '%s'\n",
		provider, model)
	return writeDshPatch(key, body)
}

// writeDshPatch stores one overlay under the user cache dir and returns its
// path, writing it at most once per process.
func writeDshPatch(key, body string) (string, error) {
	dshPatchMu.Lock()
	defer dshPatchMu.Unlock()
	if p, ok := dshPatches[key]; ok {
		return p, nil
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
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	dshPatches[key] = path
	return path, nil
}
