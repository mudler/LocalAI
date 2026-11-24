package openai

import (
	"context"
	"sync"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/compactcoord"
	"github.com/mudler/xlog"
)

// compactionSink wires the explicit compaction state machine
// (compactcoord.Coordinator — machine "M4" in docs/design/realtime-state-machines.md)
// into a conversation.
//
// It replaces the legacy `compacting atomic.Bool` single-flight guard: the
// coordinator owns whether a compaction is running, so a Trigger while one is
// already in flight is dropped (single-flight) and the background goroutine
// always reports Finished — the flag can never stick (invariant #9).
//
// run is the summarize+evict work for this conversation (captured at
// construction); StartCompaction spawns it and reports Finished when it returns.
// It takes a context derived from the sink's session-scoped ctx, so shutdown()
// can cancel an in-flight compaction.
type compactionSink struct {
	coord  *compactcoord.Coordinator
	run    func(ctx context.Context)
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newCompactionSink(run func(ctx context.Context)) *compactionSink {
	s := &compactionSink{run: run}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.coord = compactcoord.New(s)
	return s
}

// trigger asks the coordinator to start a compaction; a no-op while one is
// already running or after shutdown. Non-blocking.
func (s *compactionSink) trigger() {
	if err := s.coord.Apply(compactcoord.Trigger{}); err != nil {
		xlog.Error("compactcoord: trigger failed", "error", err)
	}
}

// shutdown is called by the connection (M1) parent's teardown: cancel any
// in-flight compaction, join it, then move the coordinator to Terminated so no
// compaction can start afterwards. This closes the legacy gap where the
// fire-and-forget compaction goroutine could outlive the session. Cancelling the
// context first makes the in-flight summarizer Predict return promptly, so the
// join is bounded.
func (s *compactionSink) shutdown() {
	s.cancel()
	s.wg.Wait()
	if err := s.coord.Apply(compactcoord.Shutdown{}); err != nil {
		xlog.Error("compactcoord: shutdown apply failed", "error", err)
	}
}

// Perform executes one effect. Called under the coordinator lock; StartCompaction
// only spawns a goroutine, so it does not block.
func (s *compactionSink) Perform(e compactcoord.Effect) {
	switch e.(type) {
	case compactcoord.StartCompaction:
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				if err := s.coord.Apply(compactcoord.Finished{}); err != nil {
					xlog.Error("compactcoord: finished apply failed", "error", err)
				}
			}()
			if s.run != nil {
				s.run(s.ctx)
			}
		}()
	}
}
