// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"errors"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("the health check on a remote model whose transport failed", func() {
	// The fourth site of the same shape as the reconciler, the health monitor
	// and the router, found by sweeping rather than by being named.
	//
	// checkIsLoaded evicts a remote model on a "connection error", which used
	// to mean exactly one thing: the worker's socket did not answer. In
	// distributed mode the client reaches the backend over the worker's tunnel,
	// and a failure of THAT transport arrives as the same codes.Unavailable.
	// Evicting on it unloads a model that is loaded and serving, on a worker
	// that is heartbeating.
	var ml *ModelLoader

	BeforeEach(func() {
		systemState, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).ToNot(HaveOccurred())
		ml = NewModelLoader(systemState)
	})

	It("keeps the model when the tunnel dial failed", func() {
		client := grpc.NewClientWithDialer("10.0.0.1:9001", false, nil, false, "",
			func(context.Context, string) (net.Conn, error) {
				return nil, errors.New("cluster: no route from this replica to that worker")
			})
		m := NewModelWithClient("remote-model", "10.0.0.1:9001", client)
		ml.store.Set("remote-model", m)

		Expect(ml.checkIsLoaded("remote-model")).To(BeIdenticalTo(m),
			"a model on a worker this frontend cannot route to must stay cached, not be unloaded")
		_, stillThere := ml.store.Get("remote-model")
		Expect(stillThere).To(BeTrue())
	})

	It("still evicts a remote model whose worker WAS reached and did not answer", func() {
		// The other direction, so the new check cannot pass by never evicting.
		// No custom dialer, so the transport reports nothing and a connection
		// error means what it always meant.
		client := grpc.NewClientWithToken("127.0.0.1:1", false, nil, false, "")
		m := NewModelWithClient("dead-model", "127.0.0.1:1", client)
		ml.store.Set("dead-model", m)

		Expect(ml.checkIsLoaded("dead-model")).To(BeNil())
		_, stillThere := ml.store.Get("dead-model")
		Expect(stillThere).To(BeFalse())
	})
})

var _ = Describe("the eviction wrapper on a remote model whose transport failed", func() {
	// The FIFTH site of the same shape, found by sweeping the decorators rather
	// than being named. initializers.go builds this wrapper for exactly the
	// remote models the router produces, and its evict callback runs
	// ShutdownModel, which sends backend.stop over NATS to every node holding
	// the model and deletes every replica row. It fires during INFERENCE, not
	// on a health check, so a tunnel blip mid-request was enough.
	failingDial := func(cause error) grpc.Backend {
		return grpc.NewClientWithDialer("10.0.0.1:9001", false, nil, false, "",
			func(context.Context, string) (net.Conn, error) { return nil, cause })
	}

	It("does not evict when the worker could not be reached", func() {
		evicted := 0
		client := newConnectionEvictingClient(
			failingDial(errors.New("cluster: no route from this replica to that worker")),
			"remote-model", func() { evicted++ })

		_, err := client.Predict(context.Background(), &pb.PredictOptions{})
		Expect(err).To(HaveOccurred())
		Expect(evicted).To(BeZero(),
			"a worker this frontend cannot route to must not have its backend stopped and its rows deleted")
	})

	It("still evicts when the worker WAS reached and the connection failed", func() {
		// The other direction. No custom dialer, so nothing reports a transport
		// failure and a connection error means what it always meant.
		evicted := 0
		client := newConnectionEvictingClient(
			grpc.NewClientWithToken("127.0.0.1:1", false, nil, false, ""),
			"dead-model", func() { evicted++ })

		_, err := client.Predict(context.Background(), &pb.PredictOptions{})
		Expect(err).To(HaveOccurred())
		Expect(evicted).To(Equal(1))
	})

	It("is transparent to the transport question, so a wrapper of it still sees through", func() {
		client := newConnectionEvictingClient(
			failingDial(errors.New("cluster: no route from this replica to that worker")),
			"remote-model", func() {})
		_, _ = client.Predict(context.Background(), &pb.PredictOptions{})
		Expect(grpc.LastDialErrorOf(client)).ToNot(BeNil())
	})
})
