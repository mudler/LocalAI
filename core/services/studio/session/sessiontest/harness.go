package sessiontest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// Step is one action of a scripted turn. Set one field.
type Step struct {
	Say   string          // emit a text event
	Think string          // emit a reasoning event with this text
	Tool  string          // call the session tool with this name ...
	Args  string          // ... with these JSON arguments
	Block <-chan struct{} // wait until the channel closes or the turn ends
	Err   error           // end the turn with this error
	Panic string          // panic with this value
}

// FakeHarness is a session.Harness that follows a script. Turns[i] is the
// script of the i-th Send of a chat; a turn past the end of the script says
// "ok". The fields are for assertions; read them after the turn ends.
type FakeHarness struct {
	Turns [][]Step
	// FailResume makes Start fail when it gets a resume state, as a harness
	// does with a state it cannot read.
	FailResume bool
	// InjectErr, when set, is what every Inject returns, even in a turn. It
	// stands for a harness whose turn is ending.
	InjectErr error
	// StartDelay makes Start wait this long, as a harness that starts a
	// process does.
	StartDelay time.Duration
	// FailNextStarts makes that many of the next Starts fail. Set it while no
	// Start runs.
	FailNextStarts int

	mu         sync.Mutex
	envs       []session.HarnessEnv
	sent       []string
	injected   []string
	interrupts int
	closed     int
}

// Starts returns the environments that Start got.
func (h *FakeHarness) Starts() []session.HarnessEnv {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]session.HarnessEnv(nil), h.envs...)
}

// Sent returns the texts that Send got.
func (h *FakeHarness) Sent() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.sent...)
}

// Injected returns the texts that Inject accepted.
func (h *FakeHarness) Injected() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.injected...)
}

// Interrupts returns how often Interrupt was called.
func (h *FakeHarness) Interrupts() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.interrupts
}

// Closed returns how many sessions were closed.
func (h *FakeHarness) Closed() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

type fakeState struct {
	Turn int `json:"turn"`
}

// Start implements session.Harness.
func (h *FakeHarness) Start(ctx context.Context, env session.HarnessEnv) (session.HarnessSession, error) {
	if h.StartDelay > 0 {
		time.Sleep(h.StartDelay)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.mu.Lock()
	if h.FailNextStarts > 0 {
		h.FailNextStarts--
		h.mu.Unlock()
		return nil, errors.New("fake harness: start failed")
	}
	h.envs = append(h.envs, env)
	h.mu.Unlock()
	s := &fakeSession{h: h, env: env}
	if len(env.Resume) > 0 {
		if h.FailResume {
			return nil, errors.New("fake harness: cannot read the resume state")
		}
		var st fakeState
		if err := json.Unmarshal(env.Resume, &st); err != nil {
			return nil, fmt.Errorf("fake harness: %w", err)
		}
		s.turn = st.Turn
	}
	return s, nil
}

type fakeSession struct {
	h   *FakeHarness
	env session.HarnessEnv

	mu      sync.Mutex
	turn    int
	running bool
}

func (s *fakeSession) tool(name string) (session.Tool, bool) {
	for _, t := range s.env.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return session.Tool{}, false
}

func (s *fakeSession) Send(ctx context.Context, text string) error {
	s.mu.Lock()
	n := s.turn
	s.turn++
	s.running = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
	s.h.mu.Lock()
	s.h.sent = append(s.h.sent, text)
	s.h.mu.Unlock()

	steps := []Step{{Say: "ok"}}
	if n < len(s.h.Turns) {
		steps = s.h.Turns[n]
	}
	for _, st := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch {
		case st.Think != "":
			s.env.Emit(session.Event{Kind: session.KindReasoning, Text: st.Think})
		case st.Say != "":
			s.env.Emit(session.Event{Kind: session.KindText, Text: st.Say})
		case st.Tool != "":
			tl, ok := s.tool(st.Tool)
			if !ok {
				return fmt.Errorf("fake harness: no tool %q", st.Tool)
			}
			args := json.RawMessage(st.Args)
			ev := &session.ToolEvent{Name: st.Tool, Summary: session.Summarize(args)}
			s.env.Emit(session.Event{Kind: session.KindToolStart, Tool: ev})
			out, err := tl.Run(ctx, args)
			end := *ev
			end.OK, end.Output = err == nil, session.CapText(out, 2048)
			if err != nil {
				end.Output = session.CapText(err.Error(), 2048)
			}
			s.env.Emit(session.Event{Kind: session.KindToolEnd, Tool: &end})
		case st.Block != nil:
			select {
			case <-st.Block:
			case <-ctx.Done():
			}
		case st.Err != nil:
			return st.Err
		case st.Panic != "":
			panic(st.Panic)
		}
	}
	return ctx.Err()
}

func (s *fakeSession) Inject(text string) error {
	if s.h.InjectErr != nil {
		return s.h.InjectErr
	}
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if !running {
		return session.ErrNoTurn
	}
	s.h.mu.Lock()
	s.h.injected = append(s.h.injected, text)
	s.h.mu.Unlock()
	return nil
}

func (s *fakeSession) Interrupt() error {
	s.h.mu.Lock()
	s.h.interrupts++
	s.h.mu.Unlock()
	return nil
}

func (s *fakeSession) Export() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.Marshal(fakeState{Turn: s.turn})
}

func (s *fakeSession) Close() error {
	s.h.mu.Lock()
	s.h.closed++
	s.h.mu.Unlock()
	return nil
}
