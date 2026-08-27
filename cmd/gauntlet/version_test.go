// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"runtime/debug"
	"testing"
)

// A release binary is stamped with -ldflags and must keep exactly what the
// tag put there. Everything else comes from what the toolchain embedded, so
// `go install ...@v1.10.0` names its version (update checks compare against
// it), a plain go build stays dev rather than inventing a number, and so does
// a toolchain that recorded no build information at all.
func TestResolveStamped(t *testing.T) {
	cases := []struct {
		name    string
		stamped string
		bi      *debug.BuildInfo
		want    string
	}{
		{"stamped wins over anything embedded", "9.9.9", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.1"}}, "9.9.9"},
		{"module install reports the tagged version", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, "1.2.3"},
		{"a toolchain-stamped tag passes through with its suffix", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3+dirty"}}, "1.2.3+dirty"},
		{"plain go build stays dev", "dev", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
		{"no module version recorded stays dev", "dev", &debug.BuildInfo{}, "dev"},
		{"no build information at all stays dev", "dev", nil, "dev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveStamped(c.stamped, c.bi); got != c.want {
				t.Fatalf("resolveStamped(%q) with Main.Version %q = %q, want %q",
					c.stamped, mainVersionOf(c.bi), got, c.want)
			}
		})
	}
}

func mainVersionOf(bi *debug.BuildInfo) string {
	if bi == nil {
		return "(none)"
	}
	return bi.Main.Version
}
