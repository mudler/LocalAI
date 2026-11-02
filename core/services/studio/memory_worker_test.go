package studio

import (
	"context"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = Describe("memory capture worker lifecycle", func() {
	DescribeTable("publishes statuses with their durable record before subsequent effects", func(deny bool) {
		ctx := context.Background()
		st, err := store.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		mode := "incoming_only"
		if deny {
			mode = "off"
		}
		Expect(as.Put(ctx, agents.Agent{ID: "agent", Principal: "alice", Name: "writer", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "docs"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "docs", ToolStorage: "content", AutomaticIngestion: mode}})).To(Succeed())
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "chat", Principal: "alice", AgentID: "agent"})).To(Succeed())
		_, err = st.AcceptMemoryInput(ctx, store.MemoryAcceptance{Principal: "alice", ChatID: "chat", SourceID: "input", Text: "remember", Policy: store.CapturePolicy{AgentID: "agent", Destination: "docs", Mode: store.CaptureIncoming}})
		Expect(err).NotTo(HaveOccurred())
		clock := time.Now().Truncate(time.Second)
		claim, err := st.ClaimCapture(ctx, "worker", clock, time.Minute)
		Expect(err).NotTo(HaveOccurred())
		backend := &timeoutBackend{complete: func() {}}
		svc := &Service{d: Deps{Store: st, Agents: as, Collections: backend}}
		var statuses []string
		Expect(svc.runCapture(ctx, claim, backend, st.(store.CaptureMemoryStore), func() time.Time { return clock }, func(r store.CaptureRecord, status memory.PublishStatus) {
			// Reads here also guard against publishing under the durable chat lock.
			records, e := st.ListCaptures(ctx, "alice", "chat")
			Expect(e).NotTo(HaveOccurred())
			Expect(r.State).To(Equal(status.Status))
			Expect(records[0].ID).To(Equal(r.ID))
			Expect(records[0].Revision).To(Equal(r.Revision))
			Expect(records[0].State).To(Equal(r.State))
			Expect(records[0].StateJSON).To(Equal(r.StateJSON))
			statuses = append(statuses, status.Status)
		})).To(Succeed())
		Expect(statuses[0]).To(Equal("Authorizing"))
		if deny {
			Expect(statuses).To(Equal([]string{"Authorizing", "Skipped"}))
			Expect(backend.calls).To(BeEmpty())
		} else {
			Expect(statuses[len(statuses)-1]).To(Equal("Saved"))
		}
	}, Entry("grant", false), Entry("deny", true))

	It("returns cancellation before attempting any effects", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s := &Service{}
		Expect(s.ProcessMemoryCaptures(ctx)).To(MatchError(context.Canceled))
	})
})
