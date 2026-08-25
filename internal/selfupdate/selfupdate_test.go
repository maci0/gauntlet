// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package selfupdate

import (
	"path/filepath"
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
	// A 64-byte field that is not hex is not a digest. Length alone used to
	// accept one, and ToLower then returned it mangled (multibyte runes grow
	// when folded), so Apply would fail closed against an invented value
	// instead of reporting the missing entry.
	// Regression: FuzzChecksumFor/ffc9bf6d6a299570.
	bogus := "0000\xff" + strings.Repeat("0", 62)
	if _, ok := checksumFor(bogus+"  gauntlet_9.9.9_darwin_arm64", "gauntlet_9.9.9_darwin_arm64"); ok {
		t.Fatal("a non-hex field must read as no entry")
	}
}

// FuzzChecksumFor drives the checksums.txt parser with arbitrary remote
// content. This listing crosses a trust boundary: it comes from the release
// server and decides which digest the downloaded binary must match. The
// contract pinned here is what applyTo relies on: whatever checksumFor
// accepts is exactly 64 characters, lowercased, and corresponds to an entry
// of the listing whose file basename names the requested asset, so a
// fabricated hash or one attributed to a different file can never be chosen.
func FuzzChecksumFor(f *testing.F) {
	sum := strings.Repeat("a", 64)
	f.Add(sum+"  gauntlet_1.2.3_linux_amd64\n", "gauntlet_1.2.3_linux_amd64")
	f.Add("deadbeef  short-hash-line\n"+sum+"  dist/x\n", "x")
	f.Add("", "anything")
	f.Add(strings.Repeat("c", 64)+"  *bin/gauntlet_9.9.9_darwin_arm64\r\n", "gauntlet_9.9.9_darwin_arm64")
	f.Add(strings.ToUpper(sum)+"  upper\n", "upper")
	f.Add("   \n\t\n"+sum+"  gauntlet_1_linux_386", "nope")

	f.Fuzz(func(t *testing.T, listing, name string) {
		got, ok := checksumFor(listing, name)
		if again, okAgain := checksumFor(listing, name); ok != okAgain || again != got {
			t.Fatalf("checksumFor(%q, %q) is not deterministic", listing, name)
		}
		if !ok {
			return
		}
		if len(got) != 64 {
			t.Fatalf("accepted a %d-character hash for %q: %q", len(got), name, got)
		}
		if strings.ToLower(got) != got {
			t.Fatalf("returned a non-lowercase hash: %q", got)
		}
		found := false
		for line := range strings.SplitSeq(listing, "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			file := strings.TrimPrefix(fields[1], "*")
			if filepath.Base(file) == name && strings.EqualFold(fields[0], got) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("accepted %q for %q but no such entry exists in %q", got, name, listing)
		}
	})
}

func TestAssetName(t *testing.T) {
	got := assetName("1.2.3")
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
