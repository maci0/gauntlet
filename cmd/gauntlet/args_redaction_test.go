// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/runx"
)

// A command line carries a credential as readily as a path, and the index row
// is a permanent record: it is what "gauntlet runs --json" hands out and what a
// backup or a sync carries off the machine. An operator defines a wrapper
// agent by handing its key on the command line, because that is where the
// agent CLIs read it, so an argument is exactly where one turns up.
func TestJournalKeepsCredentialsOutOfTheCommandLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  string
		// secret is the credential that must not survive, and want is what
		// the argument reads as once it has been redacted. A token is
		// replaced in place, so the argument keeps its shape; a URL loses the
		// part that authenticates and keeps the scheme, host, and path.
		secret string
		want   string
	}{
		{
			"issued prefix",
			"--agent-cmd=wrapper=myagent --key ghp_0123456789abcdefghij",
			"ghp_0123456789abcdefghij",
			"--agent-cmd=wrapper=myagent --key " + runx.Redacted,
		},
		{
			"value after a flag",
			"--agent-cmd=wrapper=myagent --api-key A1b2C3d4E5f6G7h8",
			"A1b2C3d4E5f6G7h8",
			"--agent-cmd=wrapper=myagent --api-key " + runx.Redacted,
		},
		{
			"remote userinfo",
			"https://alice:hunter2secret@example.com/repo.git",
			"hunter2secret",
			"https://example.com/repo.git",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := journaledArgs([]string{"review", tc.arg})
			if want := "review"; got[0] != want {
				t.Fatalf("argument 0 = %q, want %q", got[0], want)
			}
			if strings.Contains(got[1], tc.secret) {
				t.Fatalf("the index row still carries the credential: %q", got[1])
			}
			if got[1] != tc.want {
				t.Fatalf("argument 1 = %q, want %q", got[1], tc.want)
			}
		})
	}
}

// The flags around the redacted one are the run's record of what it was asked
// to do, and a redactor that took the whole argument with it would leave a row
// no reader can act on.
func TestJournaledArgsRedactOnlyTheCredential(t *testing.T) {
	got := journaledArgs([]string{"review", "--agent-cmd=wrapper=myagent --key ghp_0123456789abcdefghij", "--jobs", "4"})
	if want := "review"; got[0] != want {
		t.Fatalf("argument 0 = %q, want %q", got[0], want)
	}
	if want := "wrapper=myagent"; !strings.Contains(got[1], want) {
		t.Fatalf("the agent definition was lost: %q", got[1])
	}
	if want := "--jobs"; got[2] != want {
		t.Fatalf("argument 2 = %q, want %q", got[2], want)
	}
	if want := "4"; got[3] != want {
		t.Fatalf("argument 3 = %q, want %q", got[3], want)
	}
}

// The flags whose names carry the words a credential does, and are none:
// --token-budget is gauntlet's own, and a redactor that read the name rather
// than the list would blank the budget out of every run's record.
func TestJournaledArgsKeepsACountThatMerelySaysToken(t *testing.T) {
	got := journaledArgs([]string{"review", "--token-budget", "1000000", "--max-tokens", "4096"})
	for i, want := range []string{"review", "--token-budget", "1000000", "--max-tokens", "4096"} {
		if got[i] != want {
			t.Fatalf("argument %d = %q, want %q", i, got[i], want)
		}
	}
}
