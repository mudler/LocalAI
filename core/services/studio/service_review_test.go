package studio_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func newServiceOn(st store.Store, h session.Harness, opts ...func(*studio.Deps)) *studio.Service {
	d := studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h}
	for _, o := range opts {
		o(&d)
	}
	svc, err := studio.New(d)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = svc.Close(context.Background()) })
	return svc
}

var _ = Describe("service review", func() {
	It("a failed release is retried so the chat does not stay running", func() {
		mem, _ := store.NewJSON("")
		fs := &flakyRelease{Store: mem, fail: 2}
		h := studiotest.NewGateHarness()
		svc := newServiceOn(fs, h)
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		h.Gate <- struct{}{}
		until("the turn to end", func() bool { return !turn(svc, alice, id).Running })
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnDone))
	})

	It("a parent is not left waiting when the childs release fails", func() {
		mem, _ := store.NewJSON("")
		fs := &flakyRelease{Store: mem, fail: -1}
		h := &studiotest.ToolHarness{Script: func(a workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"job","wait":true}`}}
			}
			return []studiotest.ToolStep{{Say: "child says hi"}}
		}}
		svc := newServiceOn(fs, h, func(d *studio.Deps) {
			d.HostTools = func(context.Context, workspace.Actor) ([]session.Tool, error) { return nil, nil }
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		until("the parent's spawn_agent call to return", func() bool { return len(h.ResultsFor("spawn_agent")) == 1 })
		res := h.ResultsFor("spawn_agent")[0]
		Expect(res.Err).NotTo(BeNil(), "spawn_agent = %+v, want an error: the child's turn could not be settled", res)
	})
})

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

var _ = Describe("service review", func() {
	It("a failed send closes the chat", func() {
		h := &closeCounting{Harness: studiotest.NewGateHarness()}
		svc := newServiceOn(mustJSON(), h)
		id := create(svc, alice)
		_, err := svc.Send(context.Background(), alice, id, "")
		Expect(err).To(HaveOccurred(), "an empty message should fail")
		Expect(h.starts.Load()).To(BeNumerically("==", 1), "starts=%d closes=%d, want the chat opened once and closed once", h.starts.Load(), h.closes.Load())
		Expect(h.closes.Load()).To(BeNumerically("==", 1), "starts=%d closes=%d, want the chat opened once and closed once", h.starts.Load(), h.closes.Load())
		info := turn(svc, alice, id)
		Expect(info.Running).To(BeFalse(), "turn = %+v, want released as failed", info)
		Expect(info.Last).To(Equal(store.TurnFailed), "turn = %+v, want released as failed", info)
	})
})

func mustJSON() store.Store {
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	return st
}

// A Stop that lands as the turn ends must not open the chat again: a chat left
// open would hold stale state while another frontend runs the next turn.
var _ = Describe("service review", func() {
	It("a stop at the end of a turn does not reopen the chat", func() {
		g := studiotest.NewGateHarness()
		h := &closeCounting{Harness: g}
		svc := newServiceOn(mustJSON(), h)
		id := create(svc, alice)
		const rounds = 150
		for i := 1; i <= rounds; i++ {
			sent(svc, alice, id, "hi")
			until("the harness to get the message", func() bool { return len(g.Sent()) >= i })
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
			until("the turn to end", func() bool { return !turn(svc, alice, id).Running })
		}
		// Each turn opens the chat once; an extra start is a reopen by a late Stop.
		Expect(int(h.starts.Load())).To(Equal(rounds))
	})
})

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

var _ = Describe("service review", func() {
	It("a non fatal error event does not fail the turn", func() {
		svc := newServiceOn(mustJSON(), errorHarness{})
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		until("the turn to end", func() bool { i := turn(svc, alice, id); return !i.Running && i.Last != "" })
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnDone))
	})

	// A message delivered in the middle of a turn belongs to that turn.
	It("a mid turn message is stored with its turn number", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		sent(svc, alice, id, "start")
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		sent(svc, alice, id, "keep me")
		h.Gate <- struct{}{}
		until("the turn to end", func() bool { return !turn(svc, alice, id).Running })
		es, err := svc.Messages(context.Background(), alice, id, 0, 50)
		Expect(err).NotTo(HaveOccurred())
		for _, e := range es {
			if e.Role == session.RoleUser && e.Text == "keep me" {
				Expect(e.Turn).To(BeNumerically("==", 1), "mid-turn message has turn %d, want 1", e.Turn)
				return
			}
		}
		Fail(fmt.Sprintf("the mid-turn message is not in the transcript: %+v", es))
	})
})
