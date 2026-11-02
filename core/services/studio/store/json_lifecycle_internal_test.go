// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("JSON same-ID deletion and recreation", func() {
	ginkgo.It("waits for cleanup without blocking unrelated chats or losing new files", func() {
		ctx := context.Background()
		dir := ginkgo.GinkgoT().TempDir()
		store, err := NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		s := store.(*jsonStore)
		rec := session.ChatRecord{ID: "reused", Principal: "alice", AgentID: "agent", HarnessState: []byte(`{"old":true}`)}
		Expect(s.CreateChat(ctx, rec)).To(Succeed())
		Expect(s.CreateChat(ctx, session.ChatRecord{ID: "unrelated", Principal: "alice"})).To(Succeed())
		_, err = s.AppendEntry(ctx, rec.ID, session.Entry{})
		Expect(err).NotTo(HaveOccurred())
		in := MemoryAcceptance{Principal: "alice", ChatID: rec.ID, SourceID: "old-source", Text: "remember", Policy: CapturePolicy{AgentID: "agent", Destination: "collection", Mode: CaptureIncoming}}
		_, err = s.AcceptMemoryInput(ctx, in)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.FrozenMemory(ctx, "alice", rec.ID, "agent", "call", func() ([]byte, error) { return []byte("old request"), nil })
		Expect(err).NotTo(HaveOccurred())

		cleanup, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		s.removeChatFile = func(path string) error {
			if path == s.file(rec.ID, ".entries") {
				close(cleanup)
				<-release
			}
			return os.Remove(path)
		}
		deleted := make(chan error, 1)
		go func() { deleted <- s.DeleteChat(ctx, "alice", rec.ID) }()
		Eventually(cleanup).Should(BeClosed())
		// The old generation is now a durable tombstone, but its files still exist.
		recreated := make(chan error, 1)
		started := make(chan struct{})
		rec.HarnessState = []byte(`{"new":true}`)
		go func() {
			close(started)
			err := s.CreateChat(ctx, rec)
			if err == nil {
				_, err = s.AppendEntry(ctx, rec.ID, session.Entry{})
			}
			recreated <- err
		}()
		<-started
		unrelated := make(chan error, 1)
		go func() {
			_, err := s.AppendEntry(ctx, "unrelated", session.Entry{})
			if err == nil {
				err = s.CreateChat(ctx, session.ChatRecord{ID: "third", Principal: "alice"})
			}
			unrelated <- err
		}()
		Eventually(unrelated).Should(Receive(Succeed()))
		// The barrier, not a sleep, pins deletion inside actual file cleanup.
		var early bool
		select {
		case err := <-recreated:
			Expect(err).NotTo(HaveOccurred())
			early = true
		case <-time.After(100 * time.Millisecond):
		}
		unblock()
		Eventually(deleted).Should(Receive(Succeed()))
		if !early {
			Eventually(recreated).Should(Receive(Succeed()))
		}
		reopened, err := NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		got, err := reopened.GetChat(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred(), "old cleanup must not remove the new state generation")
		Expect(got.HarnessState).To(Equal(rec.HarnessState))
		raw, err := os.ReadFile(s.file(rec.ID, ".entries"))
		Expect(err).NotTo(HaveOccurred(), "old cleanup must not remove the new transcript")
		Expect(raw).NotTo(BeEmpty())
		_, err = reopened.AcceptMemoryInput(ctx, in)
		Expect(err).To(MatchError(ErrCaptureConflict))
		captures, err := reopened.ListCaptures(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(captures).To(BeEmpty())
		frozen, err := reopened.FrozenMemory(ctx, "alice", rec.ID, "agent", "call", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(frozen).To(BeEmpty())
		Expect(early).To(BeFalse(), "recreation must wait through same-ID cleanup")
	})
})
