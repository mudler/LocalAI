package agentpool

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The third producer of a claim row, pinned separately from the other two.
//
// A rule stated at three call sites and pinned at two is how the third comes to
// keep publishing onto a subject that no longer exists, or to write a row of
// the wrong kind that no dispatcher serves. The other two are
// jobs.Dispatcher.Enqueue and agents.AgentScheduler.runDueAgents.
var _ = Describe("Dispatching an agent chat in distributed mode", func() {
	var (
		svc *AgentPoolService
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		db := testutil.SetupTestDB()
		Expect(jobs.MigrateClaims(ctx, db)).To(Succeed())

		store, err := agents.NewAgentStore(db)
		Expect(err).ToNot(HaveOccurred())
		cfgJSON, err := json.Marshal(agents.AgentConfig{Name: "helper", Model: "m1"})
		Expect(err).ToNot(HaveOccurred())
		Expect(store.SaveConfig(&agents.AgentConfigRecord{
			UserID: "u1", Name: "helper", ConfigJSON: string(cfgJSON), Status: agents.StatusActive,
		})).To(Succeed())

		svc = &AgentPoolService{
			users:       userManager{authDB: db},
			distributed: distributedBridge{agentStore: store},
		}
	})

	It("writes an agent-run claim carrying the agent's config, rather than publishing", func() {
		messageID, err := svc.dispatchChat("u1", "helper", "hello")
		Expect(err).ToNot(HaveOccurred())
		Expect(messageID).ToNot(BeEmpty())

		var rows []jobs.WorkClaim
		Expect(svc.users.authDB.Find(&rows).Error).To(Succeed())
		Expect(rows).To(HaveLen(1), "the chat must leave a row: a publish onto a queue group nobody joined is accepted and dropped")
		Expect(rows[0].Kind).To(Equal(string(jobs.ClaimKindAgentRun)))

		var evt agents.AgentChatEvent
		Expect(json.Unmarshal(rows[0].Payload, &evt)).To(Succeed())
		Expect(evt.AgentName).To(Equal("helper"))
		Expect(evt.UserID).To(Equal("u1"))
		Expect(evt.Message).To(Equal("hello"))
		Expect(evt.MessageID).To(Equal(messageID))
		// The worker has no database, so everything it needs travels with the
		// work, exactly as it did in the enriched bus payload.
		Expect(evt.Config).ToNot(BeNil())
		Expect(evt.Config.Model).To(Equal("m1"))
	})

	It("refuses a chat for an agent that does not exist, rather than queueing work nothing can run", func() {
		_, err := svc.dispatchChat("u1", "no-such-agent", "hello")
		Expect(err).To(HaveOccurred())

		var n int64
		Expect(svc.users.authDB.Model(&jobs.WorkClaim{}).Count(&n).Error).To(Succeed())
		Expect(n).To(BeZero())
	})
})
