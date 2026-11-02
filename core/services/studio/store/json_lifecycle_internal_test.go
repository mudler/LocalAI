// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("JSON same-ID deletion and recreation", func() {
	for _, restart := range []bool{false, true} {
		for _, principal := range []string{"alice", "bob"} {
			ginkgo.It(fmt.Sprintf("finishes failed tombstone cleanup before reuse (restart=%t, principal=%s)", restart, principal), func() {
				ctx := context.Background()
				dir := ginkgo.GinkgoT().TempDir()
				opened, err := NewJSON(dir)
				Expect(err).NotTo(HaveOccurred())
				s := opened.(*jsonStore)
				rec := session.ChatRecord{ID: "reused", Principal: "alice", AgentID: "agent", HarnessState: []byte(`{"alice":"private"}`)}
				Expect(s.CreateChat(ctx, rec)).To(Succeed())
				_, err = s.AppendEntry(ctx, rec.ID, session.Entry{Text: "ALICE private transcript"})
				Expect(err).NotTo(HaveOccurred())
				// A distinct ID with a similar name must not lose any files.
				other := session.ChatRecord{ID: "reused.other", Principal: "alice", HarnessState: []byte(`{"other":true}`)}
				Expect(s.CreateChat(ctx, other)).To(Succeed())
				in := MemoryAcceptance{Principal: "alice", ChatID: rec.ID, SourceID: "old-source", Text: "remember", Policy: CapturePolicy{AgentID: "agent", Destination: "collection", Mode: CaptureIncoming}}
				_, err = s.AcceptMemoryInput(ctx, in)
				Expect(err).NotTo(HaveOccurred())
				// These tombstones must survive cleanup and recreation unchanged.
				s.chats[rec.ID].Capture.DeadKeys = map[string]bool{"old-key": true}
				blocked := errors.New("cleanup unavailable")
				s.removeChatFile = func(string) error { return blocked }
				Expect(s.DeleteChat(ctx, "alice", rec.ID)).To(MatchError(blocked))
				raw, err := os.ReadFile(s.file(rec.ID, ".entries"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(raw)).To(ContainSubstring("ALICE private transcript"))
				dead := cloneCapture(s.chats[rec.ID].Capture)
				if restart {
					opened, err = NewJSON(dir)
					Expect(err).NotTo(HaveOccurred())
					s = opened.(*jsonStore)
				}
				// Covers a crash after the minimal tombstone: it no longer names StateGen.
				residue := s.stateFile(rec.ID, 17)
				Expect(os.WriteFile(residue, []byte(`{"alice":"stray private state"}`), 0600)).To(Succeed())
				s.removeChatFile = nil
				rec.Principal, rec.HarnessState = principal, []byte(`{"fresh":true}`)
				Expect(s.CreateChat(ctx, rec)).To(Succeed())
				Expect(s.chats[rec.ID].Capture).To(Equal(dead))
				_, err = s.AppendEntry(ctx, rec.ID, session.Entry{Text: "fresh transcript"})
				Expect(err).NotTo(HaveOccurred())
				reopened, err := NewJSON(dir)
				Expect(err).NotTo(HaveOccurred())
				entries, err := reopened.Entries(ctx, rec.ID, 0, 100)
				Expect(err).NotTo(HaveOccurred())
				Expect(entries).To(HaveLen(1))
				Expect(entries[0].Text).To(Equal("fresh transcript"))
				states, err := filepath.Glob(s.file(rec.ID, ".*.state"))
				Expect(err).NotTo(HaveOccurred())
				Expect(states).To(ConsistOf(s.stateFile(rec.ID, 1)))
				got, err := reopened.GetChat(ctx, principal, rec.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(got.HarnessState).To(Equal(rec.HarnessState))
				got, err = reopened.GetChat(ctx, "alice", other.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(got.HarnessState).To(Equal(other.HarnessState))
			})
		}
	}
	ginkgo.It("keeps a tombstoned ID unavailable while transcript or state cleanup fails", func() {
		for _, ext := range []string{".entries", ".1.state"} {
			ctx := context.Background()
			dir := ginkgo.GinkgoT().TempDir()
			opened, err := NewJSON(dir)
			Expect(err).NotTo(HaveOccurred())
			s := opened.(*jsonStore)
			rec := session.ChatRecord{ID: "blocked", Principal: "alice", HarnessState: []byte(`{"private":true}`)}
			Expect(s.CreateChat(ctx, rec)).To(Succeed())
			_, err = s.AppendEntry(ctx, rec.ID, session.Entry{Text: "ALICE private transcript"})
			Expect(err).NotTo(HaveOccurred())
			blocked := errors.New("cleanup unavailable")
			fail := func(path string) error {
				if path == s.file(rec.ID, ext) {
					return blocked
				}
				return os.Remove(path)
			}
			s.removeChatFile = fail
			Expect(s.DeleteChat(ctx, "alice", rec.ID)).To(MatchError(blocked))
			opened, err = NewJSON(dir)
			Expect(err).NotTo(HaveOccurred())
			s = opened.(*jsonStore)
			// Startup may best-effort remove stale state; restore the failed residue.
			Expect(os.WriteFile(s.file(rec.ID, ext), []byte("private residue"), 0600)).To(Succeed())
			s.removeChatFile = fail
			rec.Principal = "bob"
			Expect(s.CreateChat(ctx, rec)).To(MatchError(blocked))
			_, err = s.GetChat(ctx, "bob", rec.ID)
			Expect(err).To(MatchError(session.ErrNotFound))
			reopened, err := NewJSON(dir)
			Expect(err).NotTo(HaveOccurred())
			_, err = reopened.GetChat(ctx, "bob", rec.ID)
			Expect(err).To(MatchError(session.ErrNotFound))
		}
	})

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
