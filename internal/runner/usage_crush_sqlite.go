// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build sqlite

package runner

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, linked only under this tag
)

// crush is the one supported agent that neither prints a usage counter nor
// keeps a JSONL transcript: it records per-session totals in a SQLite database
// inside the project it is working on (`.crush/crush.db`, at the git root it
// resolves). The columns are `sessions.prompt_tokens` and
// `sessions.completion_tokens`; completion is what the rest of gauntlet calls
// output.
//
// Reading it needs a driver, so this file is behind the same `sqlite` tag
// `--opencode-db` needs. Without it crush reports nothing, which is the rule
// everywhere here: measure, or say nothing.

// crushPollEvery is how often the database is re-read while a review runs.
// The dashboard turns successive readings into a rate, and a second is finer
// than a reader can follow anyway.
const crushPollEvery = 2 * time.Second

// crushReader reports what crush wrote to its project database since a review
// began.
type crushReader struct {
	path  string
	since time.Time
}

// newCrushReader returns a reader when this directory has a crush database,
// and nil when it has none: a first crush run in a fresh worktree creates one,
// so absence at startup is not final and Final() looks again.
func newCrushReader(dir string, since time.Time) transcriptReader {
	return &crushReader{path: crushDBPath(dir), since: since}
}

// crushDBPath locates the database crush would use for this directory. crush
// resolves its project root the way git does, so the walk upward stops at the
// first .crush it finds, and falls back to the directory itself.
func crushDBPath(dir string) string {
	cur := filepath.Clean(dir)
	for range 16 {
		candidate := filepath.Join(cur, ".crush", "crush.db")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return filepath.Join(filepath.Clean(dir), ".crush", "crush.db")
}

func (c *crushReader) Run(ctx context.Context, onChange func(output, thinking int)) {
	t := time.NewTicker(crushPollEvery)
	defer t.Stop()
	last := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if out, _ := c.read(); out > last {
				last = out
				onChange(out, 0)
			}
		}
	}
}

func (c *crushReader) Final() (output, thinking int) { return c.read() }

// read sums the completion tokens of every session crush touched since this
// review began. Sessions older than the review belong to whatever ran before
// it, and counting them would credit this review with someone else's spend.
func (c *crushReader) read() (output, thinking int) {
	if _, err := os.Stat(c.path); err != nil {
		return 0, 0
	}
	// Read-only, and immutable so the query never waits on crush's own writer
	// or leaves a -wal file behind in the reviewed tree.
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(2000)", c.path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, 0
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// updated_at is Unix milliseconds; a session started before this review
	// but written during it still counts, because that write is this review's.
	var total sql.NullInt64
	err = db.QueryRowContext(ctx,
		`SELECT SUM(completion_tokens) FROM sessions WHERE updated_at >= ?`,
		c.since.UnixMilli()).Scan(&total)
	if err != nil || !total.Valid || total.Int64 <= 0 {
		return 0, 0
	}
	if total.Int64 > maxPlausibleTokens {
		return 0, 0 // a counter that large is a misread, not a measurement
	}
	return int(total.Int64), 0
}
