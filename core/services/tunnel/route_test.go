package tunnel_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/libp2p/go-yamux/v5"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// netTimeout is a timeout of a socket that is not os.ErrDeadlineExceeded.
type netTimeout struct{}

func (netTimeout) Error() string   { return "i/o timeout" }
func (netTimeout) Timeout() bool   { return true }
func (netTimeout) Temporary() bool { return true }

var _ net.Error = netTimeout{}

// The failure of a dial reaches a scheduler, and the scheduler demotes a worker
// on ErrNoRoute. ErrNoRoute therefore says that no route exists, and a path that
// is slow, a peer that is gone, a database that does not answer and a reply that
// makes no sense are other conditions.
var _ = DescribeTable("The classification of a failed dial",
	func(cause error, want error, wantNoRoute bool) {
		got := tunnel.RouteFailure("w1", cause)
		if want != nil {
			Expect(errors.Is(got, want)).To(BeTrue(), "%v", got)
		}
		Expect(errors.Is(got, tunnel.ErrNoRoute)).To(Equal(wantNoRoute), "%v", got)
		Expect(errors.Is(got, cluster.ErrNoConnection)).To(BeFalse(), "absence must not reach the caller")
		Expect(errors.Is(got, cluster.ErrInstanceNotFound)).To(BeFalse())
	},
	Entry("a socket deadline that ran out", fmt.Errorf("reading a tunnel stream reply: %w", os.ErrDeadlineExceeded), tunnel.ErrTransport, false),
	Entry("a timeout of the network", fmt.Errorf("writing: %w", netTimeout{}), tunnel.ErrTransport, false),
	Entry("a timeout of the multiplexer", fmt.Errorf("opening a stream: %w", yamux.ErrTimeout), tunnel.ErrTransport, false),
	Entry("a reply that is no part of the protocol", fmt.Errorf("%w: %q", tunnel.ErrProtocol, "garbage"), tunnel.ErrProtocol, false),
	Entry("a database that did not answer", fmt.Errorf("%w: connection refused", tunnel.ErrInfrastructure), tunnel.ErrInfrastructure, false),
	Entry("a peer that did not answer", fmt.Errorf("through replica: %w", tunnel.ErrPeerUnreachable), tunnel.ErrPeerUnreachable, false),
	Entry("the budget of the caller", context.DeadlineExceeded, context.DeadlineExceeded, false),
	Entry("a cancelled caller", context.Canceled, context.Canceled, false),
	Entry("a bulk lane that is down", tunnel.ErrNoBulkSession, tunnel.ErrNoBulkSession, false),
	Entry("a node that no replica holds", cluster.ErrNoConnection, nil, true),
	Entry("a replica that is not registered", cluster.ErrInstanceNotFound, nil, true),
	Entry("a tunnel that is not held here", tunnel.ErrNotOwner, tunnel.ErrNotOwner, true),
	Entry("a replica that cannot relay", tunnel.ErrNoRelayPath, tunnel.ErrNoRelayPath, true),
	Entry("an owner that could not open a stream", tunnel.ErrRelayUnavailable, tunnel.ErrRelayUnavailable, true),
	Entry("a worker that could not serve the stream", tunnel.ErrStreamNotServed, tunnel.ErrStreamNotServed, true),
	Entry("a session that ended under the request", yamux.ErrSessionShutdown, yamux.ErrSessionShutdown, true),
	Entry("a stream that was closed under the request", fmt.Errorf("reading: %w", io.EOF), io.EOF, true),
)
