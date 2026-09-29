// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Stopping one review that has spent the run's whole token budget on its own.
// The budget itself is a scheduling bound: it is read before a loop, before a
// review, and before a retry, so it decides what starts next and never what is
// already running. That is the right shape for fifty reviews of ordinary size
// and the wrong one for a single launch an agent keeps extending, where the
// provider is charged for every turn until the timeout kills it.

package runner

import (
	"context"
	"sync"
)

// reviewCap is the run's --token-budget applied to one launch. A review that
// reaches the whole ceiling on its own is stopped: no run spends its entire
// budget in one review, so the figure is the runaway rather than the plan, and
// the work is dropped rather than committed on the strength of what it cost.
//
// Only a figure the provider itself stated reaches it. A number matched out of
// the agent's prose can be a fixture the model read, a line it invented, or a
// figure quoted from a file in the reviewed tree, and a model that printed one
// would otherwise be able to stop its own review and end the run. So the two
// sources are kept apart here: the machine-readable usage envelope and the
// session transcript, both written by the provider rather than by the model.
// An agent in prose mode reports no such figure, and a run in prose mode is
// bounded by its timeout, exactly as it was before.
type reviewCap struct {
	limit int
	stop  context.CancelFunc

	mu      sync.Mutex
	tokens  int
	tripped bool
}

// newReviewCap returns the cap for one review. A limit of zero (no budget) is
// a cap that never trips, which is what the same code path then does.
func newReviewCap(limit int, stop context.CancelFunc) *reviewCap {
	return &reviewCap{limit: limit, stop: stop}
}

// reading records what the provider has reported for the review so far and
// stops the launch the first time it reaches the ceiling. Readings are
// cumulative, so the first crossing is also the one that ends it.
func (c *reviewCap) reading(tokens int) {
	if c == nil || c.limit <= 0 || tokens < c.limit {
		return
	}
	c.mu.Lock()
	if c.tripped {
		c.mu.Unlock()
		return
	}
	c.tokens, c.tripped = tokens, true
	c.mu.Unlock()
	c.stop()
}

// spent reports the reading that stopped this review, and whether it did. The
// caller reads it after the launch returns, so the answer is the ceiling
// rather than whatever the agent printed while being killed.
func (c *reviewCap) spent() (tokens int, tripped bool) {
	if c == nil {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens, c.tripped
}
