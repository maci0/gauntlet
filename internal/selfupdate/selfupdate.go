// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package selfupdate replaces the running binary with a newer release and
// hands control to it without losing the run in progress.
//
// Two mechanisms live here and compose:
//
//   - Update: fetch the release asset for this GOOS/GOARCH, verify its
//     SHA-256 against the release's checksums.txt, and rename it over the
//     current executable. Nothing is executed before it is verified.
//   - Watch: notice that the executable on disk changed (by this updater, by
//     `make install`, or by a fresh `go build`) and let the caller re-exec at
//     a safe point.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// defaultRepo is the GitHub repository releases are fetched from.
const defaultRepo = "maci0/gauntlet"

// maxAssetBytes bounds a download. A release binary that large is a mistake or
// an attack, and either way should not fill the disk.
const maxAssetBytes = 256 << 20

// Release is the subset of a GitHub release that matters here.
type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// Version is the release version without a leading "v".
func (r *Release) Version() string { return strings.TrimPrefix(r.TagName, "v") }

// assetName is the binary this platform needs from a release. It must match
// what the Makefile's dist target produces, or self-update finds nothing.
func assetName(version string) string {
	return fmt.Sprintf("gauntlet_%s_%s_%s", version, runtime.GOOS, runtime.GOARCH)
}

var client = &http.Client{Timeout: 5 * time.Minute}

// Check queries the latest release. It is never called on the startup path:
// a version check must not stand between the user and the first review.
func Check(ctx context.Context, repo string) (*Release, error) {
	if repo == "" {
		repo = defaultRepo
	}
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %s for %s", resp.Status, url)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, errors.New("release has no tag")
	}
	return &rel, nil
}

// NewerThan reports whether the release is a different version from current.
// Comparison is deliberately exact rather than semver-aware: releases are the
// source of truth, and a "downgrade" published on purpose should be applied.
func (r *Release) NewerThan(current string) bool {
	return r.Version() != "" && r.Version() != strings.TrimPrefix(current, "v")
}

// Apply downloads, verifies, and installs the release over the running
// executable. It returns the path that was replaced.
//
// The new binary is written next to the current one (same filesystem, so the
// rename is atomic) and only renamed after its checksum matches. A failed
// verification leaves the running binary untouched.
func Apply(ctx context.Context, rel *Release) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return "", err
	}
	return applyTo(ctx, rel, self)
}

// applyTo is Apply with an explicit target, so the install path can be tested
// without replacing the test binary.
func applyTo(ctx context.Context, rel *Release, self string) (string, error) {
	want := assetName(rel.Version())
	var assetURL, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			assetURL = a.URL
		case "checksums.txt":
			sumsURL = a.URL
		}
	}
	if assetURL == "" {
		return "", fmt.Errorf("release %s has no asset %s", rel.TagName, want)
	}
	if sumsURL == "" {
		return "", fmt.Errorf("release %s has no checksums.txt; refusing to install unverified binary", rel.TagName)
	}

	sums, err := fetch(ctx, sumsURL, 1<<20)
	if err != nil {
		return "", fmt.Errorf("cannot fetch checksums: %w", err)
	}
	expect, ok := checksumFor(string(sums), want)
	if !ok {
		return "", fmt.Errorf("checksums.txt has no entry for %s", want)
	}

	dir := filepath.Dir(self)
	tmp, err := os.CreateTemp(dir, ".gauntlet-update-*")
	if err != nil {
		return "", fmt.Errorf("cannot write next to %s: %w", self, err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op once the rename succeeded
	}()

	sum, err := download(ctx, assetURL, tmp)
	if err != nil {
		return "", err
	}
	if sum != expect {
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", want, sum, expect)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, self); err != nil {
		return "", fmt.Errorf("cannot replace %s: %w", self, err)
	}
	return self, nil
}

func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// download streams url into w and returns the hex SHA-256 of what was written.
func download(ctx context.Context, url string, w io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %s", url, resp.Status)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxAssetBytes {
		return "", fmt.Errorf("asset exceeds %d bytes", int64(maxAssetBytes))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// checksumFor finds one file's expected hash in a `sha256sum` style listing
// ("<hex>  <name>", with an optional binary-mode asterisk). Only entries
// whose hash is 64 hex digits count: anything else is not a digest, and a
// mangled or misattributed entry must read as "no entry" rather than as an
// expectation the downloaded bytes can never meet.
func checksumFor(listing, name string) (string, bool) {
	for line := range strings.SplitSeq(listing, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		sum, file := fields[0], strings.TrimPrefix(fields[1], "*")
		if filepath.Base(file) == name && isHexDigest(sum) {
			return strings.ToLower(sum), true
		}
	}
	return "", false
}

// isHexDigest reports whether s is a SHA-256 digest exactly as sha256sum
// writes it: 64 hex digits, nothing else. The length check is on bytes before
// any case folding: ToLower can grow a multibyte rune, so folding first could
// turn a 64-byte non-digest into something of some other length entirely.
func isHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
