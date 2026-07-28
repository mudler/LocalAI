package openai

import (
	"sync"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/conncoord"
	"github.com/mudler/xlog"
)

// connSink wires the explicit connection-lifecycle state machine
// (conncoord.Coordinator — machine "M1" in docs/design/realtime-state-machines.md)
// into the realtime session handler.
//
// It replaces the legacy vadServerStarted bool + the `done` channel that was
// reassigned on every turn-detection toggle and closed from two sites (Part 2,
// failure mode 6). The coordinator owns whether the VAD goroutine is running, so
// the per-run done channel is created and closed in lockstep with that one state
// — closed exactly once, never resurrected after teardown.
//
// The connection machine is driven by the single session goroutine (the handler
// loop and its teardown), so this sink and its coordinator are loop-local; the
// Coordinator's lock only keeps State() race-free.
//
// Effects:
//   - StartVAD: create a fresh done channel and spawn handleVAD on it (joined via wg).
//   - StopVAD:  close that done channel.
//   - Teardown: stop the remaining input goroutines (opus decode, sound window),
//     join everything, cancel in-flight responses, and remove the session — once.
type connSink struct {
	session   *Session
	sessionID string
	transport Transport
	wg        *sync.WaitGroup

	coord *conncoord.Coordinator

	// vadDone is the current VAD run's stop signal — recreated on each StartVAD,
	// closed by StopVAD / Teardown. Owned solely by Perform (single goroutine).
	vadDone chan struct{}

	// One-shot stop signals for the other input goroutines, registered by the
	// handler when it starts them; closed once by Teardown.
	decodeDone      chan struct{}
	soundWindowDone chan struct{}
}

func newConnSink(session *Session, sessionID string, t Transport, wg *sync.WaitGroup) *connSink {
	s := &connSink{
		session:   session,
		sessionID: sessionID,
		transport: t,
		wg:        wg,
	}
	s.coord = conncoord.New(s)
	return s
}

// setVAD requests the turn-detection goroutine match active. Idempotent.
func (s *connSink) setVAD(active bool) {
	if err := s.coord.Apply(conncoord.SetVAD{Active: active}); err != nil {
		xlog.Error("conncoord: setVAD failed", "error", err)
	}
}

// close tears the session down (once). Safe to call from multiple exit paths.
func (s *connSink) close() {
	if err := s.coord.Apply(conncoord.Close{}); err != nil {
		xlog.Error("conncoord: close failed", "error", err)
	}
}

// Perform executes one effect. Called by Coordinator.Apply under the coordinator
// lock; the connection coordinator is single-writer and torn down exactly once at
// the end of the session goroutine, so the blocking joins in Teardown never
// contend the lock.
func (s *connSink) Perform(e conncoord.Effect) {
	switch e.(type) {
	case conncoord.StartVAD:
		xlog.Debug("Starting VAD goroutine...")
		s.vadDone = make(chan struct{})
		done := s.vadDone
		s.wg.Go(func() {
			conversation := s.session.Conversations[s.session.DefaultConversationID]
			handleVAD(s.session, conversation, s.transport, done)
		})
	case conncoord.StopVAD:
		xlog.Debug("Stopping VAD goroutine...")
		close(s.vadDone)
		s.vadDone = nil
	case conncoord.Teardown:
		// Tear down in dependency order, driving every child machine to its
		// terminal state so none outlives the session (the hierarchy invariant in
		// formal-verification/session_lifecycle.fizz: conn Torn => children terminal).
		//
		// 1. Stop the remaining input goroutines and join them (this joins the VAD
		//    goroutine, M2, via the StopVAD above + wg).
		if s.decodeDone != nil {
			close(s.decodeDone)
		}
		if s.soundWindowDone != nil {
			close(s.soundWindowDone)
		}
		s.wg.Wait()

		// 2. Terminate the response coordinator (M3): cancel the in-flight response
		//    and join all response goroutines (which also closes their TTS
		//    pipelines, M5). After this no response can start.
		s.session.respSink.shutdown()

		// 3. Terminate every conversation's compaction coordinator (M4): cancel +
		//    join any in-flight summarize+evict so it cannot outlive the session.
		for _, conv := range s.session.Conversations {
			if conv.compaction != nil {
				conv.compaction.shutdown()
			}
		}

		sessionLock.Lock()
		delete(sessions, s.sessionID)
		sessionLock.Unlock()
	}
}
