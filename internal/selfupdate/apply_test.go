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
		Size int64  `json:"size"`
	}{Name: assetName("9.9.9"), URL: "http://127.0.0.1:1/asset"})

	if _, err := Apply(context.Background(), rel); err == nil ||
		!strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("unverifiable release must be refused, got %v", err)
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

	sweepStaleTemps(dir, ".gauntlet-update-", staleTempAge)

	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must survive the sweep: %v", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the abandoned download should be gone (stat err=%v)", err)
	}
}
