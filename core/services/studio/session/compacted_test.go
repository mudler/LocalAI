package session_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// compactingHarness emits a compaction event and one error event in its turn.
type compactingHarness struct{}

func (compactingHarness) Start(_ context.Context, env session.HarnessEnv) (session.HarnessSession, error) {
	return &compactingSession{env: env}, nil
}

type compactingSession struct{ env session.HarnessEnv }

func (s *compactingSession) Send(context.Context, string) error {
	s.env.Emit(session.Event{Kind: session.KindCompacted, Before: 9000, After: 3000})
	return nil
}
func (s *compactingSession) Inject(string) error     { return session.ErrNoTurn }
func (s *compactingSession) Interrupt() error        { return nil }
func (s *compactingSession) Export() ([]byte, error) { return []byte(`{}`), nil }
func (s *compactingSession) Close() error            { return nil }

var _ = Describe("compaction", func() {
	// The harness tells the chat when it shrinks the model's context; clients are
	// promised a compacted event with the token counts.
	It("reaches the client", func() {
		m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Harness: compactingHarness{}, StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		defer m.Close(context.Background())
		ctx := context.Background()
		chat, err := m.Open(ctx, workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(chat.Send(ctx, session.Message{Text: "hi"})).To(Succeed())
		var got *session.Event
		timeout := time.After(20 * time.Second)
		for done := false; !done; {
			select {
			case e, ok := <-chat.Events():
				Expect(ok).To(BeTrue(), "events closed before turn_end")
				if e.Kind == session.KindCompacted {
					got = &e
				}
				done = e.Kind == session.KindTurnEnd
			case <-timeout:
				Fail("timed out")
			}
		}
		Expect(got).NotTo(BeNil(), "no compacted event reached the client")
		Expect(got.Before).To(BeEquivalentTo(9000), "compacted event = %+v", got)
		Expect(got.After).To(BeEquivalentTo(3000), "compacted event = %+v", got)
	})
})
