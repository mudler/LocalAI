package agentpool

// White-box tests (package agentpool) so a spec can build two AgentJobService
// instances sharing one in-memory bus and assert that agent *tasks* converge
// across replicas - the bug this migration fixes (ListTasks used to read
// in-memory only, so a task created on replica A was invisible on replica B).
// Jobs are deliberately untouched here: they already converge via the dispatcher
// + DB read-through.

import (
	"context"
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
func newTaskSyncService(bus messaging.MessagingClient) *AgentJobService {
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
	svc.SetTaskSyncNATS(bus)
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
