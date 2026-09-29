// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package journal

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// BenchmarkAppendIndexLocked measures what a finished run pays to append its
// summary row, against an index holding runs already recorded.
func BenchmarkAppendIndexLocked(b *testing.B) {
	for _, rows := range []int{100, 1000, 10000} {
		b.Run(label(rows), func(b *testing.B) {
			home := b.TempDir()
			b.Setenv("GAUNTLET_HOME", home)
			var buf strings.Builder
			for i := range rows {
				buf.WriteString(`{"run_id":"r` + strconv.Itoa(i) + `","path":"/home/u/.gauntlet/runs/2026-08-25/r`)
				buf.WriteString(strconv.Itoa(i))
				buf.WriteString(`.jsonl","version":"0.1.0","dirs":["/w"],"agents":["claude:sonnet"],"start":"2026-08-25T13:00:00Z","end":"2026-08-25T13:10:00Z","loops":1,"reviews":6,"ok":5,"failed":1,"ins":40,"del":12,"lines_measured":true,"tokens":123456}` + "\n")
			}
			if err := os.WriteFile(filepath.Join(home, "index.jsonl"), []byte(buf.String()), 0o600); err != nil {
				b.Fatal(err)
			}
			now := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				if err := appendIndexLocked(Summary{
					RunID:         "new" + strconv.Itoa(i),
					Path:          "/home/u/.gauntlet/runs/2026-08-25/new.jsonl",
					Dirs:          []string{"/w"},
					Start:         now,
					End:           now.Add(10 * time.Minute),
					Reviews:       6,
					OK:            5,
					Failed:        1,
					Ins:           40,
					Del:           12,
					LinesMeasured: true,
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkIndexScan measures the duplicate check alone, without the fsync
// that dominates an append.
func BenchmarkIndexScan(b *testing.B) {
	for _, rows := range []int{100, 1000, 10000} {
		b.Run(label(rows), func(b *testing.B) {
			home := b.TempDir()
			b.Setenv("GAUNTLET_HOME", home)
			var buf strings.Builder
			for i := range rows {
				buf.WriteString(`{"run_id":"r` + strconv.Itoa(i) + `","path":"/home/u/.gauntlet/runs/2026-08-25/r`)
				buf.WriteString(strconv.Itoa(i))
				buf.WriteString(`.jsonl","version":"0.1.0","dirs":["/w"],"agents":["claude:sonnet"],"start":"2026-08-25T13:00:00Z","end":"2026-08-25T13:10:00Z","loops":1,"reviews":6,"ok":5,"failed":1,"ins":40,"del":12,"lines_measured":true,"tokens":123456}` + "\n")
			}
			if err := os.WriteFile(filepath.Join(home, "index.jsonl"), []byte(buf.String()), 0o600); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				switch _, err := scanIndexFor("absent"); {
				case err != nil:
					b.Fatal(err)
				}
			}
		})
	}
}

func scanIndexFor(runID string) (bool, error) {
	return indexNamesRun(runID)
}

func label(n int) string {
	switch {
	case n >= 1000:
		return strconv.Itoa(n/1000) + "k"
	default:
		return strconv.Itoa(n)
	}
}
