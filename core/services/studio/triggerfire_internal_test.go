// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fireWorker struct {
	mu       sync.Mutex
	requests []workerctl.ChatStartRequest
}

func (w *fireWorker) StartChat(_ context.Context, req workerctl.ChatStartRequest) (*workerctl.ChatStartReply, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.requests = append(w.requests, req)
	return &workerctl.ChatStartReply{Started: true}, nil
}

var _ = Describe("trigger fire delivery identity", func() {
	It("reuses a persisted fire chat and request across host and store reopen, not across fires", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		ts, err := triggers.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		worker := &fireWorker{}
		svc, err := New(Deps{Store: st, Provider: provider.New(), Workers: worker, StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		tr := triggers.Trigger{ID: "trigger", Owner: "alice", Name: "Daily", Model: "m", Prompt: "same prompt", Kind: triggers.KindSchedule}
		Expect(ts.PutTrigger(ctx, tr)).To(Succeed())
		fire := triggers.Run{ID: "fire-1", TriggerID: tr.ID, DueKey: "due-1", Status: triggers.StatusRunning}
		ok, err := ts.Claim(ctx, fire)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		host, err := NewTriggers(svc, TriggerOptions{Store: ts})
		Expect(err).NotTo(HaveOccurred())
		first, err := host.start(ctx, tr, fire)
		Expect(err).NotTo(HaveOccurred())
		host.Close()
		reopened, err := triggers.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		host2, err := NewTriggers(svc, TriggerOptions{Store: reopened})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(host2.Close)
		second, err := host2.start(ctx, tr, fire)
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
		saved, err := reopened.GetRun(ctx, fire.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(saved.ChatID).To(Equal(first))
		// Two frontends start a fresh persisted fire concurrently.
		fire.ID, fire.DueKey = "fire-2", "due-2"
		ok, err = reopened.Claim(ctx, fire)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		host3, err := NewTriggers(svc, TriggerOptions{Store: reopened})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(host3.Close)
		var wg sync.WaitGroup
		ids := make([]string, 2)
		for i, h := range []*Triggers{host2, host3} {
			wg.Add(1)
			go func(i int, h *Triggers) {
				defer GinkgoRecover()
				defer wg.Done()
				id, err := h.start(ctx, tr, fire)
				Expect(err).NotTo(HaveOccurred())
				ids[i] = id
			}(i, h)
		}
		wg.Wait()
		Expect(ids[0]).To(Equal(ids[1]))
		Expect(ids[0]).NotTo(Equal(first))
		fire.ID, fire.DueKey = "fire-3", "due-3"
		ok, err = reopened.Claim(ctx, fire)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		third, err := host2.start(ctx, tr, fire)
		Expect(err).NotTo(HaveOccurred())
		Expect(third).NotTo(Equal(first))
		worker.mu.Lock()
		defer worker.mu.Unlock()
		Expect(worker.requests).To(HaveLen(5))
		Expect(worker.requests[0].RequestID).NotTo(BeEmpty())
		Expect(worker.requests[1].RequestID).To(Equal(worker.requests[0].RequestID))
		Expect(worker.requests[2].RequestID).NotTo(Equal(worker.requests[0].RequestID))
		Expect(worker.requests[3].RequestID).To(Equal(worker.requests[2].RequestID))
		Expect(worker.requests[4].RequestID).NotTo(Equal(worker.requests[0].RequestID))
	})
})
