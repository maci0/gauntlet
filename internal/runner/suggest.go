// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/prompt"
)

// SuggestConfig asks an agent which reviews apply to a repository.
type SuggestConfig struct {
	Dir     string
	Set     prompt.Set
	Pool    []string     // review names the agent may choose from
	Agents  []agent.Spec // sampled in random order until one answers
	Only    *agent.Spec  // --suggest-agent: try just this one
	Bin     map[string]string
	Timeout time.Duration
	Log     func(string, ...any)
	// Seed shuffles the agent try order; zero derives one from the clock, so
	// the same seed replays which agent was asked first.
	Seed uint64
}

// Suggest runs the triage step and returns the reviews the agent picked.
//
// An exit code of 0 with unusable output is as much a failure as a nonzero
// exit: the next agent is tried rather than giving up, because the alternative
// is running the entire review set by accident.
func Suggest(ctx context.Context, cfg SuggestConfig) ([]prompt.Suggestion, agent.Spec, error) {
	if len(cfg.Pool) == 0 {
		return nil, agent.Spec{}, errors.New("no reviews remain after filtering")
	}
	logf := cfg.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}

	order := make([]agent.Spec, 0, len(cfg.Agents))
	if cfg.Only != nil {
		order = append(order, *cfg.Only)
	} else {
		order = append(order, cfg.Agents...)
		rng := rand.New(rand.NewSource(int64(seedOrClock(cfg.Seed))))
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	}

	text := prompt.SuggestPrompt(cfg.Set, cfg.Pool)
	var lastErr error
	for _, spec := range order {
		if ctx.Err() != nil {
			return nil, spec, ctx.Err()
		}
		argv, err := agent.BuildCmd(spec, text, agent.BuildOpts{Binary: cfg.Bin[spec.Tool]})
		if err != nil {
			lastErr = fmt.Errorf("cannot launch %s to suggest reviews: %w", spec.Label(), err)
			logf("%v", lastErr)
			continue
		}
		logf("Asking %s which reviews apply here (timeout %s)", spec.Label(),
			humanize.Duration(cfg.Timeout))

		out, res := captureProc(ctx, argv, cfg.Dir, cfg.Timeout)
		switch {
		case res.Err != nil:
			lastErr = fmt.Errorf("cannot launch %s to suggest reviews: %w", spec.Label(), res.Err)
		case res.Canceled:
			return nil, spec, context.Canceled
		case res.TimedOut:
			lastErr = fmt.Errorf("%s timed out while suggesting reviews", spec.Label())
		case res.ExitCode != 0:
			lastErr = fmt.Errorf("%s failed while suggesting reviews (exit %d)", spec.Label(), res.ExitCode)
		default:
			picked, unknown := prompt.ParseSuggestions(out, cfg.Pool)
			if len(unknown) > 0 {
				logf("Ignoring unknown suggestions: %v", unknown)
			}
			if len(picked) == 0 {
				lastErr = fmt.Errorf("%s printed no usable 'RELEVANT:' lines; "+
					"pick reviews with --reviews if no agent manages", spec.Label())
				break
			}
			return picked, spec, nil
		}
		logf("%v", lastErr)
	}
	if lastErr == nil {
		lastErr = errors.New("no agent could suggest reviews")
	}
	return nil, agent.Spec{}, lastErr
}
