// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runx

import (
	"strings"
	"testing"
)

// The redaction here is the last thing between a child's output and a string
// that outlives the run: it reaches error values, the run journal under
// GAUNTLET_HOME, and the dashboard. A reviewed repository steers what an agent
// and git print, so the input is attacker-influenced text of no fixed shape,
// and a pattern that backtracks on it is a hang in the middle of a run.

// FuzzRedactSecrets drives RedactSecrets, RedactUserinfo, and FirstLine with
// arbitrary text of the kind a child process, an agent, or a reviewed
// repository produces.
//
// The fuzzer finds the crash and the pathological case; the assertions are
// what make a leak visible, because a leak is not a crash. Three properties
// carry the security weight:
//
//   - no credential-shaped token survives. secretPrefixRe is the table of the
//     published token formats, and it has no minimum length to argue about, so
//     anything it still matches in the output is a credential somebody reads
//     later. RedactSecrets replaces every match it finds, so a survivor is a
//     hole.
//   - redaction is a fixpoint. Every pattern here rewrites its match, and a
//     rewrite that can be rewritten again is one that left something behind:
//     half a token, a value whose tail re-formed a prefix, a replacement that
//     re-entered a different pattern's reach. Idempotence is what the
//     replacement mark is chosen for, so it is asserted rather than assumed.
//   - FirstLine is the first line, redacted. It is what every error value past
//     this package is built from, and a multi-line error carrying an agent's
//     second and third lines out of a function named for cutting to one is how
//     a note this package redacted never gets redacted.
func FuzzRedactSecrets(f *testing.F) {
	seeds := []string{
		"",
		"\n",
		"fatal: not a git repository",
		"https://github.com/owner/repo.git",
		"git@github.com:owner/project.git",
		"https://alice:s3cret@example.test/repo.git/",
		"https://alice:s3cret@example.test/repo.git/\nfatal: second line\nthird line\n",
		"ANTHROPIC_API_KEY=sk-ant-api03-AbCdEf0123456789",
		`{"client_secret": "abcd1234efgh5678"}`,
		"gh auth login --with-token <<< ghp_0123456789abcdefABCDEF",
		"github_pat_11ABCDEFG0abcdefghijkl_0123456789abcdefghijklmnopqrstuvwxyz0123456789",
		"AKIAIOSFODNN7EXAMPLE",
		"myagent --api-key A1b2C3d4E5f6G7h8 -p x",
		"--token-budget=1000000 --max-tokens 4096 --password short",
		"sk-ant-api03-AAAAAAAAAAAAAAAAAAAA and ghp_AAAAAAAAAAAAAAAAAAAA and AKIAIOSFODNN7EXAMPLE",
		// Two credentials that touch, so a rewrite of the first has to leave
		// the second whole.
		"sk-ant-api03-AAAAAAAAAAAAAAAAAAAAghp_AAAAAAAAAAAAAAAAAAAA",
		// A replacement boundary: redaction runs left to right, so what the
		// inserted mark joins to is half a token and half an argument.
		"TOKEN=short sk-ant-api03-AAAAAAAAAAAAAAAAAAAA tail",
		// Long runs of the characters each pattern is built from, which is
		// where a backtracking shape shows up as time rather than as output.
		strings.Repeat("A", 512) + "=" + strings.Repeat("b", 512),
		strings.Repeat("sk-", 128),
		strings.Repeat("--token ", 128),
		strings.Repeat("https://u:p@", 64),
		strings.Repeat("API_KEY=", 128) + strings.Repeat("v", 256),
		strings.Repeat("A1b2C3d4E5f6G7h8 ", 64),
		strings.Repeat("'", 128) + "ghp_AAAAAAAAAAAAAAAAAAAA",
		// Values at the shortest length a credential may have, one below it,
		// and the shapes either side of the assignment and flag rules.
		"API_KEY=1234567",
		"API_KEY=12345678",
		"--token 1234567",
		"--token 12345678",
		"token=x token=12345678 \"TOKEN\": \"12345678\"",
		"\r\n\t  ",
		"héllo wörld — ünïcode",
		"AKIA" + strings.Repeat("A", 12),
		"ghp_" + strings.Repeat("A", 15),
		"ghp_" + strings.Repeat("A", 16),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, in string) {
		out := RedactSecrets(in)

		// No credential-shaped token may come out. These are the published
		// formats, matched whole, and the output is what a journal entry and
		// an error string keep.
		if leak := secretPrefixRe.FindString(out); leak != "" {
			t.Fatalf("RedactSecrets(%q) = %q, which still carries the token %q", in, out, leak)
		}
		// The fixpoint: a second pass has to find nothing left to rewrite.
		// A value the first pass half-consumed, or a replacement that landed
		// inside another pattern's reach, would show here.
		if again := RedactSecrets(out); again != out {
			t.Fatalf("RedactSecrets is not idempotent on %q: %q then %q", in, out, again)
		}
		if again := RedactUserinfo(out); again != out {
			t.Fatalf("RedactUserinfo rewrote redacted output %q into %q (from %q)", out, again, in)
		}

		// RedactUserinfo's own contract: a line with no scheme keeps every
		// byte, which is what keeps ssh's git@host:path spelling readable.
		if !strings.Contains(in, "://") {
			if got := RedactUserinfo(in); got != in {
				t.Fatalf("RedactUserinfo(%q) = %q, want it unchanged with no scheme", in, got)
			}
		}
		if again := RedactUserinfo(RedactUserinfo(in)); again != RedactUserinfo(in) {
			t.Fatalf("RedactUserinfo is not idempotent on %q", in)
		}

		// FirstLine: one line, and the redaction of that line. Both halves
		// matter, because this is what every caller past this package puts in
		// an error value and in the journal. The cut is on LF, so a lone CR
		// is content of the line and not a break in it; only LF is checked.
		line, _, _ := strings.Cut(in, "\n")
		want := RedactSecrets(RedactUserinfo(line))
		got := FirstLine(in)
		if got != want {
			t.Fatalf("FirstLine(%q) = %q, want the redacted first line %q", in, got, want)
		}
		if strings.Contains(got, "\n") {
			t.Fatalf("FirstLine(%q) = %q, which still carries a line break", in, got)
		}
		if leak := secretPrefixRe.FindString(got); leak != "" {
			t.Fatalf("FirstLine(%q) = %q, which still carries the token %q", in, got, leak)
		}
	})
}
