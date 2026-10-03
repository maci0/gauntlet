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

// DshNpmPackage is the package the bunx fallback fetches and runs when no dsh
// launcher is on PATH. The version is exact, and deliberately: an unpinned
// spec resolves whatever the registry serves at the moment of the fetch and
// executes it, so the code a run runs would be a property of that moment
// rather than of this tree. Bumping it is a reviewed change, like every other
// dependency bump.
const DshNpmPackage = "@deepseek-ai/dsh@0.1.5-740e203097086e5e"

// dsh has no model flag: the headless profile's agent-default-model plugin
// decides. A --patch overlay overrides that plugin's config per run, and the
// override replaces the config object, so it must carry the required provider
// alongside the model. dsh:provider/model states both; bare dsh:model reuses
// the provider probed once from --dump-config.
var (
	dshPatchMu sync.Mutex
	dshPatches = map[string]dshPatch{}
)

// dshPatch is one memoized overlay: where it lives and the body it holds.
//
// The body is memoized alongside the path because the overlay is a
// materialized result keyed by name, and the name carries only the provider
// and the model. The directory is shared by every run and every version, so a
// file already sitting under a key is not this build's file: another version
// of the program may have written a body formatted differently, or pinned a
// different field, and dsh would be handed whatever that one left behind.
type dshPatch struct {
	path string
	body string
}

// dshPatchMapMax bounds the memo. Entries cost nothing to rebuild (a stat and
// at worst a rewrite of an identical file), and the key space is every
// provider/model pair a run pins, which a long --max-loops run over a
// suggested model list can keep enlarging. Past the cap the table is dropped
// whole rather than trimmed by a policy: an entry only saves a stat, so there
// is nothing a recency rule would buy.
const dshPatchMapMax = 256

// dshPatchAge bounds what the overlay directory keeps. It lives in the user
// cache dir, shared by every run and every version, and each distinct
// provider/model pair adds a file that nothing ever removes, so an install
// that keeps trying models would otherwise grow it without end. An overlay
// past this age is rewritten identically the next run that wants it, so the
// age costs a write and nothing else: a dsh child reads its --patch at
// startup, long before an overlay written this old is swept.
const dshPatchAge = 90 * 24 * time.Hour

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

// dshProbeTimeout bounds the config dump. A hung launcher must not park the
// first dsh:model launch for the rest of the run.
const dshProbeTimeout = 120 * time.Second

// dshDumpMaxBytes bounds stdout and stderr of dsh --dump-config. A YAML dump
// is tens of kilobytes; an unbounded read must not exhaust RAM.
const dshDumpMaxBytes = 4 << 20

// dshProbes memoizes one provider probe per launcher argv. A var so a caller
// that needs the probes run again replaces the whole memo rather than
// reaching into fields a probe owns.
//
// The launcher is part of the key, because it decides which config is dumped:
// a `--bin` override, the launcher on PATH, and the bunx fallback that fetches
// the package each read their own, and a bare dsh:model pinned to the provider
// of one of them launches the other. A single process runs more than one of
// those (an explicit --bin alongside the fallback, an agent definition that
// overrides the binary and one that does not), so the key is the whole argv.
//
// The probe runs under the lock: it costs a subprocess and a possible network
// fetch, and two callers reaching it at once would pay it twice for one
// answer.
var dshProbes = struct {
	sync.Mutex
	byBase map[string]dshProviderProbe
}{byBase: map[string]dshProviderProbe{}}

// dshProviderProbe is one memoized probe: a result and its error, taken once
// for one launcher argv, and the moment it was taken.
type dshProviderProbe struct {
	provider string
	err      error
	at       time.Time
}

// dshProbeRetry is how long a failed probe keeps its error. A success is kept
// for the life of the process, because the provider in the headless profile is
// a property of the install. A failure is not: the launcher being absent, the
// bunx fallback's fetch failing, and a config dump that came back empty are
// three statements about one moment, and a run long enough to outlive all
// three would otherwise never resolve a bare dsh:model again. The error is
// still kept, so the caller can say why, and re-probing costs one subprocess
// per launcher per window, on a path that runs once per dsh launch.
const dshProbeRetry = 30 * time.Second

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
	cmd.Env = runx.EnvIn(cmd.Dir)
	cmd.Stdin = nil
	out, errOut := runx.Bound(cmd, dshDumpMaxBytes, runx.WaitGrace)
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
// per launcher argv. A failed probe keeps its error, not just an empty result,
// so the caller can say why a bare dsh:model could not be resolved, and keeps
// it per launcher too: one launcher missing from PATH says nothing about the
// bunx fallback that stands in for it. Only a success is kept for good; a
// failure is re-probed after dshProbeRetry, because a probe that failed is
// evidence about a moment and not about the install.
//
// The probe runs in its own process group and the deadline kill takes down the
// whole group: Output reads through a pipe, and a grandchild that outlived the
// killed child would hold that pipe open and hang this call forever. KillGroup
// is deferred so grandchildren are reaped on normal exit as well.
//
// now is the clock the retry window is measured on; nil means wall time. It is
// the run's clock, so whether a replay re-probes depends on the run's own
// timeline rather than on how long the previous attempt really took.
func dshDefaultProvider(base []string, now func() time.Time) (string, error) {
	if now == nil {
		now = time.Now
	}
	key := strings.Join(base, "\x00")
	dshProbes.Lock()
	defer dshProbes.Unlock()
	p, ok := dshProbes.byBase[key]
	if ok {
		if p.err == nil {
			return p.provider, p.err
		}
		// The >= 0 half matters. A clock stepped backwards (an NTP correction, a
		// manual set) leaves at in the future, and the age it produces is
		// negative, which compares below the window for as long as the process
		// lives: a failure is then kept forever and the launcher that a later
		// probe would have resolved stays unresolvable for the rest of the run.
		// gitx.Repo.Sample and normalize.Normalizer.allow guard the same way.
		if age := now().Sub(p.at); age >= 0 && age < dshProbeRetry {
			return p.provider, p.err
		}
	}
	p = dshProviderProbe{at: now()}
	p.provider, p.err = dumpDshConfig(base)
	dshProbes.byBase[key] = p
	return p.provider, p.err
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

// dshPatchHolds reports whether path already holds exactly body.
//
// Existence is not the question, contents are: the key names a provider and a
// model and nothing about the version or the shape of the overlay under it,
// and the directory outlives any one build. A file that is the wrong length, or
// is not a regular file at all, fails on the stat and is never read, so a
// planted symlink or FIFO in the cache directory is rewritten over rather than
// read through. The size check also bounds the read: body is the overlay this
// package formats, under a hundred bytes.
func dshPatchHolds(path, body string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != int64(len(body)) {
		return false
	}
	got, err := os.ReadFile(path)
	return err == nil && string(got) == body
}

// dshModelPatch writes (once per provider/model pair) the YAML overlay that
// pins dsh's model, and returns its path. It lives in the user cache dir, not
// a temp filesystem: it is small, reusable across runs, and never secret.
func dshModelPatch(provider, model string, now func() time.Time) (string, error) {
	if !dshModelRe.MatchString(provider) || !dshModelRe.MatchString(model) {
		return "", fmt.Errorf("invalid provider %q or model %q", provider, model)
	}
	body := fmt.Sprintf("- id: agent-default-model\n  config:\n    provider: '%s'\n    model: '%s'\n",
		provider, model)
	return writeDshPatch(dshPatchKey(provider, model), body, now)
}

// writeDshPatch stores one overlay under the user cache dir and returns its
// path. A process writes each key once unless the file has since vanished or
// no longer holds the body this build wants, either of which rewrites it, so
// dsh is never pointed at a missing path or at another version's overlay.
//
// The file is renamed over any existing copy rather than truncated in place:
// the cache is shared across gauntlet processes, and another run's dsh child
// may be reading this exact overlay as it starts. A reader that catches a
// truncate sees an empty or half-written config; a rename is atomic, so every
// reader gets one whole file, and identical content makes old and new
// interchangeable.
//
// now is the clock the directory sweep ages files against; nil means wall
// time. It is the run's clock, so which overlays a replay finds already
// written does not depend on how long the first attempt took.
func writeDshPatch(key, body string, now func() time.Time) (string, error) {
	if key == "" || key == "." || strings.ContainsAny(key, "/\\") || strings.Contains(key, "..") {
		return "", errors.New("invalid overlay key")
	}
	dshPatchMu.Lock()
	defer dshPatchMu.Unlock()
	if p, ok := dshPatches[key]; ok && p.body == body && dshPatchHolds(p.path, body) {
		return p.path, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "gauntlet", "dsh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// The sweep covers the directory and not just the overlays in it, so a
	// rename that never completed does not leave its temporary behind
	// forever. It runs per write, and a write is one per pair per process.
	gauntlethome.SweepStaleTemps(dir, "", dshPatchAge, now)
	path := filepath.Join(dir, key+".yml")
	if !dshPatchHolds(path, body) {
		if err := gauntlethome.WriteFileAtomic(dir, "."+key+".yml-", path, []byte(body)); err != nil {
			return "", err
		}
	}
	if len(dshPatches) >= dshPatchMapMax {
		clear(dshPatches)
	}
	dshPatches[key] = dshPatch{path: path, body: body}
	return path, nil
}
