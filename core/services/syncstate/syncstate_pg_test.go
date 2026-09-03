// SPDX-License-Identifier: MIT

package syncstate_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/syncstate"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// ftAdapter is the same bridge the fine-tune service builds, spelled here so
// this file exercises the component over the REAL durable source rather than
// over a map that cannot fail the way a table fails.
type ftAdapter struct{ s *distributed.FineTuneStore }

func (a ftAdapter) List(_ context.Context) ([]*distributed.FineTuneJobRecord, error) {
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

func (a ftAdapter) Upsert(_ context.Context, r *distributed.FineTuneJobRecord) error {
	return a.s.Upsert(r)
}

func (a ftAdapter) Delete(_ context.Context, k string) error { return a.s.Delete(k) }

// This file is the carriage proof, and it is on a real PostgreSQL for a reason
// the doubles cannot cover.
//
// A FakeBus delivers synchronously, in process, with no payload limit and no
// connection to lose. The three things this component's correctness actually
// rests on in distributed mode are exactly the three it cannot express: a
// notification bigger than PostgreSQL's 8000-byte payload cap has to spill to a
// row and come back byte identical; two maps in different families share ONE
// LISTEN channel, so separation is a filter decision and not a channel
// decision; and a listener whose session is terminated has to come back and
// re-hydrate, because every delta published while it was gone reached the
// replicas that were connected and nobody else.
//
// That last one is the invariant. A delta that was dropped or never delivered
// must not be able to read as a state that was never set: the notification says
// only that something changed, and the table is what says what it changed to.
var _ = Describe("SyncedMap on the PostgreSQL broadcast carrier", func() {
	var (
		db  *gorm.DB
		dsn string
	)

	BeforeEach(func() {
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
	})

	// newBus is one replica's end of the carrier: its own pinned LISTEN
	// connection, so a spec can drop one replica's session without touching the
	// peer it is asserting against.
	newBus := func() *pgbus.Bus {
		GinkgoHelper()
		b, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(b.Close)
		return b
	}

	// terminate drops exactly this carrier's LISTEN session, matched on the
	// application name it reports in pg_stat_activity, so the peer bus in the
	// same spec stays up.
	terminate := func(b *pgbus.Bus) {
		GinkgoHelper()
		var killed int64
		Expect(db.Raw(
			"SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE application_name = ?",
			b.ApplicationName(),
		).Scan(&killed).Error).To(Succeed())
		Expect(killed).To(BeNumerically("==", 1), "expected exactly one listener session to drop")
	}

	Describe("two replicas over one shared store", func() {
		var (
			ftStore *distributed.FineTuneStore
			busA    *pgbus.Bus
			busB    *pgbus.Bus
			a, b    *syncstate.SyncedMap[string, *distributed.FineTuneJobRecord]
		)

		newMap := func(bus *pgbus.Bus) *syncstate.SyncedMap[string, *distributed.FineTuneJobRecord] {
			GinkgoHelper()
			m := syncstate.New(syncstate.Config[string, *distributed.FineTuneJobRecord]{
				Name: "finetune.jobs",
				Key:  func(r *distributed.FineTuneJobRecord) string { return r.ID },
				// The carrier goes in as messaging.Broadcaster. Before this
				// change the field was typed as the NATS client, so this line
				// did not compile and the durable re-hydrate leg below had no
				// consumer that could reach it.
				Bus:   bus,
				Store: ftAdapter{s: ftStore},
			})
			Expect(m.Start(context.Background())).To(Succeed())
			DeferCleanup(func() { Expect(m.Close()).To(Succeed()) })
			return m
		}

		rec := func(id, status, message string) *distributed.FineTuneJobRecord {
			return &distributed.FineTuneJobRecord{
				ID: id, UserID: "u1", Model: "m", Backend: "bk",
				TrainingType: "lora", TrainingMethod: "sft", Status: status, Message: message,
			}
		}

		BeforeEach(func() {
			var err error
			ftStore, err = distributed.NewFineTuneStore(db)
			Expect(err).ToNot(HaveOccurred())

			busA, busB = newBus(), newBus()
			a, b = newMap(busA), newMap(busB)
		})

		It("carries a Set from one replica to the other", func() {
			Expect(a.Set(context.Background(), rec("job-1", "queued", ""))).To(Succeed())

			Eventually(func() string {
				if r, ok := b.Get("job-1"); ok {
					return r.Status
				}
				return ""
			}, 30*time.Second, 50*time.Millisecond).Should(Equal("queued"))
		})

		It("carries a Delete from one replica to the other", func() {
			Expect(a.Set(context.Background(), rec("job-2", "queued", ""))).To(Succeed())
			Eventually(func() bool { _, ok := b.Get("job-2"); return ok }, 30*time.Second, 50*time.Millisecond).
				Should(BeTrue())

			Expect(a.Delete(context.Background(), "job-2")).To(Succeed())
			Eventually(func() bool { _, ok := b.Get("job-2"); return ok }, 30*time.Second, 50*time.Millisecond).
				Should(BeFalse(), "a delete that does not carry leaves a job the peer can never clear")
		})

		It("carries a delta too large for one notification, byte identical", func() {
			// Comfortably over the carrier's 8000-byte notification cap, so
			// this delta can only arrive through the spill row. A truncated or
			// re-encoded payload here would surface as a job whose message the
			// peer renders differently from the replica that owns it, with
			// nothing failing.
			big := strings.Repeat("training log line; ", 1200)
			Expect(len(big)).To(BeNumerically(">", 8000))

			Expect(a.Set(context.Background(), rec("job-3", "training", big))).To(Succeed())

			Eventually(func() int {
				if r, ok := b.Get("job-3"); ok {
					return len(r.Message)
				}
				return 0
			}, 30*time.Second, 50*time.Millisecond).Should(Equal(len(big)))

			got, ok := b.Get("job-3")
			Expect(ok).To(BeTrue())
			Expect(got.Message).To(Equal(big), "the spilled payload must come back exactly as it was published")
		})

		It("re-hydrates what it missed once its dropped listener reconnects", func() {
			// The whole reason the durable store exists. The row is written
			// straight to the shared table and never broadcast, which is what a
			// peer's delta amounts to for a replica whose session was gone when
			// it was published: at most once, no replay, no gap signal. If the
			// reconnect callback is not registered, or the map converges on
			// deltas alone, this replica answers as though the job had never
			// been created - for good.
			//
			// The row is written BEFORE the session is dropped, and never
			// broadcast, so this spec cannot pass by racing a reconnect against
			// a NOTIFY: there is no NOTIFY, and the only path into b is a
			// re-hydrate that the reconnect triggers.
			Expect(ftStore.Upsert(rec("job-4", "completed", ""))).To(Succeed())
			_, present := b.Get("job-4")
			Expect(present).To(BeFalse(), "a row written straight to the table reaches no map until something re-reads it")

			terminate(busB)
			Eventually(busB.IsConnected, 60*time.Second, 100*time.Millisecond).Should(BeTrue())

			Eventually(func() string {
				if r, ok := b.Get("job-4"); ok {
					return r.Status
				}
				return ""
			}, 60*time.Second, 100*time.Millisecond).
				Should(Equal("completed"), "a reconnected replica must re-read the durable source, not wait for a delta that is gone")
		})

		It("keeps carrying deltas after the reconnect", func() {
			// The half the re-hydrate does not cover: a carrier that came back
			// deaf re-hydrates once and then looks exactly like a deployment
			// where nobody is publishing.
			terminate(busB)
			Eventually(busB.IsConnected, 60*time.Second, 100*time.Millisecond).Should(BeTrue())

			Eventually(func() bool {
				// Retried because a NOTIFY issued between the drop and the
				// re-LISTEN is genuinely lost: this carrier is at most once.
				Expect(a.Set(context.Background(), rec("job-5", "queued", ""))).To(Succeed())
				_, ok := b.Get("job-5")
				return ok
			}, 60*time.Second, 200*time.Millisecond).Should(BeTrue())
		})
	})

	Describe("two families sharing one LISTEN channel", func() {
		// Every state.* subject maps onto the single localai_state channel, so
		// a subscriber hears its peers' families too and separation is decided
		// by the filter alone. A carrier that delivered to every subscriber on
		// the channel would put quantization jobs into the fine-tune map, and
		// the fake bus cannot express the hazard because it has no channels.
		newNamed := func(bus *pgbus.Bus, name string) *syncstate.SyncedMap[string, *job] {
			GinkgoHelper()
			m := syncstate.New(syncstate.Config[string, *job]{Name: name, Key: jobKey, Bus: bus})
			Expect(m.Start(context.Background())).To(Succeed())
			DeferCleanup(func() { Expect(m.Close()).To(Succeed()) })
			return m
		}

		It("delivers a quantization delta to the quantization map and not to the fine-tune map", func() {
			busA, busB := newBus(), newBus()

			quantA := newNamed(busA, "quant.jobs")
			quantB := newNamed(busB, "quant.jobs")
			ftB := newNamed(busB, "finetune.jobs")

			Expect(quantA.Set(context.Background(), &job{ID: "q-1", Status: "running"})).To(Succeed())

			// The positive leg first, so the negative one below is asserted
			// AFTER delivery has demonstrably happened rather than before it
			// could have.
			Eventually(func() bool { _, ok := quantB.Get("q-1"); return ok }, 30*time.Second, 50*time.Millisecond).
				Should(BeTrue())

			Expect(ftB.List()).To(BeEmpty(), "a fine-tune map must not apply a quantization delta off the shared channel")
		})
	})

	Describe("per-tenant subjects on the real carrier", func() {
		// Task 10 pinned this against a fake bus. Repeated here because this is
		// the commit where those subjects actually become LISTEN traffic, and
		// because both the tenant subject and the cluster-wide one land on the
		// same channel: if tenancy were a channel decision it would be right by
		// accident, and it is not.
		newTenant := func(bus *pgbus.Bus, tenant string) *syncstate.SyncedMap[string, *job] {
			GinkgoHelper()
			m := syncstate.New(syncstate.Config[string, *job]{
				Name: "agent.tasks", Key: jobKey, Bus: bus, PerTenant: true, Tenant: tenant,
			})
			Expect(m.Start(context.Background())).To(Succeed())
			DeferCleanup(func() { Expect(m.Close()).To(Succeed()) })
			return m
		}

		It("reaches the same tenant and the cluster-wide view, and no other tenant", func() {
			busA, busB := newBus(), newBus()

			u1A := newTenant(busA, "u1")
			u1B := newTenant(busB, "u1")
			u2B := newTenant(busB, "u2")
			clusterB := newTenant(busB, "")

			Expect(u1A.Set(context.Background(), &job{ID: "t-1", Status: "running"})).To(Succeed())

			Eventually(func() bool { _, ok := u1B.Get("t-1"); return ok }, 30*time.Second, 50*time.Millisecond).
				Should(BeTrue(), "the same tenant on another replica must see it")
			Eventually(func() bool { _, ok := clusterB.Get("t-1"); return ok }, 30*time.Second, 50*time.Millisecond).
				Should(BeTrue(), "the cluster-wide view hydrates across tenants, so it must apply across tenants")

			Expect(u2B.List()).To(BeEmpty(), "another tenant must never see this tenant's task")
		})
	})
})
