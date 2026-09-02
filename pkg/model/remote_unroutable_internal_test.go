// SPDX-License-Identifier: MIT

package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
)

// refusalFromWorker builds the error the frontend's dialler really returns when
// a worker refuses one of its streams, by writing the refusal with the worker's
// own writer and reading it back with the frontend's own reader.
//
// It matters that this goes over the wire rather than taking the sentinel
// directly. A worker that answers is CONNECTED, so its refusal is not a
// transport failure however much it looks like one from here, and a spec
// asserting against a hand-made value would not notice if the wire stopped
// carrying the distinction.
func refusalFromWorker(reason error) error {
	GinkgoHelper()
	var frame bytes.Buffer
	Expect(cluster.WriteStreamRefusal(&frame, reason)).To(Succeed())
	readBack := cluster.ReadStreamReply(&frame)
	Expect(readBack).To(MatchError(reason))
	return fmt.Errorf("opening %q on node %q: %w", "grpc", "node-1", readBack)
}

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

	It("evicts a remote model whose backend the WORKER ITSELF could not reach", func() {
		// The shape a crashed backend takes since workers stopped listening:
		// the tunnel carried the request, the worker read it and answered that
		// nothing is listening on that port. That is the worker speaking about
		// its backend, not a transport failure, and reading it as one pinned a
		// genuinely dead model in this cache forever, failing every request.
		client := grpc.NewClientWithDialer("10.0.0.1:9001", false, nil, false, "",
			func(context.Context, string) (net.Conn, error) {
				return nil, refusalFromWorker(cluster.ErrStreamTargetUnavailable)
			})
		m := NewModelWithClient("refused-model", "10.0.0.1:9001", client)
		ml.store.Set("refused-model", m)

		Expect(ml.checkIsLoaded("refused-model")).To(BeNil())
		_, stillThere := ml.store.Get("refused-model")
		Expect(stillThere).To(BeFalse())
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

	It("evicts when the WORKER ITSELF refused the stream to the backend", func() {
		// The same rule on the INFERENCE path. A refusal the worker wrote is
		// the worker reporting its backend gone, so the model must be evicted
		// here exactly as a locally spawned one would be; the guard is for a
		// route this frontend lost, which is a different condition.
		evicted := 0
		client := newConnectionEvictingClient(
			failingDial(refusalFromWorker(cluster.ErrStreamTargetUnavailable)),
			"refused-model", func() { evicted++ })

		_, err := client.Predict(context.Background(), &pb.PredictOptions{})
		Expect(err).To(HaveOccurred())
		Expect(evicted).To(Equal(1))
	})

	It("does not evict on a refusal code this frontend does not recognise", func() {
		// The boundary. An unrecognised code is a newer worker's vocabulary,
		// which WorkerDialer reports under the no-route umbrella, and acting on
		// it would let a worker upgrade stop models that are running.
		evicted := 0
		client := newConnectionEvictingClient(
			failingDial(fmt.Errorf("reaching node %q: %w: opening %q: tunnel stream refused with unrecognised code %q: %s",
				"node-1", cluster.ErrNoRoute, "grpc", "quiesced", "this worker is draining")),
			"remote-model", func() { evicted++ })

		_, err := client.Predict(context.Background(), &pb.PredictOptions{})
		Expect(err).To(HaveOccurred())
		Expect(evicted).To(BeZero())
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
