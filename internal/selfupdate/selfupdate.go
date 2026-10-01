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
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/maci0/gauntlet/internal/gauntlethome"
)

// DefaultRepo is the GitHub repository releases are fetched from when
// --update-repo is omitted.
const DefaultRepo = "maci0/gauntlet"

// assetAttempts is how many times a release asset request is made before the
// update gives up, and the shape of the wait between them. Three covers the
// ordinary cases (a dial that raced a wake, a body cut mid-transfer) without
// turning a real outage into a long stall: the client timeout already bounds
// each attempt.
const (
	assetAttempts  = 3
	assetRetryBase = 1 * time.Second
	assetRetryMax  = 8 * time.Second
)

// githubPart is one side of owner/repo: letters, digits, and . _ -, and not
// a "." / ".." path segment that would climb out of /repos/.
func githubPart(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 100 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// ParseRepo accepts a GitHub owner/repo path, the form --update-repo takes.
// Empty means DefaultRepo. A URL, a path with extra segments, or a name
// outside GitHub's charset is refused here so Check never concatenates it
// into the API path.
func ParseRepo(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultRepo, nil
	}
	if strings.Contains(s, "://") || strings.Contains(s, "@") {
		return "", fmt.Errorf("want owner/repo, not a URL (%q)", s)
	}
	owner, repo, ok := strings.Cut(s, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", fmt.Errorf("want owner/repo, got %q", s)
	}
	if !githubPart(owner) || !githubPart(repo) {
		return "", fmt.Errorf("invalid GitHub repository %q: owner and repo are letters, digits, and . _ -", s)
	}
	return owner + "/" + repo, nil
}

const (
	envGHToken     = "GH_TOKEN"
	envGitHubToken = "GITHUB_TOKEN"
)

// githubToken is the optional bearer token for api.github.com. GH_TOKEN wins
// when both are set, matching GitHub CLI; either one is sent only to GitHub.
func githubToken() string {
	for _, name := range []string{envGHToken, envGitHubToken} {
		if tok := strings.TrimSpace(os.Getenv(name)); tok != "" {
			return tok
		}
	}
	return ""
}

func setGitHubAuth(req *http.Request) {
	if req.URL.Scheme == "https" &&
		(req.URL.Hostname() == "api.github.com" || req.URL.Hostname() == "github.com") {
		if tok := githubToken(); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	} else {
		req.Header.Del("Authorization")
	}
}

// repoNameRe is OWNER/REPO as GitHub writes it: no slashes beyond the one
// separator, no spaces, nothing that could turn the releases URL into a
// different API path or inject a header.
var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

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
	} `json:"assets"`
}

// Version is the release version without a leading "v".
func (r *Release) Version() string {
	if r == nil {
		return ""
	}
	return strings.TrimPrefix(r.TagName, "v")
}

// assetName is the binary this platform needs from a release. It must match
// what the Makefile's dist target produces, or self-update finds nothing.
func assetName(version string) string {
	return fmt.Sprintf("gauntlet_%s_%s_%s", version, runtime.GOOS, runtime.GOARCH)
}

var client = &http.Client{
	Timeout: 5 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if err := validateAssetURL(req.URL.String()); err != nil {
			return fmt.Errorf("redirect: %w", err)
		}
		setGitHubAuth(req)
		return nil
	},
}

// Check queries the latest release. It is never called on the startup path:
// a version check must not stand between the user and the first review.
func Check(ctx context.Context, repo string) (*Release, error) {
	repo, err := ParseRepo(repo)
	if err != nil {
		return nil, err
	}
	if !repoNameRe.MatchString(repo) {
		return nil, fmt.Errorf("invalid GitHub repository %q (want OWNER/REPO)", repo)
	}
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	setGitHubAuth(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError(resp, url)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		drainBody(resp, 4<<20)
		return nil, fmt.Errorf("decode %s: %w", url, err)
	}
	drainBody(resp, 4<<20)
	if rel.TagName == "" {
		return nil, errors.New("release has no tag")
	}
	return &rel, nil
}

// NewerThan reports whether the release is a different version from current.
// Comparison is deliberately exact rather than semver-aware: releases are the
// source of truth, and a "downgrade" published on purpose should be applied.
func (r *Release) NewerThan(current string) bool {
	return r != nil && r.Version() != "" && r.Version() != strings.TrimPrefix(current, "v")
}

// Apply downloads, verifies, and installs the release over the running
// executable. It returns the path that was replaced.
//
// The new binary is written next to the current one (same filesystem, so the
// rename is atomic) and only renamed after its checksum matches. A failed
// verification leaves the running binary untouched. The binary being replaced
// is kept beside it under PreviousSuffix, so an install that succeeds and then
// misbehaves can be rolled back by renaming that copy back.
//
// Installing a release that is already on disk is a no-op rather than a second
// install: the rollback copy is the only state an update changes besides the
// binary, and reinstalling would overwrite it with the release just installed,
// leaving nothing to roll back to.
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
	// NewTempFile sweeps first: a kill, an OOM, or a power cut skips every
	// defer, so without it each interrupted download leaves up to
	// maxAssetBytes beside the binary.
	tmp, err := gauntlethome.NewTempFile(dir, ".gauntlet-update-", nil)
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
	if subtle.ConstantTimeCompare([]byte(sum), []byte(expect)) != 1 {
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", want, sum, expect)
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	installed, err := fileSum(self)
	if err != nil {
		return "", err
	}
	if subtle.ConstantTimeCompare([]byte(installed), []byte(sum)) == 1 {
		return self, nil
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", err
	}
	if err := keepPrevious(self); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, self); err != nil {
		return "", fmt.Errorf("cannot replace %s: %w", self, err)
	}
	// The install is not finished until the directory records the rename.
	// The new binary's bytes are synced and the rollback copy's name is
	// recorded before this point, so a power cut between the rename and the
	// record leaves the replaced binary in place, under a name an update has
	// already reported replacing. The binary itself is in place either way,
	// so the message names it rather than reading as an update that did not
	// happen.
	if err := gauntlethome.SyncDir(dir); err != nil {
		return "", fmt.Errorf("%s is installed, but the rename cannot be recorded: %w", self, err)
	}
	return self, nil
}

// PreviousSuffix names the copy of the binary an update replaced, beside it in
// the same directory. Renaming the new binary over the old one is the point of
// an update, and it is also the one step here that cannot be undone, so the
// replaced binary is kept: a release that installs and then misbehaves is
// rolled back by renaming the copy back, without a build or a download.
const PreviousSuffix = ".previous"

// keepPrevious copies self to self+PreviousSuffix, atomically, before the
// rename that would destroy it.
//
// A copy that cannot be written aborts the update rather than proceeding: an
// install with no way back is a worse outcome than an install that did not
// happen, and the copy is cheap next to the download that precedes it.
func keepPrevious(self string) error {
	prev := self + PreviousSuffix
	fi, err := os.Stat(self)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", self, err)
	}
	in, err := os.Open(self)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", self, err)
	}
	defer in.Close()
	// The sweep in NewTempFile covers this prefix, so a copy interrupted by a
	// kill or a power cut is removed by a later update once it is older than
	// the retention window, rather than accumulating.
	tmp, err := gauntlethome.NewTempFile(filepath.Dir(self), ".gauntlet-update-", nil)
	if err != nil {
		return fmt.Errorf("cannot keep %s for rollback: %w", prev, err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op once the rename succeeded
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		return fmt.Errorf("cannot keep %s for rollback: %w", prev, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("cannot keep %s for rollback: %w", prev, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot keep %s for rollback: %w", prev, err)
	}
	if err := os.Chmod(tmpName, fi.Mode().Perm()); err != nil {
		return fmt.Errorf("cannot keep %s for rollback: %w", prev, err)
	}
	if err := os.Rename(tmpName, prev); err != nil {
		return fmt.Errorf("cannot keep %s for rollback: %w", prev, err)
	}
	// The copy is only a rollback path once its name is in the directory: a
	// power cut between the rename and that record leaves a file the next
	// boot cannot find.
	if err := gauntlethome.SyncDir(filepath.Dir(self)); err != nil {
		return fmt.Errorf("cannot record %s: %w", prev, err)
	}
	return nil
}

// validateAssetURL ensures the URL points to an authorized GitHub release
// host over HTTPS (or loopback in tests), preventing cleartext transfers or
// downloading assets from untrusted third-party hosts.
func validateAssetURL(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("invalid asset URL %q: %w", raw, err)
	}
	host := u.Hostname()
	if u.Scheme == "https" {
		if isAllowedHost(host) {
			return nil
		}
		return fmt.Errorf("untrusted asset host %q in %s (want github.com)", host, raw)
	}
	if u.Scheme == "http" && isLoopback(host) {
		return nil
	}
	return fmt.Errorf("untrusted asset URL %s: require https", raw)
}

func isAllowedHost(host string) bool {
	switch host {
	case "github.com", "api.github.com", "objects.githubusercontent.com",
		"release-assets.githubusercontent.com":
		return true
	}
	return isLoopback(host)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// getAsset opens url for reading, retrying a connection that fails for a
// transient reason. The retry covers the request up to the response headers
// only: a body already partly written into a caller's file is never resumed,
// because re-reading a fresh body would append a second copy of the asset
// rather than replace the first.
func getAsset(ctx context.Context, url string) (*http.Response, error) {
	if err := validateAssetURL(url); err != nil {
		return nil, err
	}
	for attempt := range assetAttempts {
		resp, err := getAssetOnce(ctx, url)
		if err == nil {
			return resp, nil
		}
		if !transient(err) || attempt == assetAttempts-1 {
			return nil, err
		}
		if !sleepCtx(ctx, assetBackoff(attempt)) {
			return nil, ctx.Err()
		}
	}
	return nil, ctx.Err()
}

func getAssetOnce(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	setGitHubAuth(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		err := responseError(resp, url)
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

// transient reports whether err is a network failure worth trying again. A
// refused status, a rejected URL, and a cancelled context are answers the
// server or the operator already gave, so repeating the request cannot change
// them. Only the transport-level failures that a second attempt can clear
// qualify: a dial or read that timed out, a connection cut mid-handshake, a
// body that ended early.
func transient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// client.Do wraps whatever the transport reported, so the classification
	// is on the error inside: a redirect the URL check refused arrives here
	// wrapped too, and repeating it would only repeat the refusal.
	if ue, ok := errors.AsType[*url.Error](err); ok {
		err = ue.Err
	}
	// A deadline the transport imposed on one attempt (a dial or header
	// timeout) is transient, unlike a deadline the caller set on the context.
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return !errors.Is(err, context.DeadlineExceeded)
	}
	// A peer that goes away before answering reads as a bare io.EOF when the
	// connection was closed cleanly and as ErrUnexpectedEOF when it was cut,
	// and a body cut short mid-transfer is the same failure one layer in.
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE)
}

// jitterSeed decorrelates one process's retry waits from every other
// process's. It is read once, at package load, from the wall clock: that is
// the one place here consuming entropy is right, because the value decides
// nothing about what the updater does. Every wait below is then a pure
// function of this value and the attempt number, so one process's backoff
// sequence can be reproduced once the value is known. A draw from the
// package-wide math/rand generator could not be: that generator is seeded
// from OS entropy when it loads, so nothing about the sequence it produced
// survives the process. A var so a test can pin it.
var jitterSeed = uint64(time.Now().UnixNano())

// assetJitter returns a value in [0, n) drawn from jitterSeed and attempt.
// Words at or above the largest multiple of n are rejected rather than
// wrapped, so the draw is uniform instead of biased toward the low residues,
// and each rejection mixes a fresh counter so the loop terminates. n <= 1 is
// 0.
func assetJitter(attempt, n int) int64 {
	if n <= 1 {
		return 0
	}
	ceil := (^uint64(0) / uint64(n)) * uint64(n)
	// FNV-1a over the attempt number, then splitmix64 over the seed, the key,
	// and the rejection counter. This is the construction runner/draw.go uses,
	// kept separate because selfupdate sits below runner in the package graph
	// and may not import it; a second copy of a hash is cheaper than an edge
	// that would put the updater above the thing it updates.
	keyHash := uint64(14695981039346656037)
	for _, b := range strconv.AppendInt(nil, int64(attempt), 10) {
		keyHash = (keyHash ^ uint64(b)) * 1099511628211
	}
	for i := uint64(0); ; i++ {
		// splitmix64: the key alone is too structured for the low bits the
		// modulo takes, and neighbouring attempts would land together.
		x := (jitterSeed ^ keyHash) + i*0x9e3779b97f4a7c15
		x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
		x = (x ^ x>>27) * 0x94d049bb133111eb
		if v := x ^ (x >> 31); v < ceil {
			return int64(v % uint64(n))
		}
	}
}

// assetBackoff is the wait before the next asset attempt: doubling from
// assetRetryBase, capped, and jittered so several installs retrying after the
// same outage do not come back together. The jitter is decorrelated per
// process rather than drawn from the process-wide random generator, so two
// installs still part company while one install's waits stay reproducible.
func assetBackoff(attempt int) time.Duration {
	d := assetRetryBase << attempt
	if d > assetRetryMax || d <= 0 {
		d = assetRetryMax
	}
	return d/2 + time.Duration(assetJitter(attempt, int(d/2)+1))
}

// sleepCtx waits d and reports false if ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func responseError(resp *http.Response, url string) error {
	defer drainBody(resp, 64<<10)
	var ghErr struct {
		Message string `json:"message"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&ghErr) == nil && ghErr.Message != "" {
		return fmt.Errorf("github returned %s for %s: %s", resp.Status, url, ghErr.Message)
	}
	return fmt.Errorf("github returned %s for %s", resp.Status, url)
}

func drainBody(resp *http.Response, limit int64) {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, limit))
	}
}

func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := getAsset(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		drainBody(resp, maxAssetBytes)
		return nil, fmt.Errorf("%s exceeds %d bytes", url, limit)
	}
	drainBody(resp, limit)
	return data, nil
}

// download streams url into w and returns the hex SHA-256 of what was written.
func download(ctx context.Context, url string, w io.Writer) (string, error) {
	resp, err := getAsset(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		// A body that ends early or a connection that drops mid-transfer
		// leaves w holding a partial asset. The caller discards it, and the
		// error has to say which asset and how far it got: a bare read error
		// reads the same whether the checksum listing or the binary died.
		return "", fmt.Errorf("downloading %s after %d bytes: %w", url, n, err)
	}
	if n > maxAssetBytes {
		drainBody(resp, maxAssetBytes)
		return "", fmt.Errorf("%s exceeds %d bytes", url, int64(maxAssetBytes))
	}
	drainBody(resp, maxAssetBytes+1)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fileSum is the hex SHA-256 of the bytes already installed at path, so a
// repeated install of the same release can be recognized and skipped.
func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
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
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
