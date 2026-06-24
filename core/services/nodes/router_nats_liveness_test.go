package nodes

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A node's stored status comes from its HTTP heartbeat, but work is dispatched
// over NATS. A worker that dies stops answering on the bus immediately and
// keeps its healthy status until the heartbeat ages out, so the scheduler could
// commit to a node it could not reach. The request then failed outright with
// "no responders available" rather than moving to a node that was actually up.
var _ = Describe("Scheduling past a node that left the bus", func() {
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

	// Only a no-responders answer proves absence. Excluding a node that is
	// merely slow would cost real capacity.
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
})
