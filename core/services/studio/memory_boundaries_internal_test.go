// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type unavailableCaptureStore struct{ store.Store }

func (s unavailableCaptureStore) AcceptMemoryInput(context.Context, store.MemoryAcceptance) (store.AcceptedInput, error) {
	return store.AcceptedInput{}, errors.New("capture unavailable")
}
func (s unavailableCaptureStore) CompleteMemoryTurn(context.Context, store.MemoryTurnCompletion) error {
	return errors.New("capture unavailable")
}

var _ = Describe("automatic memory payload isolation", func() {
	It("separates notes and attachment paths from accepted pending and direct text", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ag := agents.Agent{ID: "agent", Principal: "alice", Name: "Recorder", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "notes"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "notes", ToolStorage: "content", AutomaticIngestion: "incoming_only"}}
		Expect(as.Put(ctx, ag)).To(Succeed())
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "chat", Principal: "alice", AgentID: "agent", Model: "resolved-model"})).To(Succeed())
		svc, err := New(Deps{Store: st, Agents: as, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		payload := session.ComposeMessage("typed words", "extracted secret", []session.Attachment{{Path: "/private/file"}})
		pending, err := svc.acceptPendingMemory(ctx, "alice", "chat", "typed words", payload)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending.Text).To(Equal(payload))
		Expect(svc.acceptDirectMemory(ctx, "alice", "chat", "direct words")).NotTo(BeEmpty())
		records, err := st.ListCaptures(ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(2))
		for _, r := range records {
			Expect(r.RawText).NotTo(ContainSubstring("secret"))
			Expect(r.RawText).NotTo(ContainSubstring("private"))
		}
		waiting, err := st.Pending(ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(waiting).To(HaveLen(1))
	})
	It("releases a successful answer even if capture completion fails", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())

		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		Expect(as.Put(ctx, agents.Agent{ID: "agent", Principal: "alice", Name: "Recorder", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "notes"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "notes", ToolStorage: "content", AutomaticIngestion: "incoming_only"}})).To(Succeed())
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "chat", Principal: "alice", AgentID: "agent"})).To(Succeed())
		svc, err := New(Deps{Store: unavailableCaptureStore{st}, Agents: as, Provider: provider.New(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		Expect(st.ClaimTurn(ctx, "alice", "chat", "run")).To(Succeed())
		Expect(svc.completeMemoryRun(ctx, "alice", "chat", &run{id: "run", memoryInputs: []string{"memory:input"}, memoryReply: "answer"}, store.TurnDone)).To(Succeed())
		info, err := st.Turn(ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Running).To(BeFalse())
		Expect(info.Last).To(Equal(store.TurnDone))
		p, err := svc.acceptPendingMemory(ctx, "alice", "chat", "still answered", "still answered")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Text).To(Equal("still answered"))
	})
	It("does not accept an internal goal continuation as incoming", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		h := studiotest.NewGateHarness()
		svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.ClaimTurn(ctx, "alice", rec.ID, "run")).To(Succeed())
		Expect(svc.startClaimed(ctx, "alice", rec.ID, "run", session.Message{Text: "internal scheduling", Goal: true})).To(Succeed())
		Eventually(func() int { return len(h.Sent()) }, time.Second*10).Should(Equal(1))
		h.Gate <- struct{}{}
		Eventually(func() bool { info, _ := st.Turn(ctx, "alice", rec.ID); return !info.Running }, time.Second*10).Should(BeTrue())
		records, err := st.ListCaptures(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(BeEmpty())
	})
})
