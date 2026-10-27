package session_test

import (
	"context"
	"testing"
	"time"

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

// The harness tells the chat when it shrinks the model's context; clients are
// promised a compacted event with the token counts.
func TestCompactionReachesTheClient(t *testing.T) {
	m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Harness: compactingHarness{}, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	ctx := context.Background()
	chat, err := m.Open(ctx, workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(ctx, session.Message{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	var got *session.Event
	timeout := time.After(20 * time.Second)
	for done := false; !done; {
		select {
		case e, ok := <-chat.Events():
			if !ok {
				t.Fatal("events closed before turn_end")
			}
			if e.Kind == session.KindCompacted {
				got = &e
			}
			done = e.Kind == session.KindTurnEnd
		case <-timeout:
			t.Fatal("timed out")
		}
	}
	if got == nil {
		t.Fatal("no compacted event reached the client")
	}
	if got.Before != 9000 || got.After != 3000 {
		t.Fatalf("compacted event = %+v, want before 9000 and after 3000", got)
	}
}
