package studio_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// flakyRelease fails ReleaseTurn the first fail times (all times when fail is
// negative), the way a locked database does.
type flakyRelease struct {
	store.Store
	fail  int32
	calls atomic.Int32
}

func (f *flakyRelease) ReleaseTurn(ctx context.Context, p, id, run string, st store.TurnStatus) error {
	n := f.calls.Add(1)
	if f.fail < 0 || n <= f.fail {
		return errors.New("database is locked")
	}
	return f.Store.ReleaseTurn(ctx, p, id, run, st)
}

func newServiceOn(t *testing.T, st store.Store, h session.Harness, opts ...func(*studio.Deps)) *studio.Service {
	t.Helper()
	d := studio.Deps{Store: st, Provider: provider.New(), StateDir: t.TempDir(), Harness: h}
	for _, o := range opts {
		o(&d)
	}
	svc, err := studio.New(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	return svc
}

func TestAFailedReleaseIsRetriedSoTheChatDoesNotStayRunning(t *testing.T) {
	mem, _ := store.NewJSON("")
	fs := &flakyRelease{Store: mem, fail: 2}
	h := studiotest.NewGateHarness()
	svc := newServiceOn(t, fs, h)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hi")
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	h.Gate <- struct{}{}
	until(t, "the turn to end", func() bool { return !turn(t, svc, alice, id).Running })
	if got := turn(t, svc, alice, id).Last; got != store.TurnDone {
		t.Fatalf("turn ended %q, want done", got)
	}
}

func TestAParentIsNotLeftWaitingWhenTheChildsReleaseFails(t *testing.T) {
	mem, _ := store.NewJSON("")
	fs := &flakyRelease{Store: mem, fail: -1}
	h := &studiotest.ToolHarness{Script: func(a workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"job","wait":true}`}}
		}
		return []studiotest.ToolStep{{Say: "child says hi"}}
	}}
	svc := newServiceOn(t, fs, h, func(d *studio.Deps) {
		d.HostTools = func(context.Context, workspace.Actor) ([]session.Tool, error) { return nil, nil }
	})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	until(t, "the parent's spawn_agent call to return", func() bool { return len(h.ResultsFor("spawn_agent")) == 1 })
	if res := h.ResultsFor("spawn_agent")[0]; res.Err == nil {
		t.Fatalf("spawn_agent = %+v, want an error: the child's turn could not be settled", res)
	}
}

// closeCounting wraps a harness and counts started and closed sessions.
type closeCounting struct {
	session.Harness
	starts, closes atomic.Int32
}

func (c *closeCounting) Start(ctx context.Context, env session.HarnessEnv) (session.HarnessSession, error) {
	hs, err := c.Harness.Start(ctx, env)
	if err != nil {
		return nil, err
	}
	c.starts.Add(1)
	return &countedSession{HarnessSession: hs, c: c}, nil
}

type countedSession struct {
	session.HarnessSession
	c *closeCounting
}

func (s *countedSession) Close() error { s.c.closes.Add(1); return s.HarnessSession.Close() }

func TestAFailedSendClosesTheChat(t *testing.T) {
	h := &closeCounting{Harness: studiotest.NewGateHarness()}
	svc := newServiceOn(t, mustJSON(t), h)
	id := create(t, svc, alice)
	if _, err := svc.Send(context.Background(), alice, id, ""); err == nil {
		t.Fatal("an empty message should fail")
	}
	if h.starts.Load() != 1 || h.closes.Load() != 1 {
		t.Fatalf("starts=%d closes=%d, want the chat opened once and closed once", h.starts.Load(), h.closes.Load())
	}
	if info := turn(t, svc, alice, id); info.Running || info.Last != store.TurnFailed {
		t.Fatalf("turn = %+v, want released as failed", info)
	}
}

func mustJSON(t *testing.T) store.Store {
	t.Helper()
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// A Stop that lands as the turn ends must not open the chat again: a chat left
// open would hold stale state while another frontend runs the next turn.
func TestAStopAtTheEndOfATurnDoesNotReopenTheChat(t *testing.T) {
	g := studiotest.NewGateHarness()
	h := &closeCounting{Harness: g}
	svc := newServiceOn(t, mustJSON(t), h)
	id := create(t, svc, alice)
	const rounds = 150
	for i := 1; i <= rounds; i++ {
		sent(t, svc, alice, id, "hi")
		until(t, "the harness to get the message", func() bool { return len(g.Sent()) >= i })
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); g.Gate <- struct{}{} }()
		for k := 0; k < 4; k++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 5; j++ {
					_ = svc.Stop(context.Background(), alice, id)
				}
			}()
		}
		wg.Wait()
		until(t, "the turn to end", func() bool { return !turn(t, svc, alice, id).Running })
	}
	// Each turn opens the chat once; an extra start is a reopen by a late Stop.
	if got := int(h.starts.Load()); got != rounds {
		t.Fatalf("harness sessions started = %d for %d turns: a Stop reopened the chat", got, rounds)
	}
}

// errorHarness emits a non-fatal error event and then finishes normally.
type errorHarness struct{}

func (errorHarness) Start(_ context.Context, env session.HarnessEnv) (session.HarnessSession, error) {
	return &errorSession{env: env}, nil
}

type errorSession struct{ env session.HarnessEnv }

func (s *errorSession) Send(context.Context, string) error {
	s.env.Emit(session.Event{Kind: session.KindError, Text: "something small went wrong"})
	s.env.Emit(session.Event{Kind: session.KindText, Text: "fine"})
	return nil
}
func (s *errorSession) Inject(string) error     { return session.ErrNoTurn }
func (s *errorSession) Interrupt() error        { return nil }
func (s *errorSession) Export() ([]byte, error) { return []byte(`{}`), nil }
func (s *errorSession) Close() error            { return nil }

func TestANonFatalErrorEventDoesNotFailTheTurn(t *testing.T) {
	svc := newServiceOn(t, mustJSON(t), errorHarness{})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hi")
	until(t, "the turn to end", func() bool { i := turn(t, svc, alice, id); return !i.Running && i.Last != "" })
	if got := turn(t, svc, alice, id).Last; got != store.TurnDone {
		t.Fatalf("turn ended %q, want done: the error did not end the turn", got)
	}
}

// A message delivered in the middle of a turn belongs to that turn.
func TestAMidTurnMessageIsStoredWithItsTurnNumber(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newService(t, h)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "start")
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	sent(t, svc, alice, id, "keep me")
	h.Gate <- struct{}{}
	until(t, "the turn to end", func() bool { return !turn(t, svc, alice, id).Running })
	es, err := svc.Messages(context.Background(), alice, id, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Role == session.RoleUser && e.Text == "keep me" {
			if e.Turn != 1 {
				t.Fatalf("mid-turn message has turn %d, want 1", e.Turn)
			}
			return
		}
	}
	t.Fatalf("the mid-turn message is not in the transcript: %+v", es)
}
