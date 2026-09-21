// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// aiTrailer reports whether one commit message line is AI attribution.
// Cursor injects these at git commit despite the commit prompt ban:
// "Co-Authored-By: Cursor <cursoragent@cursor.com>", "Generated-by: Cursor",
// "Made-with: Cursor".
func aiTrailer(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	low := strings.ToLower(t)
	if strings.Contains(low, "cursoragent") {
		return true
	}
	key, _, ok := strings.Cut(low, ":")
	if !ok {
		return false
	}
	switch strings.TrimSpace(key) {
	case "co-authored-by", "generated-by", "made-with":
		return true
	}
	return false
}

// stripAITrailers drops AI attribution lines, preserving everything else
// including Signed-off-by. It returns the message unchanged when no AI
// trailer is present, and never returns an empty message: a commit whose
// whole body is trailers keeps its original text rather than becoming
// an empty commit.
func stripAITrailers(msg string) string {
	lines := strings.Split(strings.ReplaceAll(msg, "\r\n", "\n"), "\n")
	keep := lines[:0:0]
	dropped := false
	for _, l := range lines {
		if aiTrailer(l) {
			dropped = true
			continue
		}
		keep = append(keep, l)
	}
	if !dropped {
		return msg
	}
	for len(keep) > 0 && strings.TrimSpace(keep[len(keep)-1]) == "" {
		keep = keep[:len(keep)-1]
	}
	if len(keep) == 0 {
		return msg
	}
	return strings.Join(keep, "\n") + "\n"
}

// StripAITrailers removes AI attribution trailers from HEAD's message,
// amending in place. since is the tip before this step's commit: when HEAD
// has not moved there is nothing new to clean and it leaves history alone,
// unless since is empty, which means "clean HEAD whatever it is" and is
// what the yolo rebase path uses after the replay. --amend without
// --reset-author keeps the original author; --no-verify matches CommitAll.
func (r *Repo) StripAITrailers(ctx context.Context, since string) (bool, error) {
	head, err := r.Tip(ctx, "HEAD")
	if err != nil || head == "" || (since != "" && head == since) {
		return false, nil
	}
	out, err := r.run(ctx, gitQuick, "log", "-1", "--format=%B", "HEAD", "--")
	if err != nil {
		return false, fmt.Errorf("git log: %w", err)
	}
	cleaned := stripAITrailers(string(out))
	if cleaned == string(out) {
		return false, nil
	}
	if _, err := r.runIn(ctx, bytes.NewReader([]byte(cleaned)), gitNormal,
		"commit", "--amend", "--no-verify", "--quiet", "-F", "-"); err != nil {
		return false, fmt.Errorf("git commit --amend: %w", err)
	}
	return true, nil
}
