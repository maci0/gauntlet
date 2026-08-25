// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package selfupdate

import (
	"runtime"
	"strings"
	"testing"
)

func TestChecksumFor(t *testing.T) {
	sum := strings.Repeat("a", 64)
	listing := "  \n" +
		"deadbeef  short-hash-line\n" +
		sum + "  gauntlet_1.2.3_linux_amd64\n" +
		strings.Repeat("b", 64) + " *dist/gauntlet_1.2.3_darwin_arm64\n"

	got, ok := checksumFor(listing, "gauntlet_1.2.3_linux_amd64")
	if !ok || got != sum {
		t.Fatalf("got %q %v", got, ok)
	}
	// Binary-mode asterisk and a directory prefix must still match.
	if got, ok := checksumFor(listing, "gauntlet_1.2.3_darwin_arm64"); !ok || got != strings.Repeat("b", 64) {
		t.Fatalf("got %q %v", got, ok)
	}
	if _, ok := checksumFor(listing, "gauntlet_1.2.3_windows_amd64"); ok {
		t.Fatal("missing entry reported as found")
	}
	if _, ok := checksumFor("deadbeef  short-hash-line", "short-hash-line"); ok {
		t.Fatal("a short hash must not be accepted")
	}
}

func TestAssetName(t *testing.T) {
	got := AssetName("1.2.3")
	if !strings.Contains(got, runtime.GOOS) || !strings.Contains(got, runtime.GOARCH) {
		t.Fatalf("asset name lacks the platform: %q", got)
	}
}

func TestNewerThan(t *testing.T) {
	rel := &Release{TagName: "v1.2.3"}
	if !rel.NewerThan("1.0.0") {
		t.Fatal("a different version should be installable")
	}
	if rel.NewerThan("v1.2.3") {
		t.Fatal("the same version must not reinstall")
	}
}
