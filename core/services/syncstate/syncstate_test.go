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
