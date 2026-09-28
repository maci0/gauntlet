// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/gauntlethome"
)

// releaseServer serves one asset and a checksums.txt, optionally lying about
// the hash.
func releaseServer(t *testing.T, payload []byte, sum string) (*httptest.Server, *Release) {
	t.Helper()
	name := assetName("9.9.9")
	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sum + "  " + name + "\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rel := &Release{TagName: "v9.9.9"}
	body := `{"tag_name":"v9.9.9","assets":[
		{"name":"` + name + `","browser_download_url":"` + srv.URL + `/asset"},
		{"name":"checksums.txt","browser_download_url":"` + srv.URL + `/checksums.txt"}]}`
	if err := json.Unmarshal([]byte(body), rel); err != nil {
		t.Fatal(err)
	}
	return srv, rel
}

func TestApplyRejectsChecksumMismatch(t *testing.T) {
	payload := []byte("#!/bin/sh\necho new\n")
	_, rel := releaseServer(t, payload, strings.Repeat("0", 64))

	target := filepath.Join(t.TempDir(), "gauntlet")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := applyTo(context.Background(), rel, target)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want a checksum mismatch, got %v", err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "old binary" {
		t.Fatal("a failed verification must leave the existing binary untouched")
	}
	// And no partial download is left lying around.
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".gauntlet-update-*"))
	if len(matches) != 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}
}

func TestApplyRefusesReleaseWithoutChecksums(t *testing.T) {
	rel := &Release{TagName: "v9.9.9"}
	rel.Assets = append(rel.Assets, struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}{Name: assetName("9.9.9"), URL: "http://127.0.0.1:1/asset"})

	if _, err := Apply(context.Background(), rel); err == nil ||
		!strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("unverifiable release must be refused, got %v", err)
	}
}

func TestApplyRefusesReleaseWithoutPlatformAsset(t *testing.T) {
	rel := &Release{TagName: "v9.9.9"}
	rel.Assets = append(rel.Assets, struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}{Name: "checksums.txt", URL: "http://127.0.0.1:1/checksums.txt"})

	target := filepath.Join(t.TempDir(), "gauntlet")
	if _, err := applyTo(context.Background(), rel, target); err == nil ||
		!strings.Contains(err.Error(), "has no asset") {
		t.Fatalf("release without platform asset must be refused, got %v", err)
	}
}

func TestApplyRefusesMissingChecksumEntry(t *testing.T) {
	payload := []byte("#!/bin/sh\necho new\n")
	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("a", 64) + "  some_other_asset\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	name := assetName("9.9.9")
	rel := &Release{TagName: "v9.9.9"}
	rel.Assets = append(rel.Assets,
		struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{Name: name, URL: srv.URL + "/asset"},
		struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{Name: "checksums.txt", URL: srv.URL + "/checksums.txt"},
	)

	target := filepath.Join(t.TempDir(), "gauntlet")
	if _, err := applyTo(context.Background(), rel, target); err == nil ||
		!strings.Contains(err.Error(), "checksums.txt has no entry for "+name) {
		t.Fatalf("missing checksum entry must be refused, got %v", err)
	}
}

func TestApplyReplacesTargetOnMatch(t *testing.T) {
	payload := []byte("#!/bin/sh\necho new\n")
	h := sha256.Sum256(payload)
	_, rel := releaseServer(t, payload, hex.EncodeToString(h[:]))

	target := filepath.Join(t.TempDir(), "gauntlet")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := applyTo(context.Background(), rel, target)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("replaced %q, want %q", got, target)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(payload) {
		t.Fatalf("binary not replaced: %q", body)
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("replacement is not executable: %v", fi.Mode())
	}
	// No temp files survive a successful install.
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".gauntlet-update-*"))
	if len(matches) != 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}
}

// The rename that installs a release is the one step an update cannot undo, so
// the binary it replaces is kept beside it: a release that installs and then
// misbehaves is rolled back by renaming that copy back over the new one.
func TestApplyKeepsTheReplacedBinaryForRollback(t *testing.T) {
	payload := []byte("#!/bin/sh\necho new\n")
	h := sha256.Sum256(payload)
	_, rel := releaseServer(t, payload, hex.EncodeToString(h[:]))

	dir := t.TempDir()
	target := filepath.Join(dir, "gauntlet")
	old := []byte("#!/bin/sh\necho old\n")
	if err := os.WriteFile(target, old, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := applyTo(context.Background(), rel, target); err != nil {
		t.Fatal(err)
	}
	prev := target + PreviousSuffix
	kept, err := os.ReadFile(prev)
	if err != nil {
		t.Fatalf("the replaced binary was not kept: %v", err)
	}
	if string(kept) != string(old) {
		t.Fatalf("kept %q, want the binary that was replaced", kept)
	}
	fi, err := os.Stat(prev)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("the kept binary is not executable: %v", fi.Mode())
	}
	// The documented rollback is a rename, so it has to work: putting the
	// copy back restores the old binary byte for byte.
	if err := os.Rename(prev, target); err != nil {
		t.Fatal(err)
	}
	back, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != string(old) {
		t.Fatalf("rollback restored %q, want the previous binary", back)
	}
}

// A release that never verified replaced nothing, so there is nothing to roll
// back to and the copy is not made.
func TestApplyLeavesNoRollbackCopyAfterAFailedVerification(t *testing.T) {
	payload := []byte("#!/bin/sh\necho new\n")
	_, rel := releaseServer(t, payload, strings.Repeat("0", 64))

	target := filepath.Join(t.TempDir(), "gauntlet")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := applyTo(context.Background(), rel, target); err == nil {
		t.Fatal("want a checksum mismatch")
	}
	if _, err := os.Stat(target + PreviousSuffix); !os.IsNotExist(err) {
		t.Fatalf("a failed verification must not leave %s (stat err=%v)", PreviousSuffix, err)
	}
}

// An update killed mid-download (SIGKILL, OOM, power cut) skips every defer
// and leaves its partial download beside the binary. The next update sweeps
// what is old enough to be certainly abandoned and touches nothing else.
func TestSweepStaleTempsRemovesOnlyAbandonedDownloads(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := write(".gauntlet-update-old")
	fresh := write(".gauntlet-update-new")
	other := write("gauntlet")
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	gauntlethome.SweepStaleTemps(dir, ".gauntlet-update-", gauntlethome.StaleTempAge, nil)

	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must survive the sweep: %v", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the abandoned download should be gone (stat err=%v)", err)
	}
}
