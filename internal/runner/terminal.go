// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Whether a failed agent launch is worth running again. A provider's answer to
// a request is a statement about the account, not about the tree: once the
// account's window is gone or its key is rejected, the same command with the
// same model fails the same way, so the backoff between attempts buys nothing
// and the attempt is paid for twice.

package runner

import "regexp"

// terminalAgentRe matches the provider conditions a retry cannot clear: a
// spent or exhausted quota, a rejected key, a model the account cannot reach.
//
// The list is deliberately narrow, and each entry is a phrase no ordinary
// sentence of a review uses. A review's own findings live in the report
// sections, not in the one line the classifier reads, and the ambiguous words
// those findings do use ("rate limit", "unauthorized", "quota", "api key")
// are left out for exactly that reason. The cost of a miss is one wasted
// attempt; the cost of a false positive is a review that is not retried on the
// agent that just failed it. Narrow errs the second way only when a phrase
// this narrow really is an error line.
var terminalAgentRe = regexp.MustCompile(`(?i)` +
	// The window is spent, or the account has nothing left to spend.
	`\busage limit reached\b` +
	`|\busage limit\b[^\n]{0,40}\b(?:exceeded|reached)\b` +
	`|\byou(?:'ve| have)? exceeded your\b[^\n]{0,40}\bquota\b` +
	`|\bexceeded your\b[^\n]{0,40}\bquota\b` +
	`|\bquota (?:exceeded|exhausted)\b` +
	`|\binsufficient[_ ]quota\b` +
	`|\bcredit balance is too low\b` +
	`|\b(?:billing|payment)\b[^\n]{0,30}\b(?:required|issue|error|problem)\b` +
	// The key is refused. "invalid api key" as one phrase, or the key named
	// with the verdict after it: no review prose puts those together.
	`|\binvalid[_ ]api[_ ]key\b` +
	`|\bauthentication[_ ]error\b` +
	`|\bapi[_ ]key\b[^\n]{0,30}\b(?:invalid|expired|revoked|not found)\b` +
	// The model is not one this account can run. Retrying the same pin fails
	// identically, and the run's next review would fail the same way, so this
	// is the one that most needs saying out loud.
	`|\bmodel[_ ]not[_ ]found\b` +
	`|\bmodel\b[^\n]{0,20}\bnot found\b` +
	`|\bunknown model\b` +
	`|\binvalid model\b`)

// terminalAgentFailure reports whether the agent's own last line says the
// launch failed for a reason a retry cannot clear. The line is model-adjacent
// output: it is the agent's statement of why it stopped, and the runner already
// trusts it that far when it explains a failure to the operator. Reading it for
// a retry decision is the same signal used once more, not a new dependency on
// the text.
//
// Transient conditions are absent on purpose: a 429, an overloaded region, and
// a dropped connection clear in seconds, which is what the backoff is for.
func terminalAgentFailure(note string) bool {
	return note != "" && terminalAgentRe.MatchString(note)
}
