// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"strings"
	"testing"
	"time"
)

func TestTerminalAgentFailureReadsProviderAnswers(t *testing.T) {
	terminal := []string{
		"Error: Claude usage limit reached. Your limit will reset at 3pm.",
		"You exceeded your current quota, please check your plan and billing details.",
		"insufficient_quota: You exceeded your current quota",
		"Credit balance is too low to access the Anthropic API",
		"Error: 401 {\"type\":\"error\",\"error\":{\"type\":\"authentication_error\"}}",
		"Invalid API key provided",
		"model_not_found: The model `gpt-9-turbo` does not exist",
		"unknown model: qwen-nonsense",
	}
	for _, note := range terminal {
		if !terminalAgentFailure(note) {
			t.Errorf("terminalAgentFailure(%q) = false, want true", note)
		}
	}
}

func TestTerminalAgentFailureLeavesTransientAndReviewLines(t *testing.T) {
	// A 429 and an overloaded region clear in seconds, which is what the
	// backoff is for. The rest are lines a review can end on: a last line is
	// not an error message, and an agent that finds these and then exits
	// nonzero must still be retried.
	transient := []string{
		"",
		"Error: 429 Too Many Requests, retry after 20s",
		"API is overloaded, please try again",
		"rate limit exceeded for requests, retrying",
		"src/handler.go:28 drops the auth check, so an unauthorized caller reaches the row",
		"docs/api-review.md: rate limiting is missing on the public endpoint",
		"Done: sec-review in 4m2s",
	}
	for _, note := range transient {
		if terminalAgentFailure(note) {
			t.Errorf("terminalAgentFailure(%q) = true, want false", note)
		}
	}
}

// A provider that has spent the window answers the same way every time, so a
// review that got that answer is not launched again on the same agent. A
// transient one still is.
func TestReviewRetriesOncePerAgentAnswer(t *testing.T) {
	oldDelay := retryBaseDelay
	retryBaseDelay = time.Millisecond
	t.Cleanup(func() { retryBaseDelay = oldDelay })

	for _, tc := range []struct {
		name   string
		answer string
		starts int
	}{
		{"terminal", "Error: Claude usage limit reached", 1},
		{"transient", "Error: 429 Too Many Requests", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := testRepo(t)
			set, _ := promptSet(t, "sec-review")
			bin := fakeAgent(t, t.TempDir(), "claude", "echo '"+tc.answer+"'\nexit 1")
			cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
			cfg.Retries = 2

			_, events := runRecorded(t, cfg)
			if n := countKind(events, EvReviewStart); n != tc.starts {
				t.Fatalf("started %d attempts, want %d", n, tc.starts)
			}
			said, carried := false, false
			for _, ev := range events {
				if ev.Kind == EvLog && strings.HasPrefix(ev.Text, "Not retrying ") {
					said = true
				}
				if ev.Kind == EvReviewEnd && strings.Contains(ev.Text, tc.answer) {
					carried = true
				}
			}
			if said != (tc.starts == 1) {
				t.Fatalf("said %t that it would not retry, want %t", said, tc.starts == 1)
			}
			// The reason reaches the journal row, so a run that stopped on a
			// spent window says why without the terminal in front of it.
			if !carried {
				t.Fatalf("the review's own failure does not carry %q", tc.answer)
			}
		})
	}
}

func TestLastNoteIsDisplaySafe(t *testing.T) {
	tail := []byte("working\n\x1b[31mError: invalid api key\x1b[0m\n\n")
	got := lastNote(tail)
	if got != "Error: invalid api key" {
		t.Fatalf("lastNote = %q, want %q", got, "Error: invalid api key")
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Fatalf("lastNote kept an escape: %q", got)
	}
}

func TestWithNoteUsesTheLastLine(t *testing.T) {
	if got := withNote("commit step failed", "a\nb\n\nError: unknown model\n"); got !=
		"commit step failed: Error: unknown model" {
		t.Fatalf("withNote = %q", got)
	}
	if got := withNote("commit step failed", "\n \n"); got != "commit step failed" {
		t.Fatalf("withNote = %q", got)
	}
}

// lastNote walks the newline boundaries from the end. The cases below are the
// ones where a walk and a split disagree about how many lines there are: a
// trailing newline, no newline at all, nothing but newlines, and an empty tail.
func TestLastNoteLineBoundaries(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"\n", ""},
		{"\n\n\n", ""},
		{"   \n\t\n", ""},
		{"reason", "reason"},
		{"reason\n", "reason"},
		{"first\nreason", "reason"},
		{"first\nreason\n", "reason"},
		{"first\nreason\n\n\n", "reason"},
		{"first\nreason\n   \n", "reason"},
	}
	for _, c := range cases {
		if got := lastNote([]byte(c.in)); got != c.want {
			t.Errorf("lastNote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
