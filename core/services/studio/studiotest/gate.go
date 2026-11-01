// Package studiotest has test doubles for the studio service.
package studiotest

import (
	"context"
	"errors"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// GateHarness is a session.Harness whose turns advance only when the test says
// so. Each token sent on Gate is one "model request": the harness asks the host
// for pending input (HarnessEnv.Inputs) at that moment, as nib does before a
// request. A turn makes Requests requests (default 1). When End is set, it then
// waits for a token on End before it finishes, so a test can add input in the
// gap; when End is nil it finishes at once.
type GateHarness struct {
	Requests int           // model requests per turn; 0 means 1
	Gate     chan struct{} // one token per request
	End      chan struct{} // one token to finish a turn; nil: finish at once
	// FailRequest makes that (1-based) request of a turn fail after the input
	// was taken: settle gets an error and the turn returns it. 0: none.
	FailRequest int
	// PauseSteps, when set, makes every turn end as one that reached a step
	// limit of that many steps (a paused event before it returns).
	PauseSteps int

	mu        sync.Mutex
	sent      []string
	delivered [][]string
	settles   []error
	starts    int
	requests  int
	env       session.HarnessEnv
}

// NewGateHarness returns a harness with a buffered Gate and no End.
func NewGateHarness() *GateHarness {
	return &GateHarness{Gate: make(chan struct{}, 64)}
}

// Sent returns the texts of the turns, in order.
func (g *GateHarness) Sent() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.sent...)
}

// Delivered returns the batches that Inputs returned, one entry per request
// that got input.
func (g *GateHarness) Delivered() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]string(nil), g.delivered...)
}

// Settles returns what each settle call got.
func (g *GateHarness) Settles() []error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]error(nil), g.settles...)
}

// Boundaries returns how many model requests the harness has handled.
func (g *GateHarness) Boundaries() int { g.mu.Lock(); defer g.mu.Unlock(); return g.requests }

// Starts returns how many times the host started a session.
func (g *GateHarness) Starts() int { g.mu.Lock(); defer g.mu.Unlock(); return g.starts }

// Env returns what the host gave the last session it started.
func (g *GateHarness) Env() session.HarnessEnv { g.mu.Lock(); defer g.mu.Unlock(); return g.env }

// Start implements session.Harness.
func (g *GateHarness) Start(_ context.Context, env session.HarnessEnv) (session.HarnessSession, error) {
	g.mu.Lock()
	g.starts++
	g.env = env
	g.mu.Unlock()
	return &gateSession{g: g, env: env}, nil
}

type gateSession struct {
	g   *GateHarness
	env session.HarnessEnv

	mu     sync.Mutex
	cancel context.CancelFunc // ends the running turn only
}

func (s *gateSession) Send(ctx context.Context, text string) error {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer cancel()
	g := s.g
	g.mu.Lock()
	g.sent = append(g.sent, text)
	n := g.Requests
	g.mu.Unlock()
	if n == 0 {
		n = 1
	}
	s.env.Emit(session.Event{Kind: session.KindText, Text: "working on: " + text})
	for i := 1; i <= n; i++ {
		if err := s.wait(ctx, g.Gate); err != nil {
			return err
		}
		if s.env.Inputs == nil {
			g.countRequest()
			continue
		}
		texts, settle := s.env.Inputs()
		var failed error
		if len(texts) > 0 {
			g.mu.Lock()
			g.delivered = append(g.delivered, append([]string(nil), texts...))
			g.mu.Unlock()
			if g.FailRequest == i {
				failed = errors.New("model request failed")
			}
		}
		if settle != nil {
			settle(failed)
			g.mu.Lock()
			g.settles = append(g.settles, failed)
			g.mu.Unlock()
		}
		g.countRequest()
		if failed != nil {
			return failed
		}
	}
	if g.End != nil {
		if err := s.wait(ctx, g.End); err != nil {
			return err
		}
	}
	s.env.Emit(session.Event{Kind: session.KindText, Text: "done"})
	if g.PauseSteps > 0 {
		s.env.Emit(session.Event{Kind: session.KindPaused, Steps: g.PauseSteps})
	}
	return nil
}

func (s *gateSession) wait(ctx context.Context, ch chan struct{}) error {
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *gateSession) Inject(string) error { return session.ErrNoTurn }

// Interrupt ends the running turn only; the session stays usable.
func (s *gateSession) Interrupt() error {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *gateSession) Export() ([]byte, error) { return []byte(`{}`), nil }
func (s *gateSession) Close() error            { return s.Interrupt() }

func (g *GateHarness) countRequest() { g.mu.Lock(); g.requests++; g.mu.Unlock() }
