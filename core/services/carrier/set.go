// Package carrier holds the transports of a distributed deployment behind the
// seams that cross a process boundary.
//
// Every seam (fan-out, control verbs, file staging, backend dials, agent RPC)
// is an interface. The members split along two axes:
//
//   - A WorkerRoute is one way to reach a worker: the control verbs, the file
//     stager, the gRPC client factory, the dialer of the HTTP server of the
//     worker and the agent RPC. A frontend may hold several, one for each kind
//     of route (nodes.RouteKind), and the RouteTable picks one for each node
//     from the routes that the node has (nodes.RoutesOf).
//   - A Fabric is what the processes of the cluster use among themselves: the
//     broadcaster, request and reply, and the reconnect event.
//
// The holders in this package implement the seam interfaces. The holders of the
// worker-facing seams forward each call through the route table; the
// broadcaster holder forwards to the Fabric of the Set that an atomic pointer
// names.
//
// This package names interfaces only. The code that builds the routes and the
// sets is the one place that names a transport (see nats.go and tunnel.go).
package carrier

import (
	"errors"
	"fmt"
	"sync/atomic"

	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// FileCarrier is a file stager with the batch release the file staging client
// looks for. It is one interface so that a carrier cannot leave the extension
// out, and the holder cannot hide it.
type FileCarrier interface {
	nodes.FileStager
	nodes.RequestFileReleaser
}

// WorkerRoute is the implementation of the worker-facing seams for one kind of
// route. The RouteTable holds one for each kind and picks one for each node.
type WorkerRoute struct {
	// Kind is the kind of route that this implements.
	Kind     nodes.RouteKind
	Commands nodes.NodeControl
	Files    FileCarrier
	Clients  nodes.BackendClientFactory
	Dialer   nodes.WorkerNetDialerFor
	// Agents carries the MCP requests to an agent worker. It is nil for a route
	// that has no agent RPC, and the agent holder then asks the next route.
	Agents mcpTools.AgentControl

	// ForgetNode drops what the route caches for a node that left or lost its
	// right to a connection, and closes the idle streams that it holds. It may
	// be nil for a route that caches nothing per node.
	ForgetNode func(nodeID string)
	// Close releases the connections of the route. It may be nil.
	Close func()
}

// Validate reports the first member that is missing.
func (r *WorkerRoute) Validate() error {
	if r == nil {
		return errors.New("worker route is nil")
	}
	if r.Kind != nodes.RouteDirect && r.Kind != nodes.RouteTunnel {
		return fmt.Errorf("worker route has an unknown kind %q", r.Kind)
	}
	for _, m := range []struct {
		name string
		ok   bool
	}{
		{"Commands", r.Commands != nil},
		{"Files", r.Files != nil},
		{"Clients", r.Clients != nil},
		{"Dialer", r.Dialer != nil},
	} {
		if !m.ok {
			return fmt.Errorf("%s worker route has no %s", r.Kind, m.name)
		}
	}
	return nil
}

// Fabric is what the processes of the cluster (frontends and chat workers) use
// among themselves. Workers never use it.
type Fabric struct {
	// Broadcaster carries fan-out and nothing else. A subject outside the
	// broadcast roots (see messaging.ValidateBroadcastSubject) is not served
	// through it: control traffic uses the worker routes.
	Broadcaster messaging.Broadcaster
	// OnReconnect registers a callback the carrier runs after it recovers a
	// lost connection. It is nil for a carrier with no such event.
	OnReconnect func(func())
	// Replies carries request and reply between the processes of the cluster:
	// the chat start, the replay of chat events and the background jobs of a
	// turn. It is nil for a carrier that has none, and the holder then answers
	// every request with messaging.ErrNotDelivered. That is not "nobody
	// listens": the callers handle it as a request that reached no process.
	Replies messaging.RequestReply
}

// Set is one carrier of the cluster carrier row: its Fabric and the worker route
// that it brings. The broadcaster holder publishes on the Fabric of the Set that
// the row names, and the change of carrier (Swapper) moves it. Worker calls do
// not follow the row: the route table holds the worker route of a set while
// the set is in use or drains (RouteTable.Attach).
//
// The Set, the carrier row and the change of carrier are removed with the
// switch (step 8 of the hybrid carriers work); the Fabric then stands alone.
// Build a set completely and call Validate before storing it where holders can
// see it.
type Set struct {
	// Name is the carrier this set belongs to.
	Name cluster.Carrier
	// Epoch is the epoch of the cluster row the set was built for.
	Epoch int64
	// NATSURL is the address of the NATS server that a NATS set was built from,
	// as the row of the cluster named it. A set that was built from another
	// address is not the set that the row asks for. Empty for the tunnel.
	NATSURL string

	Fabric
	// WorkerRoute is the route to the workers that this carrier brings. A
	// tunnel set may share the route that the table pins (TunnelOptions.Routes),
	// and its Close then leaves that route open.
	*WorkerRoute

	// Close releases the carrier's connections. It may be nil.
	Close func()

	// The fan-out holder (Broadcaster) keeps these. publishing counts the
	// publishes that run on the set, and released says that the holder dropped
	// the subscriptions of the set. Release waits for the count to reach zero, so
	// a publish that started on the set reaches its subscribers. A set belongs to
	// one holder.
	publishing atomic.Int64
	released   atomic.Bool
}

// Validate reports the first member that is missing.
func (s *Set) Validate() error {
	if s == nil {
		return errors.New("carrier set is nil")
	}
	if s.Name != cluster.CarrierNATS && s.Name != cluster.CarrierTunnel {
		return fmt.Errorf("%w: %q", cluster.ErrInvalidCarrier, s.Name)
	}
	if s.Broadcaster == nil {
		return fmt.Errorf("carrier set %q has no Broadcaster", s.Name)
	}
	if s.WorkerRoute == nil {
		return fmt.Errorf("carrier set %q has no worker route", s.Name)
	}
	if err := s.WorkerRoute.Validate(); err != nil {
		return fmt.Errorf("carrier set %q: %w", s.Name, err)
	}
	if s.Kind != routeKindOf(s.Name) {
		return fmt.Errorf("carrier set %q brings a %s route", s.Name, s.Kind)
	}
	if s.Agents == nil {
		return fmt.Errorf("carrier set %q has no Agents", s.Name)
	}
	return nil
}

// routeKindOf is the kind of the route that a carrier brings.
func routeKindOf(c cluster.Carrier) nodes.RouteKind {
	if c == cluster.CarrierTunnel {
		return nodes.RouteTunnel
	}
	return nodes.RouteDirect
}
