// Package carrier makes the transport of a distributed deployment replaceable
// at runtime.
//
// Every seam that crosses a process boundary (fan-out, control verbs,
// file staging, backend dials, agent RPC) is an interface. A Set is one
// carrier's implementation of all of them. The holders in this package
// implement the same interfaces and forward each call to the Set that an
// atomic pointer names at that moment, so code that was built against an
// interface keeps working while the pointer moves.
//
// This package names interfaces only. The code that builds a Set for a
// carrier is the one place that names the carrier (see nats.go).
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

// Set is the implementation of every seam for one carrier. Build it completely
// and call Validate before storing it where holders can see it.
type Set struct {
	// Name is the carrier this set belongs to.
	Name cluster.Carrier
	// Epoch is the epoch of the cluster row the set was built for.
	Epoch int64
	// NATSURL is the address of the NATS server that a NATS set was built from,
	// as the row of the cluster named it. A set that was built from another
	// address is not the set that the row asks for. Empty for the tunnel.
	NATSURL string

	// Broadcaster carries fan-out and nothing else. A subject outside the
	// broadcast roots (see messaging.ValidateBroadcastSubject) is not served
	// through it: control traffic uses Commands, Clients and Agents.
	Broadcaster messaging.Broadcaster
	// OnReconnect registers a callback the carrier runs after it recovers a
	// lost connection. It is nil for a carrier with no such event.
	OnReconnect func(func())
	Commands    nodes.NodeControl
	Files       FileCarrier
	Clients     nodes.BackendClientFactory
	Dialer      nodes.WorkerNetDialerFor
	Agents      mcpTools.AgentControl
	// Replies carries request and reply between the processes of the cluster:
	// the chat start, the replay of chat events and the background jobs of a
	// turn. It is nil for a carrier that has none, and the holder then answers
	// every request with messaging.ErrNotDelivered. That is not "nobody
	// listens": the callers handle it as a request that reached no process.
	Replies messaging.RequestReply

	// ForgetNode drops what the set caches for a node that left or lost its
	// right to a connection, and closes the idle streams that it holds. It may
	// be nil for a carrier that caches nothing per node.
	ForgetNode func(nodeID string)

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
	for _, m := range []struct {
		name string
		ok   bool
	}{
		{"Broadcaster", s.Broadcaster != nil},
		{"Commands", s.Commands != nil},
		{"Files", s.Files != nil},
		{"Clients", s.Clients != nil},
		{"Dialer", s.Dialer != nil},
		{"Agents", s.Agents != nil},
	} {
		if !m.ok {
			return fmt.Errorf("carrier set %q has no %s", s.Name, m.name)
		}
	}
	return nil
}
