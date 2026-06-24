package distributed_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/syncstate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ftSyncStore adapts the real FineTuneStore to syncstate.Store, exactly as the
// finetune service does in production. Defined here (rather than reusing the
// service's unexported adapter) so the e2e exercises the store + component over
// real infrastructure without pulling in backend execution.
type ftSyncStore struct{ s *distributed.FineTuneStore }

func (a ftSyncStore) List(_ context.Context) ([]*distributed.FineTuneJobRecord, error) {
	recs, err := a.s.ListAll()
	if err != nil {
		return nil, err
	}
	out := make([]*distributed.FineTuneJobRecord, len(recs))
	for i := range recs {
		r := recs[i]
		out[i] = &r
	}
	return out, nil
}

func (a ftSyncStore) Upsert(_ context.Context, r *distributed.FineTuneJobRecord) error {
	return a.s.Upsert(r)
}

func (a ftSyncStore) Delete(_ context.Context, k string) error { return a.s.Delete(k) }

// This suite is the real-infrastructure counterpart to the fake-bus unit tests:
// two SyncedMap instances stand in for two LocalAI frontend replicas, each with
// its OWN NATS connection to a shared NATS server and a SHARED PostgreSQL store -
// the exact distributed-mode invariant (single shared DB, per-replica process
// state). It proves the delta path works over the wire and that a late-joining
// replica recovers via store hydrate (the at-most-once gap a fake bus cannot
// exercise).
var _ = Describe("SyncedMap two-replica sync over real NATS", Label("Distributed"), func() {
	var (
		infra   *TestInfra
		ftStore *distributed.FineTuneStore
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_syncstate_dist_test")

		db, err := gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		ftStore, err = distributed.NewFineTuneStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	// newReplica builds an independent "replica": its own NATS client to the
	// shared server plus a SyncedMap over the shared store, started (hydrate +
	// subscribe) and cleaned up automatically.
	newReplica := func() *syncstate.SyncedMap[string, *distributed.FineTuneJobRecord] {
		GinkgoHelper()
		nc, err := messaging.New(infra.NatsURL)
		Expect(err).ToNot(HaveOccurred())

		sm := syncstate.New(syncstate.Config[string, *distributed.FineTuneJobRecord]{
			Name:  "finetune.jobs",
			Key:   func(r *distributed.FineTuneJobRecord) string { return r.ID },
			Nats:  nc,
			Store: ftSyncStore{s: ftStore},
		})
		Expect(sm.Start(infra.Ctx)).To(Succeed())
		FlushNATS(nc) // ensure the subscription is registered server-side before any publish
		DeferCleanup(func() {
			_ = sm.Close()
			nc.Close()
		})
		return sm
	}

	rec := func(id, status string) *distributed.FineTuneJobRecord {
		return &distributed.FineTuneJobRecord{
			ID: id, UserID: "u1", Model: "m", Backend: "b",
			TrainingType: "lora", TrainingMethod: "sft", Status: status,
		}
	}

	It("propagates a create from replica A to replica B over the wire", func() {
		a := newReplica()
		b := newReplica()

		Expect(a.Set(infra.Ctx, rec("job-1", "queued"))).To(Succeed())

		Eventually(func() bool { _, ok := b.Get("job-1"); return ok }, "10s", "50ms").
			Should(BeTrue(), "replica B must observe the job created on A via NATS")

		got, ok := b.Get("job-1")
		Expect(ok).To(BeTrue())
		Expect(got.Status).To(Equal("queued"))
	})

	It("propagates an update and a delete across replicas", func() {
		a := newReplica()
		b := newReplica()

		Expect(a.Set(infra.Ctx, rec("job-2", "queued"))).To(Succeed())
		Eventually(func() bool { _, ok := b.Get("job-2"); return ok }, "10s", "50ms").Should(BeTrue())

		// Update on A -> B reflects the new status.
		Expect(a.Set(infra.Ctx, rec("job-2", "training"))).To(Succeed())
		Eventually(func() string {
			if r, ok := b.Get("job-2"); ok {
				return r.Status
			}
			return ""
		}, "10s", "50ms").Should(Equal("training"))

		// Delete on A -> B prunes (a reload-from-path could not do this).
		Expect(a.Delete(infra.Ctx, "job-2")).To(Succeed())
		Eventually(func() bool { _, ok := b.Get("job-2"); return ok }, "10s", "50ms").
			Should(BeFalse(), "replica B must drop the job deleted on A")
	})

	It("hydrates a late-joining replica from the shared store (missed-delta recovery)", func() {
		a := newReplica()

		// Written (and broadcast) BEFORE replica C exists, so C can never receive
		// the delta - it can only learn the job by hydrating from shared Postgres
		// on Start. This is the at-most-once gap a fake bus cannot exercise.
		Expect(a.Set(infra.Ctx, rec("job-3", "completed"))).To(Succeed())
		Eventually(func() (*distributed.FineTuneJobRecord, error) { return ftStore.Get("job-3") }, "10s", "50ms").
			ShouldNot(BeNil(), "write-through must reach the shared store first")

		c := newReplica() // joins late; Start() hydrates from the store synchronously

		got, ok := c.Get("job-3")
		Expect(ok).To(BeTrue(), "late replica must recover the job via store hydrate, not a delta")
		Expect(got.Status).To(Equal("completed"))
	})

	It("write-through persists a local Set to the shared PostgreSQL store", func() {
		a := newReplica()

		Expect(a.Set(infra.Ctx, rec("job-4", "queued"))).To(Succeed())

		persisted, err := ftStore.Get("job-4")
		Expect(err).ToNot(HaveOccurred())
		Expect(persisted.ID).To(Equal("job-4"))
		Expect(persisted.Status).To(Equal("queued"))
	})
})
