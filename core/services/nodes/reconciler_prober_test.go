// SPDX-License-Identifier: MIT

package nodes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mudler/LocalAI/core/services/cluster"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// refusalFromWorker builds the error a frontend actually holds after a worker
// refused one of its streams.
//
// It goes over the WIRE rather than being handed the sentinel directly: the
// refusal is written with the worker's own writer and read back with the
// frontend's own reader, so a reason the protocol cannot carry, or a code
// mapping that stopped round-tripping, reddens these specs instead of leaving
// them asserting against a value production never produces.
//
// The wrap is chosen by cluster.IsWorkerAnswer, which is what
// WorkerDialer.handshake does, so the spec exercises the real branch rather
// than a transcription of it. That is not circular: the helper decides the
// SHAPE of the error and the table below states the OUTCOME independently, so
// moving a sentinel into or out of the predicate reddens the table. In
// particular, adding ErrStreamNotServed to the predicate would strip its
// umbrella here and turn its ProbeUnknown entry red, which is the property that
// keeps a transient worker-side failure from reaping a live model.
func refusalFromWorker(reason error) error {
	GinkgoHelper()
	var frame bytes.Buffer
	Expect(cluster.WriteStreamRefusal(&frame, reason)).To(Succeed())
	readBack := cluster.ReadStreamReply(&frame)
	Expect(readBack).To(MatchError(reason), "the refusal must survive its own round trip")
	Expect(readBack).ToNot(MatchError(cluster.ErrNoRoute))
	if cluster.IsWorkerAnswer(readBack) {
		return fmt.Errorf("opening %q on node %q: %w", "grpc", "node-1", readBack)
	}
	return fmt.Errorf("reaching node %q: %w: opening %q: %w", "node-1", cluster.ErrNoRoute, "grpc", readBack)
}

// proberFactory hands the prober one client, and records what it was asked for.
type proberFactory struct {
	client grpc.Backend
	err    error
	asked  []string
}

func (f *proberFactory) NewClientForNode(nodeID, address string, _ bool) (grpc.Backend, error) {
	f.asked = append(f.asked, nodeID+"|"+address)
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

var _ = Describe("the reconciler's gRPC model prober", func() {
	// The two lines that decide whether a row survives, both previously
	// untested. Everything else in the reaper is driven through fakeProber,
	// which means the mapping from a real client to a ProbeOutcome had nothing
	// holding it at all.
	probe := func(f *proberFactory) ProbeOutcome {
		GinkgoHelper()
		return grpcModelProber{clients: f}.Probe(context.Background(), "node-1", "10.0.0.1:9001")
	}

	It("answers ProbeUnknown when no client can be built for the node", func() {
		Expect(probe(&proberFactory{err: ErrNoWorkerDialer})).To(Equal(ProbeUnknown))
	})

	It("answers ProbeUnknown when the client was built and the tunnel dial failed", func() {
		// The likelier half. ProbeUnreachable here would delete the row after
		// probeFailuresBeforeReap passes of a peer link that was merely
		// restarting, and the backend would still be running the model.
		Expect(probe(&proberFactory{client: &fakeBackendClient{
			healthy: false,
			err:     fmt.Errorf("rpc error: code = Unavailable"),
			dialErr: fmt.Errorf("%w: %w", cluster.ErrNoRoute, cluster.ErrPeerUnreachable),
		}})).To(Equal(ProbeUnknown))
	})

	It("asks for the client by NODE, not by address alone", func() {
		f := &proberFactory{client: &fakeBackendClient{healthy: true}}
		Expect(probe(f)).To(Equal(ProbeAlive))
		Expect(f.asked).To(ContainElement("node-1|10.0.0.1:9001"))
	})

	It("answers ProbeAlive for a healthy backend it reached", func() {
		Expect(probe(&proberFactory{client: &fakeBackendClient{healthy: true}})).To(Equal(ProbeAlive))
	})

	It("still answers ProbeUnreachable for a backend that answered unhealthy", func() {
		// One of the two shapes a dead backend takes, and the easy one: the
		// process is up enough to answer and reports itself unhealthy over a
		// working transport. It is a ghost and its row should go.
		//
		// This spec used to be named for the property the table below holds,
		// which it never tested: a backend process that DIED on a tunnelled
		// worker does not answer at all, and what the frontend gets back is the
		// worker's refusal, not an unhealthy reply.
		Expect(probe(&proberFactory{client: &fakeBackendClient{healthy: false}})).To(Equal(ProbeUnreachable))
	})

	DescribeTable("answers ProbeUnreachable when the WORKER ITSELF refused the stream",
		// The dominant shape of a dead backend since workers stopped listening,
		// and the one that produced a permanently unreapable row. The worker is
		// healthy, connected and answering; what it answers is that the stream
		// cannot be served. That is evidence about the backend, so the reaper
		// may act on it. Reported as ProbeUnknown instead, no reap path deleted
		// the row, the replica slot never freed, and at the default
		// MaxReplicasPerModel=1 the only remaining cleanup was LRU eviction of
		// models that were working.
		func(reason error) {
			Expect(probe(&proberFactory{client: &fakeBackendClient{
				healthy: false,
				err:     status.Error(codes.Unavailable, "connection error: transport"),
				dialErr: refusalFromWorker(reason),
			}})).To(Equal(ProbeUnreachable))
		},
		Entry("the worker could not reach the backend process", cluster.ErrStreamTargetUnavailable),
		Entry("the worker does not serve gRPC streams at all", cluster.ErrStreamTagUnknown),
		Entry("the worker rejected the stored address", cluster.ErrStreamRequestInvalid),
	)

	It("answers ProbeUnknown when the worker refused but said it learned nothing", func() {
		// The fourth refusal, and the boundary that keeps the three above safe
		// to act on. A worker answers with it for its OWN transient conditions:
		// a request frame that never arrived in time, a stream whose deadline
		// would not arm, a local dial that ended on the session going away.
		// Those clear on a reconnect, so acting on them would convert
		// peer-link congestion into a reaped row, and on the inference path
		// into a model stopped across the fleet.
		//
		// This is not hypothetical: the header-timeout case USED to arrive as
		// ErrStreamRequestInvalid, which the table above reaps on.
		Expect(probe(&proberFactory{client: &fakeBackendClient{
			healthy: false,
			err:     status.Error(codes.Unavailable, "connection error: transport"),
			dialErr: refusalFromWorker(cluster.ErrStreamNotServed),
		}})).To(Equal(ProbeUnknown))
	})

	It("answers ProbeUnknown for a refusal code this frontend does not recognise", func() {
		// The other direction, and the boundary of the exemption above. A
		// newer worker's vocabulary must not be read as evidence about a
		// backend: ReadStreamReply returns an unrecognised code as a plain
		// error, WorkerDialer puts the no-route umbrella on it, and the row
		// survives. Guessing wrong here costs a retry; guessing wrong the other
		// way costs a reaped replica.
		unknownCode := fmt.Errorf("reaching node %q: %w: opening %q: tunnel stream refused with unrecognised code %q: %s",
			"node-1", cluster.ErrNoRoute, "grpc", "quiesced", "this worker is draining")
		Expect(probe(&proberFactory{client: &fakeBackendClient{
			healthy: false,
			err:     status.Error(codes.Unavailable, "connection error: transport"),
			dialErr: unknownCode,
		}})).To(Equal(ProbeUnknown))
	})

	It("answers ProbeUnknown when the tunnel broke while reading the worker's reply", func() {
		// The condition a refusal is most easily confused with, kept apart on
		// purpose: a read failure is the tunnel breaking, not the worker
		// speaking, and it says nothing about the backend.
		Expect(probe(&proberFactory{client: &fakeBackendClient{
			healthy: false,
			err:     status.Error(codes.Unavailable, "connection error: transport"),
			dialErr: fmt.Errorf("reaching node %q: %w: opening %q: reading a tunnel stream reply: %w",
				"node-1", cluster.ErrNoRoute, "grpc", io.ErrUnexpectedEOF),
		}})).To(Equal(ProbeUnknown))
	})

	It("does not report a transport that recovered", func() {
		// LastDialError is cleared by a successful dial, so a client that
		// failed once and then reconnected must not keep reading as
		// unroutable; otherwise a row could never be reaped again after one
		// blip on that client.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = listener.Close() })

		attempt := 0
		f, err := NewTunnelClientFactory("", func(string) func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return func(ctx context.Context, _ string) (net.Conn, error) {
				attempt++
				if attempt == 1 {
					return nil, errors.New("first dial fails")
				}
				return d.DialContext(ctx, "tcp", listener.Addr().String())
			}
		})
		Expect(err).ToNot(HaveOccurred())
		client, err := f.NewClientForNode("node-1", "10.0.0.1:9001", false)
		Expect(err).ToNot(HaveOccurred())

		_, _ = client.HealthCheck(context.Background())
		Expect(unroutable(client)).ToNot(BeNil())

		// gRPC re-dials on the next call; the listener now accepts.
		Eventually(func() error {
			_, _ = client.HealthCheck(context.Background())
			return unroutable(client)
		}, "20s").Should(BeNil())
	})
})
