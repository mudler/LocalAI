package nodes

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

type legacyFactory struct{ addrs []string }

func (f *legacyFactory) NewClient(address string, _ bool) grpc.Backend {
	f.addrs = append(f.addrs, address)
	return nil
}

type nodeAwareFactory struct {
	legacyFactory
	nodes []string
}

func (f *nodeAwareFactory) NewNodeClient(nodeID, address string, _ bool) grpc.Backend {
	f.nodes = append(f.nodes, nodeID+"@"+address)
	return nil
}

var _ = Describe("newBackendClient", func() {
	It("uses the legacy factory, without a node id, when that is all it offers", func() {
		f := &legacyFactory{}
		newBackendClient(f, "n1", "10.0.0.1:50051", false)
		Expect(f.addrs).To(Equal([]string{"10.0.0.1:50051"}))
	})

	It("hands the node id to a factory that asks for it, and not to the legacy path", func() {
		f := &nodeAwareFactory{}
		newBackendClient(f, "n1", "10.0.0.1:50051", true)
		Expect(f.nodes).To(Equal([]string{"n1@10.0.0.1:50051"}))
		Expect(f.addrs).To(BeEmpty())
	})

	It("keeps the default factory dialling the address it was given", func() {
		var f BackendClientFactory = &tokenClientFactory{}
		_, ok := f.(NodeBackendClientFactory)
		Expect(ok).To(BeTrue(), "the default factory must implement the node-aware form")
	})
})
