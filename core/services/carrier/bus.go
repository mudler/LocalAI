package carrier

import (
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// The names of the buses that the frontends and chat workers broadcast on.
const (
	// BusPostgres is LISTEN and NOTIFY on the cluster database. Every frontend
	// and chat worker has it, so it is the floor: a broadcast that it carries
	// reaches every one of them.
	BusPostgres = "postgres"
	// BusNATS is the NATS server of the cluster, when one is configured.
	BusNATS = "nats"
)

// BusState is the broadcast bus of one process, as the Nodes page shows it.
type BusState struct {
	// Bus is the bus that the process publishes on now: BusNATS or BusPostgres.
	Bus string `json:"bus"`
	// NATSConfigured says that the cluster has a NATS server, and NATSConnected
	// that the process reaches it.
	NATSConfigured bool `json:"nats_configured"`
	NATSConnected  bool `json:"nats_connected"`
}

// ErrBusUnreachable says that a bus which is configured for the cluster cannot
// be reached from this process. The process still listens and publishes on the
// floor, but it does not hear what the others publish on that bus, so it is not
// ready to serve.
var ErrBusUnreachable = errors.New("a configured bus is not reachable")

// Bus is one bus that the broadcaster holder listens on.
type Bus struct {
	// Name names the bus in log lines and errors (BusPostgres, BusNATS).
	Name string
	// Broadcaster carries the broadcasts of the bus.
	Broadcaster messaging.Broadcaster
	// OnReconnect registers a callback that runs after the bus recovers a lost
	// connection. It may be nil.
	OnReconnect func(func())
	// Connected says whether this process reaches the bus now. Nil means always.
	Connected func() bool
	// Close releases the connection of the bus. It may be nil.
	Close func()
}

// Validate reports a bus that cannot carry broadcasts.
func (b *Bus) Validate() error {
	switch {
	case b == nil:
		return errors.New("bus is nil")
	case b.Broadcaster == nil:
		return fmt.Errorf("bus %q has no broadcaster", b.Name)
	}
	return nil
}

func (b *Bus) connected() bool { return b.Connected == nil || b.Connected() }

func (b *Bus) close() {
	if b != nil && b.Close != nil {
		b.Close()
	}
}
