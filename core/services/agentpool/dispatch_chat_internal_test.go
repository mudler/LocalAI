package agentpool

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// noopEventBridge stands in for the real bridge in the mode-selection specs
// below, which need every optional dependency present EXCEPT the one under
// test. It publishes nowhere on purpose: nothing here reads what it sent.
type noopEventBridge struct{}

func (noopEventBridge) PublishMessage(_, _, _, _, _ string) error              { return nil }
func (noopEventBridge) PublishStatus(_, _, _ string) error                     { return nil }
func (noopEventBridge) PublishStreamEvent(_, _ string, _ map[string]any) error { return nil }
func (noopEventBridge) RegisterCancel(_ string, _ context.CancelFunc)          {}
func (noopEventBridge) DeregisterCancel(_ string)                              {}

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

// Which mode this service runs agents in, pinned against the dependency the
// mode actually requires rather than against whichever one happened to be
// non-nil.
//
// The gate used to be a nil-check on a message-bus connection that nothing ever
// published on. It gave the right answer for the wrong reason, and it would
// have gone on giving it until the bus was retired: at that moment every
// frontend replica would have started running agents in an in-process pool,
// against a database full of distributed agent state, with no error and no log
// line to say the mode had changed. The bus field is gone, so that particular
// mistake is no longer expressible; this is what keeps the replacement from
// drifting onto another incidental dependency.
//
// Each It below supplies EVERY other optional dependency and withholds only the
// store, which is what makes it discriminating: a gate moved to the auth DB, to
// the skill store or to the event bridge passes a spec that only checks the
// store's presence, and fails these.
var _ = Describe("choosing how a deployment runs its agents", func() {
	It("runs them distributed when there is an agent store", func() {
		svc, err := NewAgentPoolService(&config.ApplicationConfig{}, AgentPoolOptions{
			AgentStore: &agents.AgentStore{},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(svc.runsDistributed()).To(BeTrue(),
			"a deployment with an agent store must run agents distributed; running them in-process would serve every replica its own view of state the store owns")
	})

	It("runs them in process when there is no agent store, however much else is wired", func() {
		svc, err := NewAgentPoolService(&config.ApplicationConfig{}, AgentPoolOptions{
			AuthDB:      &gorm.DB{},
			SkillStore:  &distributed.SkillStore{},
			EventBridge: noopEventBridge{},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(svc.runsDistributed()).To(BeFalse(),
			"the mode was decided by something other than the agent store; distributed mode reads and writes every agent config through that store and cannot run without it")
	})
})
