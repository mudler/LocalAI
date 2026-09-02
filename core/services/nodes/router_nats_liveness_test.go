package nodes

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
)

// pickReachableNode's exclusion branch, which no longer runs in production.
//
// It excludes on nats.ErrNoResponders alone, and since the control plane moved
// onto the tunnel nothing can produce that value: cluster, the package that
// supplies every control-path dial error, does not link nats.go at all. So the
// specs below that drive fakeUnloader.deadNodes exercise a branch a real
// adapter cannot enter. They are kept, rather than deleted, because the LOOP is
// still compiled and still executed on every scheduling decision, and its shape
// (the retry bound, and stopping when the demotion itself fails) is what stops a
// future re-wiring from spinning. They go with the exclusion in the task that
// gives the scheduler cluster.Presence.
//
// What is load-bearing TODAY is the opposite assertion, and it lives in two
// places: the DescribeTable at the bottom of this file, which pins that none of
// the sentinels a control RPC actually produces excludes anything, and the
// scheduling specs in unloader_ping_test.go, which prove the same thing through
// a REAL RemoteUnloaderAdapter. This file's doubles cannot see a carrier
// change; that is why they stayed green through the window in which every
// healthy worker read as absent.
var _ = Describe("The retired bus-absence exclusion", func() {
	var (
		reg    *fakeModelRouter
		fake   *fakeUnloader
		router *SmartRouter
	)

	newNode := func(id string) *BackendNode {
		return &BackendNode{ID: id, Name: id, Address: id + ":50051"}
	}

	// selectorReturning hands back each node in turn, mimicking a scheduler
	// that re-picks after the previous choice was demoted.
	selectorReturning := func(nodes ...*BackendNode) func() *BackendNode {
		i := 0
		return func() *BackendNode {
			if i >= len(nodes) {
				return nil
			}
			n := nodes[i]
			i++
			return n
		}
	}

	BeforeEach(func() {
		reg = &fakeModelRouter{}
		fake = &fakeUnloader{deadNodes: map[string]bool{}}
		router = NewSmartRouter(reg, SmartRouterOptions{Unloader: fake})
	})

	It("passes over a node that no longer answers and takes one that does", func() {
		dead, alive := newNode("dead-node"), newNode("alive-node")
		fake.deadNodes["dead-node"] = true

		picked := router.pickReachableNode(context.Background(), selectorReturning(dead, alive))

		Expect(picked).ToNot(BeNil())
		Expect(picked.ID).To(Equal("alive-node"))
		Expect(fake.pingCalls).To(Equal([]string{"dead-node", "alive-node"}))
	})

	It("demotes the absent node so other schedulers stop choosing it", func() {
		dead, alive := newNode("dead-node"), newNode("alive-node")
		fake.deadNodes["dead-node"] = true

		router.pickReachableNode(context.Background(), selectorReturning(dead, alive))

		Expect(reg.markedUnhealthy).To(Equal([]string{"dead-node"}))
	})

	It("takes the first node when it answers, without probing further", func() {
		first, second := newNode("first"), newNode("second")

		picked := router.pickReachableNode(context.Background(), selectorReturning(first, second))

		Expect(picked.ID).To(Equal("first"))
		Expect(fake.pingCalls).To(Equal([]string{"first"}))
	})

	It("gives up rather than spinning when every node is gone", func() {
		a, b, c, d := newNode("a"), newNode("b"), newNode("c"), newNode("d")
		for _, id := range []string{"a", "b", "c", "d"} {
			fake.deadNodes[id] = true
		}

		picked := router.pickReachableNode(context.Background(), selectorReturning(a, b, c, d))

		Expect(picked).To(BeNil())
		Expect(len(fake.pingCalls)).To(BeNumerically("<=", maxNodeLivenessRetries))
	})

	It("stops when the demotion itself fails, so it cannot loop on one node", func() {
		dead := newNode("dead-node")
		fake.deadNodes["dead-node"] = true
		reg.markUnhealthyErr = errors.New("database is down")

		picked := router.pickReachableNode(context.Background(), selectorReturning(dead, dead, dead))

		Expect(picked).To(BeNil())
		Expect(fake.pingCalls).To(Equal([]string{"dead-node"}))
	})

	// The half of the branch that IS still reachable: every error a control
	// RPC can produce lands here.
	It("keeps a node that answers slowly or errors for another reason", func() {
		slow := newNode("slow-node")
		fake.pingErr = errors.New("timeout waiting for reply")

		picked := router.pickReachableNode(context.Background(), selectorReturning(slow))

		Expect(picked).ToNot(BeNil())
		Expect(picked.ID).To(Equal("slow-node"))
		Expect(reg.markedUnhealthy).To(BeEmpty())
	})

	It("treats every node as reachable when no command sender is configured", func() {
		plain := NewSmartRouter(reg, SmartRouterOptions{})
		node := newNode("only-node")

		Expect(plain.pickReachableNode(context.Background(), selectorReturning(node))).To(Equal(node))
	})

	// The rule that actually holds now, stated in the vocabulary a control RPC
	// answers in. Widening the exclusion to any of these puts the scheduler
	// back in the state this task was opened to fix: a worker that is
	// heartbeating and serving, unroutable from THIS replica for a moment
	// while its tunnel re-homes, demoted for every scheduler in the cluster.
	//
	// unloader_ping_test.go proves the same thing through a real adapter and a
	// real transport. This table is the cheap negative control beside it: it
	// names each sentinel, so a widening is attributed to the sentinel that
	// caused it rather than to "something in the ping".
	DescribeTable("never excludes a node on anything a control RPC can answer with",
		func(cause error) {
			fake.pingErr = cause
			node := newNode("live-node")

			Expect(router.pickReachableNode(context.Background(), selectorReturning(node))).To(Equal(node))
			Expect(reg.markedUnhealthy).To(BeEmpty())
		},
		Entry("no route to the worker", fmt.Errorf("control rpc: %w: %w", ErrWorkerUnroutable, cluster.ErrNoRoute)),
		Entry("no tunnel dialer wired", ErrNoWorkerDialer),
		Entry("the worker is older than this frontend", ErrWorkerControlUnsupported),
		Entry("the caller's budget ran out", fmt.Errorf("control rpc: %w: %w", ErrWorkerUnroutable, context.DeadlineExceeded)),
		Entry("the worker's own refusal", fmt.Errorf("%w: no such process", cluster.ErrStreamTargetUnavailable)),
	)
})
