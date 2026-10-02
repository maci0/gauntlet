// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/evidence"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/prompt"
)

// SuggestConfig asks which reviews apply to a repository, either by an agent
// or, with Only naming internal/evidence, off the tree's own files.
type SuggestConfig struct {
	NoSandbox    bool
	SandboxWrite []string
	Dir          string
	Set          prompt.Set
	Pool         []string     // review names the agent may choose from
	Agents       []agent.Spec // sampled in random order until one answers
	Only         *agent.Spec  // --suggest-agent: try just this one
	Bin          map[string]string
	Timeout      time.Duration
	Log          func(string, ...any)
	// Seed shuffles the agent try order. The caller resolves the run's
	// effective seed once (SeedOrClock) and passes the same number to the
	// schedule, so the seed the journal records replays this step too; zero
	// here still derives one from Now, for a caller with no run seed.
	Seed uint64
	// Now is the clock a zero Seed derives from. Nil means time.Now. The run
	// passes its bus clock, so the fallback reads the same clock as the rest
	// of the run rather than a second wall clock.
	Now func() time.Time
}

// Suggest runs the triage step and returns the reviews it picked: the
// agent's, or the file-signal suggester's when Only names internal/evidence.
//
// An exit code of 0 with unusable output is as much a failure as a nonzero
// exit: the next agent is tried rather than giving up, because the alternative
// is running the entire review set by accident.
func Suggest(ctx context.Context, cfg SuggestConfig) ([]prompt.Suggestion, agent.Spec, error) {
	if err := ctx.Err(); err != nil {
		return nil, agent.Spec{}, err
	}
	if len(cfg.Pool) == 0 {
		return nil, agent.Spec{}, errors.New("no reviews remain after filtering")
	}
	logf := cfg.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}

	// The suggester that is not an agent: internal/evidence, no launch, no tokens.
	if cfg.Only != nil && cfg.Only.Tool == evidence.AgentName {
		spec := *cfg.Only
		logf("Reading %s for review signals (no agent)", filepath.Base(cfg.Dir))
		picked, readErr := evidence.Reviews(cfg.Dir, cfg.Pool, cfg.Set, cfg.Now)
		if readErr != nil {
			logf("Cannot read complete review evidence or history, so suggested reviews "+
				"stay at one pass: %v", readErr)
		}
		if len(picked) == 0 {
			return nil, spec, errors.New("no review matched anything in this tree " +
				"(the file-signal suggester found no signal to go on; name reviews with --reviews, " +
				"or use --suggest-agent to ask a model instead)")
		}
		return picked, spec, nil
	}

	order := make([]agent.Spec, 0, len(cfg.Agents))
	if cfg.Only != nil {
		order = append(order, *cfg.Only)
	} else {
		order = append(order, cfg.Agents...)
		// The same keyed-draw shuffle the runner's review schedule uses, so the
		// suggest step is driven by the one seed the journal records and its
		// order is a pure function of seed and pool size, stable across builds
		// (math/rand's Shuffle is not pinned by anything in-tree).
		seed := SeedOrClock(cfg.Seed, cfg.Now)
		for i := len(order) - 1; i > 0; i-- {
			j := drawIndex(seed, fmt.Sprintf("suggest-shuffle\x00%d", i), i+1)
			order[i], order[j] = order[j], order[i]
		}
	}

	text := prompt.SuggestPrompt(cfg.Set, cfg.Pool)
	var lastErr error
	for _, spec := range order {
		if ctx.Err() != nil {
			return nil, spec, ctx.Err()
		}
		argv, err := agent.BuildCmd(spec, text, agent.BuildOpts{
			Binary:  cfg.Bin[spec.Tool],
			Timeout: cfg.Timeout,
			Dir:     cfg.Dir,
			Now:     cfg.Now,
		})
		if err != nil {
			lastErr = fmt.Errorf("cannot launch %s to suggest reviews: %w", spec.Label(), err)
			logf("%v", lastErr)
			continue
		}
		logf("Asking %s which reviews apply here (timeout %s)", spec.Label(),
			humanize.Duration(cfg.Timeout))

		out, res := captureProc(ctx, procOpts{Argv: argv, Dir: cfg.Dir, Timeout: cfg.Timeout, Tool: spec.Tool, NoSandbox: cfg.NoSandbox, SandboxWrite: cfg.SandboxWrite})
		switch {
		case res.Err != nil:
			lastErr = fmt.Errorf("cannot launch %s to suggest reviews: %w", spec.Label(), res.Err)
		case res.Canceled:
			return nil, spec, context.Canceled
		case res.TimedOut:
			lastErr = errors.New(withNote(
				fmt.Sprintf("%s timed out while suggesting reviews", spec.Label()), out))
		case res.StreamErr != nil:
			// A broken pipe leaves a short RELEVANT: list, and a short list
			// is indistinguishable from a considered one, so the next agent
			// gets a turn rather than the schedule being quietly narrowed.
			// Below the cancel and timeout arms: those close the pipes on
			// purpose, which the reader sees as the same broken pipe.
			lastErr = fmt.Errorf("%s suggested reviews from a stream that ended early: %w",
				spec.Label(), res.StreamErr)
		case res.ExitCode != 0:
			lastErr = errors.New(withNote(
				fmt.Sprintf("%s failed while suggesting reviews (exit %d)", spec.Label(), res.ExitCode), out))
		default:
			picked, unknown, dropped := prompt.ParseSuggestions(out, cfg.Pool)
			if len(unknown) > 0 {
				logf("Ignoring unknown suggestions: %v%s", unknown, droppedNote(dropped))
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

// droppedNote says how many unknown names the report left out, so a list that
// stops at its cap reads as a truncated list rather than as the whole answer.
func droppedNote(dropped int) string {
	if dropped == 0 {
		return ""
	}
	return fmt.Sprintf(" (and %d more)", dropped)
}
