package nodes

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeCleanupRegistry struct {
	mu       sync.Mutex
	due      []NodeModel
	claimed  bool
	removed  []modelReplicaRef
	failures []string
	next     []time.Time
}

func (f *fakeCleanupRegistry) ClaimModelCleanupRetries(_ context.Context, _ time.Time, _ time.Time, _ int) ([]NodeModel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimed {
		return nil, nil
	}
	f.claimed = true
	return append([]NodeModel(nil), f.due...), nil
}

func (f *fakeCleanupRegistry) RemoveClaimedModelCleanup(_ context.Context, claimed NodeModel) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, modelReplicaRef{claimed.NodeID, claimed.ModelName, claimed.ReplicaIndex})
	return true, nil
}

func (f *fakeCleanupRegistry) RecordModelCleanupFailure(_ context.Context, _, _ string, _ int, cleanupErr string, next time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, cleanupErr)
	f.next = append(f.next, next)
	return nil
}

type fakeExactStopper struct {
	mu      sync.Mutex
	replies []messaging.ModelStopReply
	errs    []error
	calls   []NodeModel
	block   chan struct{}
}

type leasingCleanupRegistry struct {
	mu         sync.Mutex
	row        NodeModel
	leaseUntil time.Time
}

func (f *leasingCleanupRegistry) ClaimModelCleanupRetries(_ context.Context, now, leaseUntil time.Time, _ int) ([]NodeModel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.leaseUntil.IsZero() && f.leaseUntil.After(now) {
		return nil, nil
	}
	f.leaseUntil = leaseUntil
	return []NodeModel{f.row}, nil
}

func (f *leasingCleanupRegistry) RemoveClaimedModelCleanup(_ context.Context, _ NodeModel) (bool, error) {
	return true, nil
}

func (f *leasingCleanupRegistry) RecordModelCleanupFailure(_ context.Context, _, _ string, _ int, _ string, _ time.Time) error {
	return nil
}

type blockingExactStopper struct {
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
	calls   int
}

func (f *blockingExactStopper) StopModelReplica(_ context.Context, _ string, _ NodeModel, _ bool) (messaging.ModelStopReply, error) {
	f.mu.Lock()
	f.calls++
	if f.calls == 1 {
		close(f.entered)
	}
	f.mu.Unlock()
	<-f.release
	return messaging.ModelStopReply{Matched: true, Terminated: true}, nil
}

func (f *fakeExactStopper) StopModelReplica(_ context.Context, _ string, replica NodeModel, _ bool) (messaging.ModelStopReply, error) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	i := len(f.calls)
	f.calls = append(f.calls, replica)
	var reply messaging.ModelStopReply
	var err error
	if i < len(f.replies) {
		reply = f.replies[i]
	}
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return reply, err
}

var _ = Describe("ModelCleanupService", func() {
	var now time.Time
	BeforeEach(func() { now = time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC) })

	It("deletes only replicas whose termination is confirmed", func() {
		registry := &fakeCleanupRegistry{}
		stopper := &fakeExactStopper{replies: []messaging.ModelStopReply{{Matched: true, Terminated: true}}}
		service := NewModelCleanupService(registry, stopper)
		service.now = func() time.Time { return now }
		service.Cleanup(context.Background(), []NodeModel{{NodeID: "n1", ModelName: "m", ReplicaIndex: 3}}, false)
		Expect(registry.removed).To(Equal([]modelReplicaRef{{"n1", "m", 3}}))
		Expect(registry.failures).To(BeEmpty())
	})

	It("treats exact process absence as idempotent success", func() {
		registry := &fakeCleanupRegistry{}
		stopper := &fakeExactStopper{replies: []messaging.ModelStopReply{{Matched: false, Terminated: true}}}
		service := NewModelCleanupService(registry, stopper)
		service.Cleanup(context.Background(), []NodeModel{{NodeID: "n1", ModelName: "m"}}, false)
		Expect(registry.removed).To(HaveLen(1))
	})

	It("keeps and backs off a replica when no worker responds", func() {
		registry := &fakeCleanupRegistry{}
		stopper := &fakeExactStopper{errs: []error{errors.New("NATS request: no responders available")}}
		service := NewModelCleanupService(registry, stopper)
		service.now = func() time.Time { return now }
		service.Cleanup(context.Background(), []NodeModel{{NodeID: "n1", ModelName: "m", CleanupAttempts: 2}}, false)
		Expect(registry.removed).To(BeEmpty())
		Expect(registry.failures).To(Equal([]string{"no responders available"}))
		Expect(registry.next[0]).To(Equal(now.Add(4 * time.Second)))
	})

	It("retries transient failures and later removes the row", func() {
		registry := &fakeCleanupRegistry{}
		stopper := &fakeExactStopper{errs: []error{errors.New("timeout"), nil}, replies: []messaging.ModelStopReply{{}, {Matched: true, Terminated: true}}}
		service := NewModelCleanupService(registry, stopper)
		r := NodeModel{NodeID: "n1", ModelName: "m"}
		service.Cleanup(context.Background(), []NodeModel{r}, false)
		service.Cleanup(context.Background(), []NodeModel{r}, false)
		Expect(registry.failures).To(HaveLen(1))
		Expect(registry.removed).To(HaveLen(1))
	})

	It("records a negative reply and tolerates a concurrent row deletion", func() {
		registry := &fakeCleanupRegistry{}
		stopper := &fakeExactStopper{replies: []messaging.ModelStopReply{{Matched: true, Terminated: false, Error: "address mismatch"}}}
		service := NewModelCleanupService(registry, stopper)
		service.Cleanup(context.Background(), []NodeModel{{NodeID: "n1", ModelName: "m"}}, false)
		Expect(registry.failures).To(Equal([]string{"address mismatch"}))
	})

	It("leases due work so two runners do not own the same replica", func() {
		registry := &fakeCleanupRegistry{due: []NodeModel{{NodeID: "n1", ModelName: "m"}}}
		stopper := &fakeExactStopper{replies: []messaging.ModelStopReply{{Matched: true, Terminated: true}}}
		a := NewModelCleanupService(registry, stopper)
		b := NewModelCleanupService(registry, stopper)
		a.runOnce(context.Background())
		b.runOnce(context.Background())
		Expect(stopper.calls).To(HaveLen(1))
	})

	It("keeps single ownership while a slow stop advances past the old lease boundary", func() {
		clock := now
		registry := &leasingCleanupRegistry{row: NodeModel{ID: "claimed-row", NodeID: "n1", ModelName: "m", State: "unloading"}}
		stopper := &blockingExactStopper{entered: make(chan struct{}), release: make(chan struct{})}
		a := NewModelCleanupService(registry, stopper)
		b := NewModelCleanupService(registry, stopper)
		a.now = func() time.Time { return clock }
		b.now = func() time.Time { return clock }

		done := make(chan struct{})
		go func() {
			defer close(done)
			a.runOnce(context.Background())
		}()
		Eventually(stopper.entered).Should(BeClosed())
		clock = clock.Add(31 * time.Second)
		b.runOnce(context.Background())

		stopper.mu.Lock()
		Expect(stopper.calls).To(Equal(1))
		stopper.mu.Unlock()
		close(stopper.release)
		Eventually(done).Should(BeClosed())
	})
})
