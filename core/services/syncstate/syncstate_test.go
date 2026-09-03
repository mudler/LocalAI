package syncstate_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/syncstate"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// job is a minimal JSON-serializable value stand-in for the real cross-replica
// records (finetune/quant/agent jobs) the component is built for.
type job struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func jobKey(j *job) string { return j.ID }

const stateName = "test.jobs"

func deltaSubject() string { return messaging.SubjectSyncStateDelta(stateName) }

// fakeStore is an in-memory Store that records call counts so specs can assert
// the write-through-vs-apply split (local writes hit the Store; applied deltas
// must not).
type fakeStore struct {
	mu          sync.Mutex
	data        map[string]*job
	upsertCalls int
	deleteCalls int
	listCalls   int
}

func newFakeStore(seed ...*job) *fakeStore {
	s := &fakeStore{data: map[string]*job{}}
	for _, j := range seed {
		s.data[j.ID] = j
	}
	return s
}

func (s *fakeStore) List(_ context.Context) ([]*job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	out := make([]*job, 0, len(s.data))
	for _, j := range s.data {
		out = append(out, j)
	}
	return out, nil
}

func (s *fakeStore) Upsert(_ context.Context, j *job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertCalls++
	s.data[j.ID] = j
	return nil
}

func (s *fakeStore) Delete(_ context.Context, k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls++
	delete(s.data, k)
	return nil
}

// add simulates a peer replica writing to the shared DB out-of-band (e.g. while
// this replica was partitioned), so a re-hydrate can be observed to pick it up.
func (s *fakeStore) add(j *job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[j.ID] = j
}

func (s *fakeStore) counts() (upsert, del, list int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upsertCalls, s.deleteCalls, s.listCalls
}

var _ = Describe("SyncedMap", func() {
	ctx := context.Background()

	Describe("cross-replica delta propagation", func() {
		var (
			bus  *testutil.FakeBus
			a, b *syncstate.SyncedMap[string, *job]
		)

		BeforeEach(func() {
			bus = testutil.NewFakeBus()
			a = syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey, Nats: bus})
			b = syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey, Nats: bus})
			Expect(a.Start(ctx)).To(Succeed())
			Expect(b.Start(ctx)).To(Succeed())
		})

		AfterEach(func() {
			Expect(a.Close()).To(Succeed())
			Expect(b.Close()).To(Succeed())
		})

		It("propagates a Set on A to B", func() {
			Expect(a.Set(ctx, &job{ID: "1", Status: "running"})).To(Succeed())

			got, ok := b.Get("1")
			Expect(ok).To(BeTrue(), "replica B should see the value A just set")
			Expect(got.Status).To(Equal("running"))
		})

		It("prunes a Delete on A from B", func() {
			Expect(a.Set(ctx, &job{ID: "1", Status: "running"})).To(Succeed())
			_, present := b.Get("1")
			Expect(present).To(BeTrue(), "precondition: B must have the value before the delete")

			Expect(a.Delete(ctx, "1")).To(Succeed())

			_, ok := b.Get("1")
			Expect(ok).To(BeFalse(), "a delete on A must remove the key from B")
		})
	})

	Describe("hydration", func() {
		It("hydrates on Start from a preloaded Store", func() {
			store := newFakeStore(&job{ID: "x", Status: "done"})
			m := syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey, Store: store})
			Expect(m.Start(ctx)).To(Succeed())

			got, ok := m.Get("x")
			Expect(ok).To(BeTrue(), "Start must populate the map from the Store")
			Expect(got.Status).To(Equal("done"))
		})

		It("uses the Loader when Store is nil", func() {
			m := syncstate.New(syncstate.Config[string, *job]{
				Name: stateName,
				Key:  jobKey,
				Loader: func(_ context.Context) ([]*job, error) {
					return []*job{{ID: "l", Status: "loaded"}}, nil
				},
			})
			Expect(m.Start(ctx)).To(Succeed())

			got, ok := m.Get("l")
			Expect(ok).To(BeTrue(), "Loader output must hydrate the map when there is no Store")
			Expect(got.Status).To(Equal("loaded"))
		})
	})

	Describe("echo-loop guard", func() {
		It("applies its own broadcast once and does not re-publish", func() {
			bus := testutil.NewFakeBus()
			a := syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey, Nats: bus})
			b := syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey, Nats: bus})
			Expect(a.Start(ctx)).To(Succeed())
			Expect(b.Start(ctx)).To(Succeed())
			defer func() {
				Expect(a.Close()).To(Succeed())
				Expect(b.Close()).To(Succeed())
			}()

			Expect(a.Set(ctx, &job{ID: "e", Status: "running"})).To(Succeed())

			// One local write must produce exactly one broadcast: A and B both
			// receive it and apply to memory, but the apply path never re-publishes.
			Expect(bus.PublishCount(deltaSubject())).To(Equal(1),
				"the apply path must not re-broadcast, otherwise replicas storm")
			Expect(a.List()).To(HaveLen(1), "A must not double-store its own echo")
			_, ok := b.Get("e")
			Expect(ok).To(BeTrue())
		})
	})

	Describe("Store write-through vs apply", func() {
		It("writes the Store on local Set/Delete but not on an applied delta", func() {
			bus := testutil.NewFakeBus()
			storeA := newFakeStore()
			storeB := newFakeStore()
			a := syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey, Nats: bus, Store: storeA})
			b := syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey, Nats: bus, Store: storeB})
			Expect(a.Start(ctx)).To(Succeed())
			Expect(b.Start(ctx)).To(Succeed())
			defer func() {
				Expect(a.Close()).To(Succeed())
				Expect(b.Close()).To(Succeed())
			}()

			Expect(a.Set(ctx, &job{ID: "w", Status: "running"})).To(Succeed())

			upA, _, _ := storeA.counts()
			upB, _, _ := storeB.counts()
			Expect(upA).To(Equal(1), "local Set must write through to its own Store")
			Expect(upB).To(Equal(0), "the apply path must never write the peer's Store")

			Expect(a.Delete(ctx, "w")).To(Succeed())
			_, delA, _ := storeA.counts()
			_, delB, _ := storeB.counts()
			Expect(delA).To(Equal(1), "local Delete must delete from its own Store")
			Expect(delB).To(Equal(0), "the apply path must never delete from the peer's Store")
		})
	})

	Describe("OnApply hook", func() {
		It("fires with the correct op and key on an applied delta", func() {
			bus := testutil.NewFakeBus()
			var (
				mu   sync.Mutex
				ops  []string
				keys []string
			)
			a := syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey, Nats: bus})
			b := syncstate.New(syncstate.Config[string, *job]{
				Name: stateName, Key: jobKey, Nats: bus,
				OnApply: func(op string, k string, _ *job) {
					mu.Lock()
					ops = append(ops, op)
					keys = append(keys, k)
					mu.Unlock()
				},
			})
			Expect(a.Start(ctx)).To(Succeed())
			Expect(b.Start(ctx)).To(Succeed())
			defer func() {
				Expect(a.Close()).To(Succeed())
				Expect(b.Close()).To(Succeed())
			}()

			Expect(a.Set(ctx, &job{ID: "o", Status: "running"})).To(Succeed())
			Expect(a.Delete(ctx, "o")).To(Succeed())

			mu.Lock()
			defer mu.Unlock()
			Expect(ops).To(Equal([]string{"set", "delete"}))
			Expect(keys).To(Equal([]string{"o", "o"}))
		})
	})

	Describe("standalone (nil Nats)", func() {
		It("works in-memory with no panic and nothing to broadcast", func() {
			m := syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey})
			Expect(m.Start(ctx)).To(Succeed())
			defer func() { Expect(m.Close()).To(Succeed()) }()

			Expect(func() {
				Expect(m.Set(ctx, &job{ID: "s", Status: "running"})).To(Succeed())
			}).ToNot(Panic())

			got, ok := m.Get("s")
			Expect(ok).To(BeTrue())
			Expect(got.Status).To(Equal("running"))
			Expect(m.List()).To(HaveLen(1))
			Expect(m.Snapshot()).To(HaveKey("s"))

			Expect(m.Delete(ctx, "s")).To(Succeed())
			_, ok = m.Get("s")
			Expect(ok).To(BeFalse())
		})
	})

	Describe("reconnect re-hydrate", func() {
		It("re-reads the source when the messaging client reconnects", func() {
			bus := testutil.NewFakeBus()
			store := newFakeStore(&job{ID: "init", Status: "running"})
			m := syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey, Nats: bus, Store: store})
			Expect(m.Start(ctx)).To(Succeed())
			defer func() { Expect(m.Close()).To(Succeed()) }()

			_, ok := m.Get("init")
			Expect(ok).To(BeTrue())

			// A peer writes to the shared DB while we are unaware (no delta seen).
			store.add(&job{ID: "late", Status: "running"})
			_, ok = m.Get("late")
			Expect(ok).To(BeFalse(), "the new row should not appear before a re-hydrate")

			bus.TriggerReconnect()

			_, ok = m.Get("late")
			Expect(ok).To(BeTrue(), "reconnect must re-hydrate from the source and pick up drift")
			_, _, list := store.counts()
			Expect(list).To(Equal(2), "exactly one Start hydrate plus one reconnect re-hydrate")
		})
	})
})

// Per-tenant maps.
//
// A SyncedMap instantiated once per tenant used to publish and subscribe on the
// SAME subject as every other tenant's copy, so a delta was applied into every
// other tenant's in-memory map. These specs assert the NEGATIVE case - tenant A
// must not receive tenant B's traffic - at least as hard as the positive one,
// because the negative case is the data-exposure bug and the positive case is
// only the feature.
var _ = Describe("SyncedMap per-tenant subjects", func() {
	ctx := context.Background()

	newTenantMap := func(bus *testutil.FakeBus, tenant string) *syncstate.SyncedMap[string, *job] {
		m := syncstate.New(syncstate.Config[string, *job]{
			Name:      stateName,
			Key:       jobKey,
			Nats:      bus,
			PerTenant: true,
			Tenant:    tenant,
		})
		Expect(m.Start(ctx)).To(Succeed())
		return m
	}

	Describe("three maps on one bus", func() {
		var (
			bus             *testutil.FakeBus
			cluster, u1, u2 *syncstate.SyncedMap[string, *job]
		)

		BeforeEach(func() {
			bus = testutil.NewFakeBus()
			// Tenant "" is the cluster-wide administrative view: it hydrates
			// from every tenant's rows, so it must also apply every tenant's
			// deltas or it is stale the moment any tenant writes.
			cluster = newTenantMap(bus, "")
			u1 = newTenantMap(bus, "u1")
			u2 = newTenantMap(bus, "u2")
		})

		AfterEach(func() {
			Expect(u2.Close()).To(Succeed())
			Expect(u1.Close()).To(Succeed())
			Expect(cluster.Close()).To(Succeed())
		})

		It("keeps a Set on u1 out of u2 while the cluster-wide view sees it", func() {
			Expect(u1.Set(ctx, &job{ID: "j-u1", Status: "running"})).To(Succeed())

			// Assert on Get, not on a publish counter: a counter is satisfied
			// by a map that received the delta and applied it under a key the
			// spec never looks up.
			_, leaked := u2.Get("j-u1")
			Expect(leaked).To(BeFalse(), "tenant u2 must never receive tenant u1's delta")

			_, mine := u1.Get("j-u1")
			Expect(mine).To(BeTrue(), "the originating tenant keeps its own write")

			_, admin := cluster.Get("j-u1")
			Expect(admin).To(BeTrue(), "the cluster-wide view must apply every tenant's delta")
		})

		It("keeps a Set on u2 out of u1 while the cluster-wide view sees it", func() {
			// The mirror direction, spelled out rather than assumed: a filter
			// built from the wrong tenant would pass one direction only.
			Expect(u2.Set(ctx, &job{ID: "j-u2", Status: "running"})).To(Succeed())

			_, leaked := u1.Get("j-u2")
			Expect(leaked).To(BeFalse(), "tenant u1 must never receive tenant u2's delta")

			_, admin := cluster.Get("j-u2")
			Expect(admin).To(BeTrue(), "the cluster-wide view must apply every tenant's delta")
		})

		It("keeps a Delete on u1 out of u2", func() {
			// A delete carries only op+key, so a leaked delete removes a row
			// from a map that never held the create - the same boundary, the
			// destructive direction.
			Expect(u2.Set(ctx, &job{ID: "shared-id", Status: "u2s"})).To(Succeed())
			Expect(u1.Set(ctx, &job{ID: "shared-id", Status: "u1s"})).To(Succeed())

			Expect(u1.Delete(ctx, "shared-id")).To(Succeed())

			_, survives := u2.Get("shared-id")
			Expect(survives).To(BeTrue(), "tenant u1 deleting its own key must not erase tenant u2's")
		})

		It("keeps a Set on the cluster-wide map out of both tenant maps", func() {
			Expect(cluster.Set(ctx, &job{ID: "j-admin", Status: "running"})).To(Succeed())

			_, in1 := u1.Get("j-admin")
			Expect(in1).To(BeFalse())
			_, in2 := u2.Get("j-admin")
			Expect(in2).To(BeFalse())
		})

		It("publishes a tenant's mutation on that tenant's subject alone", func() {
			// The publish half of the rule, asserted by subject name so a map
			// that published unscoped but subscribed scoped - which would look
			// like a dead bus rather than a leak - fails here by name.
			Expect(u1.Set(ctx, &job{ID: "j-u1", Status: "running"})).To(Succeed())

			Expect(bus.PublishCount(messaging.SubjectSyncStateTenantDelta(stateName, "u1"))).To(Equal(1))
			Expect(bus.PublishCount(deltaSubject())).To(Equal(0),
				"a tenant must not put anything on the cluster-wide subject")
			Expect(bus.PublishCount(messaging.SubjectSyncStateTenantDelta(stateName, "u2"))).To(Equal(0))
		})

		It("publishes the cluster-wide map's mutation on the unscoped subject", func() {
			Expect(cluster.Set(ctx, &job{ID: "j-admin", Status: "running"})).To(Succeed())

			Expect(bus.PublishCount(deltaSubject())).To(Equal(1))
		})
	})

	Describe("Close on the cluster-wide map", func() {
		It("drops BOTH of its subscriptions", func() {
			// The cluster-wide view is the only map holding two subscriptions,
			// so it is the only one where a Close that unsubscribes the first
			// and returns leaves a live handler writing into a closed map.
			bus := testutil.NewFakeBus()
			cluster := newTenantMap(bus, "")
			peerCluster := newTenantMap(bus, "")
			u1 := newTenantMap(bus, "u1")
			defer func() {
				Expect(peerCluster.Close()).To(Succeed())
				Expect(u1.Close()).To(Succeed())
			}()

			Expect(cluster.Close()).To(Succeed())

			// The tenant-wildcard subscription.
			Expect(u1.Set(ctx, &job{ID: "after-close-tenant", Status: "x"})).To(Succeed())
			_, got := cluster.Get("after-close-tenant")
			Expect(got).To(BeFalse(), "the tenant-wildcard subscription must be gone after Close")

			// The unscoped subscription.
			Expect(peerCluster.Set(ctx, &job{ID: "after-close-unscoped", Status: "x"})).To(Succeed())
			_, got = cluster.Get("after-close-unscoped")
			Expect(got).To(BeFalse(), "the unscoped subscription must be gone after Close")
		})
	})

	Describe("PerTenant false", func() {
		It("keeps exactly the subject it has today", func() {
			// finetune.jobs, quantization and the responses store are unscoped
			// adopters. Pin that this change moved none of them.
			bus := testutil.NewFakeBus()
			a := syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey, Nats: bus})
			b := syncstate.New(syncstate.Config[string, *job]{Name: stateName, Key: jobKey, Nats: bus})
			Expect(a.Start(ctx)).To(Succeed())
			Expect(b.Start(ctx)).To(Succeed())
			tenant := newTenantMap(bus, "u1")
			defer func() {
				Expect(a.Close()).To(Succeed())
				Expect(b.Close()).To(Succeed())
				Expect(tenant.Close()).To(Succeed())
			}()

			Expect(a.Set(ctx, &job{ID: "unscoped", Status: "running"})).To(Succeed())

			Expect(bus.PublishCount(deltaSubject())).To(Equal(1))
			_, peer := b.Get("unscoped")
			Expect(peer).To(BeTrue(), "unscoped adopters must keep converging with each other")
			_, crossed := tenant.Get("unscoped")
			Expect(crossed).To(BeFalse(), "an unscoped map must not reach a tenant map")

			Expect(tenant.Set(ctx, &job{ID: "scoped", Status: "running"})).To(Succeed())
			_, back := a.Get("scoped")
			Expect(back).To(BeFalse(), "a tenant map must not reach an unscoped map")
		})
	})
})
