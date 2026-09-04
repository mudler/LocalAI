package agentpool

// White-box tests (package agentpool) so a spec can build two AgentJobService
// instances sharing one in-memory bus and assert that agent *tasks* converge
// across replicas - the bug this migration fixes (ListTasks used to read
// in-memory only, so a task created on replica A was invisible on replica B).
// Jobs are deliberately untouched here: they already converge via the dispatcher
// + DB read-through.

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/syncstate"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/system"
)

// newTaskSyncService builds an AgentJobService wired to the given bus and a
// throwaway data dir (so the file persister has somewhere to write). Model/config
// loaders are nil because the task sync paths under test never touch them.
func newTaskSyncService(bus messaging.Broadcaster) *AgentJobService {
	tmpDir := GinkgoT().TempDir()
	sysState := &system.SystemState{}
	sysState.Model.ModelsPath = tmpDir
	appConfig := config.NewApplicationConfig(
		config.WithDynamicConfigDir(tmpDir),
		config.WithContext(context.Background()),
	)
	appConfig.SystemState = sysState

	svc := NewAgentJobServiceWithPaths(appConfig, nil, nil, nil,
		// Distinct per-replica files so the file persister write-through never
		// crosses replicas: convergence here must be proven via the bus alone.
		tmpDir+"/tasks.json", tmpDir+"/jobs.json")
	svc.SetTaskSyncBus(bus)
	return svc
}

var _ = Describe("AgentJobService task cross-replica sync", func() {
	Describe("two replicas sharing one bus", func() {
		var (
			bus  *testutil.FakeBus
			a, b *AgentJobService
		)

		BeforeEach(func() {
			// One shared bus, two replicas: exactly the distributed topology where a
			// round-robin request may land on a replica that did not originate the
			// change.
			bus = testutil.NewFakeBus()
			a = newTaskSyncService(bus)
			b = newTaskSyncService(bus)
			// Start hydrates (empty here) and subscribes both replicas to deltas.
			Expect(a.Start(context.Background())).To(Succeed())
			Expect(b.Start(context.Background())).To(Succeed())
		})

		AfterEach(func() {
			Expect(a.Stop()).To(Succeed())
			Expect(b.Stop()).To(Succeed())
		})

		It("makes a task created on A visible via B's GetTask and ListTasks", func() {
			id, err := a.CreateTask(schema.Task{Name: "Shared", Model: "m", Prompt: "p"})
			Expect(err).NotTo(HaveOccurred())

			got, err := b.GetTask(id)
			Expect(err).NotTo(HaveOccurred(), "B must see a task A just created")
			Expect(got.Name).To(Equal("Shared"))

			listed := b.ListTasks()
			Expect(listed).To(HaveLen(1))
			Expect(listed[0].ID).To(Equal(id))
		})

		It("propagates a task update from A to B", func() {
			id, err := a.CreateTask(schema.Task{Name: "Before", Model: "m", Prompt: "p"})
			Expect(err).NotTo(HaveOccurred())

			Expect(a.UpdateTask(id, schema.Task{Name: "After", Model: "m", Prompt: "p"})).To(Succeed())

			got, err := b.GetTask(id)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("After"), "an update on A must be visible on B")
		})

		It("removes a task from B when it is deleted on A", func() {
			id, err := a.CreateTask(schema.Task{Name: "Doomed", Model: "m", Prompt: "p"})
			Expect(err).NotTo(HaveOccurred())
			_, err = b.GetTask(id)
			Expect(err).NotTo(HaveOccurred(), "precondition: B must have the task before the delete")

			Expect(a.DeleteTask(id)).To(Succeed())

			_, err = b.GetTask(id)
			Expect(err).To(HaveOccurred(), "a delete on A must remove the task from B")
			Expect(b.ListTasks()).To(BeEmpty())
		})

		It("does not re-broadcast a delta it received (echo-loop guard)", func() {
			subject := messaging.SubjectSyncStateDelta("agent.tasks")

			_, err := a.CreateTask(schema.Task{Name: "Once", Model: "m", Prompt: "p"})
			Expect(err).NotTo(HaveOccurred())

			// Exactly one publish: A's create. B applies it without re-publishing,
			// otherwise this would be 2+ and a real bus would storm.
			Expect(bus.PublishCount(subject)).To(Equal(1))
		})
	})

	Describe("ListTasks ordering and scoping", func() {
		var svc *AgentJobService

		BeforeEach(func() {
			svc = newTaskSyncService(testutil.NewFakeBus())
			Expect(svc.Start(context.Background())).To(Succeed())
		})
		AfterEach(func() { Expect(svc.Stop()).To(Succeed()) })

		It("sorts newest-first, breaking ties by name", func() {
			// CreateTask stamps CreatedAt with time.Now(); space them out so ordering
			// is deterministic rather than relying on the sub-millisecond gap.
			oldID, err := svc.CreateTask(schema.Task{Name: "Old", Model: "m", Prompt: "p"})
			Expect(err).NotTo(HaveOccurred())
			time.Sleep(5 * time.Millisecond)
			newID, err := svc.CreateTask(schema.Task{Name: "New", Model: "m", Prompt: "p"})
			Expect(err).NotTo(HaveOccurred())

			listed := svc.ListTasks()
			Expect(listed).To(HaveLen(2))
			Expect(listed[0].ID).To(Equal(newID), "newest first")
			Expect(listed[1].ID).To(Equal(oldID))
		})
	})

	Describe("compile-time adapter contract", func() {
		It("satisfies syncstate.Store for tasks", func() {
			// Mirrors the var assertion in task_syncstore.go; keeps the type
			// referenced from a spec so drift surfaces here too.
			var _ syncstate.Store[string, schema.Task] = (*taskStoreAdapter)(nil)
			Expect(&taskStoreAdapter{}).ToNot(BeNil())
		})
	})
})

// newTaskSyncServiceForTenant builds a per-user AgentJobService the way
// UserServicesManager.GetJobs does: a user id, then the shared bus. Each
// service gets its own task file so nothing converges except over the bus.
func newTaskSyncServiceForTenant(bus messaging.Broadcaster, userID string) *AgentJobService {
	tmpDir := GinkgoT().TempDir()
	sysState := &system.SystemState{}
	sysState.Model.ModelsPath = tmpDir
	appConfig := config.NewApplicationConfig(
		config.WithDynamicConfigDir(tmpDir),
		config.WithContext(context.Background()),
	)
	appConfig.SystemState = sysState

	svc := NewAgentJobServiceWithPaths(appConfig, nil, nil, nil,
		tmpDir+"/tasks.json", tmpDir+"/jobs.json")
	svc.SetUserID(userID)
	svc.SetTaskSyncBus(bus)
	return svc
}

// recordingTaskPersister records what DeleteTask was asked to delete and for
// whom, then delegates. The in-memory map is NOT the authority on ownership -
// the store is - so the argument the delete carries has to be pinned on its own
// rather than inferred from a map lookup that never happens once the subject is
// scoped.
type recordingTaskPersister struct {
	JobPersister
	mu    sync.Mutex
	calls []taskDeleteCall
}

type taskDeleteCall struct {
	userID string
	taskID string
}

func (p *recordingTaskPersister) DeleteTask(userID, taskID string) error {
	p.mu.Lock()
	p.calls = append(p.calls, taskDeleteCall{userID: userID, taskID: taskID})
	p.mu.Unlock()
	return p.JobPersister.DeleteTask(userID, taskID)
}

func (p *recordingTaskPersister) recorded() []taskDeleteCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]taskDeleteCall(nil), p.calls...)
}

var _ = Describe("AgentJobService per-tenant task isolation", func() {
	var (
		bus    *testutil.FakeBus
		u1, u2 *AgentJobService
	)

	BeforeEach(func() {
		// One bus, two tenants: the topology every distributed deployment has,
		// where UserServicesManager builds one AgentJobService per user id and
		// every one of them talks over the same carrier.
		bus = testutil.NewFakeBus()
		u1 = newTaskSyncServiceForTenant(bus, "u1")
		u2 = newTaskSyncServiceForTenant(bus, "u2")
		Expect(u1.Start(context.Background())).To(Succeed())
		Expect(u2.Start(context.Background())).To(Succeed())
	})

	AfterEach(func() {
		Expect(u1.Stop()).To(Succeed())
		Expect(u2.Stop()).To(Succeed())
	})

	It("does not put tenant u1's task into tenant u2's list", func() {
		id, err := u1.CreateTask(schema.Task{Name: "Private", Model: "m", Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())

		Expect(u2.ListTasks()).To(BeEmpty(), "tenant u2 must not see tenant u1's task")
		_, err = u2.GetTask(id)
		Expect(err).To(MatchError(ErrTaskNotFound))

		Expect(u1.ListTasks()).To(HaveLen(1), "the owning tenant still has its own task")
	})

	It("does not let tenant u1's delete remove tenant u2's task", func() {
		idU2, err := u2.CreateTask(schema.Task{Name: "Theirs", Model: "m", Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())
		idU1, err := u1.CreateTask(schema.Task{Name: "Mine", Model: "m", Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())

		Expect(u1.DeleteTask(idU1)).To(Succeed())

		_, err = u2.GetTask(idU2)
		Expect(err).NotTo(HaveOccurred(), "tenant u2's task must survive tenant u1's delete")
	})

	It("publishes on the tenant's own subject and not the cluster-wide one", func() {
		// The wiring pin. buildTasksMap declaring PerTenant/Tenant is a struct
		// literal a refactor can drop without breaking the build, so the
		// subject it produces is asserted by name here.
		_, err := u1.CreateTask(schema.Task{Name: "Named", Model: "m", Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())

		Expect(bus.PublishCount(messaging.SubjectSyncStateTenantDelta("agent.tasks", "u1"))).To(Equal(1))
		Expect(bus.PublishCount(messaging.SubjectSyncStateDelta("agent.tasks"))).To(Equal(0))
	})

	It("passes the owning tenant to the persister on delete", func() {
		rec := &recordingTaskPersister{JobPersister: u1.persister}
		u1.persister = rec

		id, err := u1.CreateTask(schema.Task{Name: "Scoped", Model: "m", Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())
		Expect(u1.DeleteTask(id)).To(Succeed())

		Expect(rec.recorded()).To(Equal([]taskDeleteCall{{userID: "u1", taskID: id}}),
			"an unscoped delete reaches every tenant's row with the same primary key")
	})
})

var _ = Describe("AgentJobService same-tenant replicas", func() {
	It("converges two replicas of the same tenant", func() {
		// The positive half. Scoping the subject must not cost the feature the
		// map exists for: two frontend replicas serving the SAME user still
		// have to see each other's writes.
		bus := testutil.NewFakeBus()
		a := newTaskSyncServiceForTenant(bus, "u1")
		b := newTaskSyncServiceForTenant(bus, "u1")
		Expect(a.Start(context.Background())).To(Succeed())
		Expect(b.Start(context.Background())).To(Succeed())
		defer func() {
			Expect(a.Stop()).To(Succeed())
			Expect(b.Stop()).To(Succeed())
		}()

		id, err := a.CreateTask(schema.Task{Name: "Shared", Model: "m", Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())

		got, err := b.GetTask(id)
		Expect(err).NotTo(HaveOccurred(), "the other replica of the same tenant must see the task")
		Expect(got.Name).To(Equal("Shared"))
	})

	It("publishes the cluster-wide view on the carrier SetTaskSyncBus was handed", func() {
		// S3 in the wiring table. Two startup paths call this one setter
		// (startup.go and the settings-driven restart in agent_jobs.go), so the
		// service must broadcast on whatever it was given and on the agent-task
		// family's own subject, never on another family's.
		bus := testutil.NewFakeBus()
		svc := newTaskSyncService(bus)
		Expect(svc.Start(context.Background())).To(Succeed())
		defer func() { Expect(svc.Stop()).To(Succeed()) }()

		_, err := svc.CreateTask(schema.Task{Name: "Cluster", Model: "m", Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())

		Expect(bus.PublishCount(messaging.SubjectSyncStateDelta("agent.tasks"))).To(Equal(1))
		Expect(bus.PublishCount(messaging.SubjectSyncStateDelta("finetune.jobs"))).To(Equal(0))
	})

	It("broadcasts nothing when it is handed no carrier at all", func() {
		// The standalone half of S3: a single-binary deployment passes nil and
		// must get a map that still works and never reaches for a carrier.
		bus := testutil.NewFakeBus()
		svc := newTaskSyncService(nil)
		Expect(svc.Start(context.Background())).To(Succeed())
		defer func() { Expect(svc.Stop()).To(Succeed()) }()

		id, err := svc.CreateTask(schema.Task{Name: "Solo", Model: "m", Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.GetTask(id)
		Expect(err).NotTo(HaveOccurred(), "a carrier-less service must still serve its own reads")
		Expect(bus.Subscribers()).To(Equal(0))
	})

	It("scopes the subject whichever order the setters are called in", func() {
		// UserServicesManager.GetJobs happens to call SetUserID before
		// SetTaskSyncBus. Nothing enforces that order, and a map built from a
		// user id that had not been set yet publishes cluster-wide - the leak,
		// reintroduced by a line move.
		bus := testutil.NewFakeBus()
		svc := newTaskSyncService(bus)
		svc.SetUserID("u1")
		Expect(svc.Start(context.Background())).To(Succeed())
		defer func() { Expect(svc.Stop()).To(Succeed()) }()

		_, err := svc.CreateTask(schema.Task{Name: "Ordered", Model: "m", Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())

		Expect(bus.PublishCount(messaging.SubjectSyncStateTenantDelta("agent.tasks", "u1"))).To(Equal(1))
		Expect(bus.PublishCount(messaging.SubjectSyncStateDelta("agent.tasks"))).To(Equal(0))
	})
})

// The per-user manager is the wiring site that fails silently.
//
// S4 in the table, and it is the reason the table has five rows rather than
// four: the global AgentJobService and every per-user one are different
// objects, so a carrier handed only to the global service leaves every tenant's
// tasks unreplicated with nothing failing anywhere. The manager stores the
// carrier once and every service it builds afterwards inherits it.
var _ = Describe("UserServicesManager task carrier propagation", func() {
	newManager := func() *UserServicesManager {
		GinkgoHelper()
		tmpDir := GinkgoT().TempDir()
		sysState := &system.SystemState{}
		sysState.Model.ModelsPath = tmpDir
		appConfig := config.NewApplicationConfig(
			config.WithDynamicConfigDir(tmpDir),
			config.WithContext(context.Background()),
		)
		appConfig.SystemState = sysState
		return NewUserServicesManager(NewUserScopedStorage(tmpDir, tmpDir), appConfig, nil, nil, nil)
	}

	It("hands the carrier it was given to each per-user service, on that tenant's subject", func() {
		bus := testutil.NewFakeBus()
		m := newManager()
		m.SetJobSyncBus(bus)

		svc, err := m.GetJobs("u1")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(svc.Stop()).To(Succeed()) })

		_, err = svc.CreateTask(schema.Task{Name: "Tenant", Model: "m", Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())

		Expect(bus.PublishCount(messaging.SubjectSyncStateTenantDelta("agent.tasks", "u1"))).To(Equal(1),
			"a per-user service that inherited no carrier leaves that tenant unreplicated, silently")
		Expect(bus.PublishCount(messaging.SubjectSyncStateDelta("agent.tasks"))).To(Equal(0),
			"and one that inherited it must not publish a tenant's tasks cluster-wide")
	})

	It("leaves per-user services carrier-less when the manager was given none", func() {
		bus := testutil.NewFakeBus()
		m := newManager()

		svc, err := m.GetJobs("u2")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(svc.Stop()).To(Succeed()) })

		_, err = svc.CreateTask(schema.Task{Name: "Tenant", Model: "m", Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())
		Expect(bus.Subscribers()).To(Equal(0))
	})
})
