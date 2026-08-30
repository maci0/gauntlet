// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !notoktop

package runner

import (
	"context"
	"time"

	"github.com/maci0/toktop/agentusage"
)

// newTranscriptReader follows an agent's session transcript through toktop,
// which is where the per-agent knowledge of those formats is maintained.
func newTranscriptReader(tool, dir string, since time.Time) transcriptReader {
	w := agentusage.Watch(tool, dir, since)
	if w == nil {
		return nopReader{} // this agent keeps nothing readable
	}
	return watcher{w}
}

const transcriptSource = "toktop"

type watcher struct{ w *agentusage.Watcher }

func (t watcher) Run(ctx context.Context, onChange func(output, thinking int)) {
	t.w.Run(ctx, 0, func(s agentusage.Sample) { onChange(s.Output, s.Thinking) })
}

func (t watcher) Final() (int, int) {
	s := t.w.Poll()
	return s.Output, s.Thinking
}
