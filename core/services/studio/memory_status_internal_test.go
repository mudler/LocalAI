// SPDX-License-Identifier: MIT
package studio

import (
	"encoding/json"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
	"time"
)

var _ = Describe("memory status REST service contract", func() {
	It("scopes history, checks live policy for retry and preserves frozen identity and payload", func() {
		f := newCaptureFixture(false)
		f.backend.fail = true
		f.run("worker", nil)
		f.due()
		f.run("worker", nil)
		f.due()
		f.run("worker", nil)
		original := f.record()
		Expect(original.State).To(Equal("Failed"))
		statuses, err := f.svc.MemoryStatuses(f.ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(statuses[0].RetryEligible).To(BeTrue())
		_, err = f.svc.MemoryStatuses(f.ctx, "bob", "chat")
		Expect(err).To(MatchError(session.ErrNotFound))
		_, err = f.svc.RetryMemoryCapture(f.ctx, "bob", "chat", original.ID)
		Expect(err).To(MatchError(session.ErrNotFound))
		Expect(f.st.CreateChat(f.ctx, session.ChatRecord{ID: "other", Principal: "alice", AgentID: "agent"})).To(Succeed())
		_, err = f.svc.RetryMemoryCapture(f.ctx, "alice", "other", original.ID)
		Expect(err).To(MatchError(store.ErrNoCapture))
		f.policy("off")
		statuses, err = f.svc.MemoryStatuses(f.ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(statuses[0].Status).To(Equal("failed"))
		Expect(statuses[0].RetryEligible).To(BeFalse())
		_, err = f.svc.RetryMemoryCapture(f.ctx, "alice", "chat", original.ID)
		Expect(err).To(MatchError(ErrMemoryDenied))
		f.policy("incoming_only")
		bus := NewLocalBus()
		f.svc.bus = bus
		events, cancel := bus.Subscribe(key("alice", "chat"))
		defer cancel()
		status, err := f.svc.RetryMemoryCapture(f.ctx, "alice", "chat", original.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Status).To(Equal("queued"))
		select {
		case ev := <-events:
			Expect(ev.Kind).To(Equal(session.KindMemoryCapture))
			Expect(ev.IDs).To(Equal([]string{original.ID}))
			Expect(ev.Text).To(BeEmpty())
			Expect(f.record().State).To(Equal("Queued"))
		case <-time.After(time.Second):
			Fail("missing actual bus capture event")
		}
		retried := f.record()
		Expect(retried.Identity).To(Equal(original.Identity))
		Expect(retried.RawText).To(Equal(original.RawText))
		Expect(retried.InputType).To(Equal(original.InputType))
		before, _ := original.ReducerState()
		after, _ := retried.ReducerState()
		Expect(after.(memory.Queued).Job.Input).To(Equal(before.(memory.Failed).Job.Input))
		entries, err := f.st.Entries(f.ctx, "chat", 0, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
		_, err = f.svc.RetryMemoryCapture(f.ctx, "alice", "chat", original.ID)
		Expect(err).To(MatchError(store.ErrCaptureConflict))
		f.backend.fail = false
		f.clock = time.Now()
		f.run("resumed", nil)
		statuses, err = f.svc.MemoryStatuses(f.ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(statuses[0].EntryID).To(Equal("exact-entry"))
		body, err := json.Marshal(statuses)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(body), "original input")).To(BeFalse())
		Expect(string(body)).NotTo(ContainSubstring("alice"))
	})
	It("publishes durable worker transitions on the actual bus without a custom hook", func() {
		f := newCaptureFixture(false)
		f.svc.bus = NewLocalBus()
		events, cancel := f.svc.bus.Subscribe(key("alice", "chat"))
		defer cancel()
		f.run("worker", nil)
		var count int
		for {
			select {
			case ev := <-events:
				Expect(ev.Kind).To(Equal(session.KindMemoryCapture))
				Expect(ev.IDs).To(Equal([]string{f.record().ID}))
				Expect(ev.Text).To(BeEmpty())
				count++
			default:
				Expect(count).To(BeNumerically(">=", 2))
				Expect(f.record().State).To(Equal("Saved"))
				return
			}
		}
	})
})
