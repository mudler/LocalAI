package distsync_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/core/services/failover/distsync"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// fakeSource is a minimal failover.ConfigSource with one chain "chain" of two
// targets: "x" (remote, primary) and "y" (local warm, fallback). It is
// shared across the managers in a spec, mirroring how frontends in a real
// deployment read the same config loader.
type fakeSource struct {
	mu   sync.Mutex
	cfgs map[string]config.ModelConfig
}

func newChainSource() *fakeSource {
	return &fakeSource{cfgs: map[string]config.ModelConfig{
		"x": {Name: "x", Backend: "cloud-proxy"},
		"y": {Name: "y", Backend: "llama-cpp"},
		"chain": {Name: "chain", Failover: &config.FailoverConfig{Targets: []config.FailoverTarget{
			{Model: "x"},
			{Model: "y", Warm: true},
		}}},
	}}
}

func (s *fakeSource) GetModelConfig(n string) (config.ModelConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cfgs[n]
	return c, ok
}

func (s *fakeSource) GetAllModelsConfigs() []config.ModelConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]config.ModelConfig, 0, len(s.cfgs))
	for _, c := range s.cfgs {
		out = append(out, c)
	}
	return out
}

// memPinStore is an in-memory syncstate.Store[string, distsync.PinRecord]
// shared by several distsync.Sync instances the way a real DB would be, so a
// spec can build a "late joiner" that hydrates from what earlier instances
// already wrote through.
type memPinStore struct {
	mu   sync.Mutex
	data map[string]distsync.PinRecord
}

func newMemPinStore() *memPinStore { return &memPinStore{data: map[string]distsync.PinRecord{}} }

func (s *memPinStore) List(context.Context) ([]distsync.PinRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]distsync.PinRecord, 0, len(s.data))
	for _, v := range s.data {
		out = append(out, v)
	}
	return out, nil
}

func (s *memPinStore) Upsert(_ context.Context, v distsync.PinRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[v.Chain] = v
	return nil
}

func (s *memPinStore) Delete(_ context.Context, k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, k)
	return nil
}

// leaderGateFor grants leadership to exactly one named manager at a time
// (whatever *leader currently holds), mirroring an advisory-lock leader loop
// where only one frontend probes and decides chains.
func leaderGateFor(name string, leader *string) failover.LeaderGate {
	return func(_ context.Context, fn func()) bool {
		if *leader != name {
			return false
		}
		fn()
		return true
	}
}

var errBoom = errors.New("boom")

var _ = Describe("distsync", func() {
	var (
		ctx      context.Context
		bus      *testutil.FakeBus
		src      *fakeSource
		pinStore *memPinStore
		leader   string
	)

	BeforeEach(func() {
		ctx = context.Background()
		bus = testutil.NewFakeBus()
		src = newChainSource()
		pinStore = newMemPinStore()
		leader = "a"
	})

	// newManager wires a fresh failover.Manager to a fresh distsync.Sync on
	// the shared bus and pin store, named so leaderGateFor can grant or deny
	// it leadership.
	newManager := func(name string) (*failover.Manager, *distsync.Sync) {
		m := failover.New(src, failover.WithLeaderGate(leaderGateFor(name, &leader)))
		s, err := distsync.New(ctx, bus, pinStore, m)
		Expect(err).ToNot(HaveOccurred())
		return m, s
	}

	It("a pin on A is visible on B and survives a new instance C built from the same store", func() {
		a, _ := newManager("a")
		b, _ := newManager("b")
		a.Tick(ctx)
		b.Tick(ctx)

		Expect(a.Pin("chain", "y")).To(Succeed())

		stB, ok := b.ChainStatus("chain")
		Expect(ok).To(BeTrue())
		Expect(stB.Pinned).ToNot(BeNil())
		Expect(*stB.Pinned).To(Equal("y"))

		// C is built after the pin was already written through to the shared
		// store, and before ever ticking: SetStateSync (inside distsync.New)
		// hydrates C's pins from s.Pins(), so the chain is created pinned the
		// first time anything asks for it.
		c, _ := newManager("c")
		stC, ok := c.ChainStatus("chain")
		Expect(ok).To(BeTrue())
		Expect(stC.Pinned).ToNot(BeNil())
		Expect(*stC.Pinned).To(Equal("y"))
	})

	It("a trip on B makes A's plan skip the target", func() {
		a, _ := newManager("a")
		b, _ := newManager("b")
		a.Tick(ctx)
		b.Tick(ctx)

		b.ReportFailure("x", errBoom)

		att, err := a.Plan("chain")
		Expect(err).ToNot(HaveOccurred())
		Expect(att.Target()).To(Equal("y"))
	})

	It("the leader's chain switch reaches the follower", func() {
		a, _ := newManager("a") // leader
		b, _ := newManager("b") // follower
		a.Tick(ctx)
		b.Tick(ctx)

		a.ReportFailure("x", errBoom)

		st, ok := b.ChainStatus("chain")
		Expect(ok).To(BeTrue())
		Expect(st.Active).To(Equal("y"))
	})

	It("late joiner converges on heartbeat", func() {
		a, _ := newManager("a") // leader
		a.Tick(ctx)

		a.ReportFailure("x", errBoom)

		// C joins after the trip: failover.targets/chains are NATS-only with
		// no Store, so C's hydrate on Start sees nothing and it starts out
		// believing every target is healthy.
		c, _ := newManager("c") // follower
		c.Tick(ctx)

		before, ok := c.ChainStatus("chain")
		Expect(ok).To(BeTrue())
		Expect(before.Active).To(Equal("x"))

		a.Republish()

		after, ok := c.ChainStatus("chain")
		Expect(ok).To(BeTrue())
		Expect(after.Active).To(Equal("y"))
	})

	It("guards against a typed-nil PinStore passed as the Store interface", func() {
		var nilStore *distsync.PinStore // deliberately typed, deliberately nil
		m := failover.New(src, failover.WithLeaderGate(leaderGateFor("a", &leader)))
		_, err := distsync.New(ctx, bus, nilStore, m)
		Expect(err).ToNot(HaveOccurred())
		m.Tick(ctx)

		Expect(m.Pin("chain", "y")).To(Succeed())
		st, ok := m.ChainStatus("chain")
		Expect(ok).To(BeTrue())
		Expect(st.Pinned).ToNot(BeNil())
		Expect(*st.Pinned).To(Equal("y"))
	})

	It("Close stops all three maps without erroring", func() {
		_, s := newManager("a")
		Expect(s.Close()).To(Succeed())
	})

	It("PinStore round-trips through a real sqlite-backed gorm DB", func() {
		db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())

		store, err := distsync.NewPinStore(db)
		Expect(err).ToNot(HaveOccurred())

		recs, err := store.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(recs).To(BeEmpty())

		Expect(store.Upsert(ctx, distsync.PinRecord{Chain: "chain", Target: "y", UpdatedAt: time.Now()})).To(Succeed())

		recs, err = store.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(recs).To(HaveLen(1))
		Expect(recs[0].Chain).To(Equal("chain"))
		Expect(recs[0].Target).To(Equal("y"))

		// Upsert again on the same key updates rather than duplicating.
		Expect(store.Upsert(ctx, distsync.PinRecord{Chain: "chain", Target: "x", UpdatedAt: time.Now()})).To(Succeed())
		recs, err = store.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(recs).To(HaveLen(1))
		Expect(recs[0].Target).To(Equal("x"))

		Expect(store.Delete(ctx, "chain")).To(Succeed())
		recs, err = store.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(recs).To(BeEmpty())
	})
})
