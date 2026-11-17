package p2prelaymgr

import (
	"context"
	"errors"
	"time"
)

// Relay states. They are what the interface shows as the badge of a relay.
const (
	// StateInSync: this server is an admin of the relay and the relay holds
	// exactly its paired set.
	StateInSync = "in_sync"
	// StateOutOfSync: this server is an admin but the relay's set differs and
	// has not been repaired yet.
	StateOutOfSync = "out_of_sync"
	// StateOpen: the relay is open. Anyone can use it.
	StateOpen = "open"
	// StateUnclaimed: the relay has no admin yet and waits for a claim code.
	StateUnclaimed = "unclaimed"
	// StateNotAdmin: this server is a member of the relay, not an admin.
	StateNotAdmin = "not_admin"
	// StateDenied: the relay does not know this server.
	StateDenied = "denied"
	// StateUnreachable: no connection to the relay.
	StateUnreachable = "unreachable"
	// StateChecking: a check is running.
	StateChecking = "checking"
)

// Relay modes, as the relay reports them.
const (
	ModeGated     = "gated"
	ModeOpen      = "open"
	ModeUnclaimed = "unclaimed"
	ModeUnknown   = "unknown"
)

var (
	// ErrBadAddr means that a relay address is not a multiaddr that ends in
	// /p2p/<peer ID>.
	ErrBadAddr = errors.New("p2prelaymgr: the relay address must be a multiaddr that ends in /p2p/<relay peer ID>")
	// ErrNotFound means that no relay has the ID.
	ErrNotFound = errors.New("p2prelaymgr: unknown relay")
	// ErrClaimRefused means that the relay refused the claim code.
	ErrClaimRefused = errors.New("p2prelaymgr: the relay refused the claim code")
)

// Limits are the limits a relay reports. Zero means no limit.
type Limits struct {
	CircuitBytes   int64
	CircuitSeconds int64
	// RateBPS is the bandwidth limit per circuit, in bytes per second.
	RateBPS int64
}

// Relay is a relay of this server and what the server knows about it.
type Relay struct {
	ID    string
	Label string
	// Addr is a multiaddr that ends in /p2p/<relay peer ID>.
	Addr string
	// Mode is "gated", "open", "unclaimed" or "unknown" (not read yet).
	Mode string
	// State is one of the State constants.
	State string
	// Health is a short text for people.
	Health  string
	Latency time.Duration
	Admins  int
	Members int
	Limits  Limits
	// CheckedAt is when the state was last read from the relay.
	CheckedAt time.Time
}

// Check is the result of Manager.Check, step by step.
type Check struct {
	Connected bool
	Reserved  bool
	AdminOK   bool
	CircuitOK bool
	InSync    bool
	// Detail has one line per step, in order.
	Detail []string
	Relay  Relay
}

// Manager keeps the relays a server uses and their allowlists in sync.
// A Manager is safe for concurrent use.
type Manager interface {
	List() []Relay
	// Add adds a relay, reads its state and, when this server is an admin,
	// pushes the paired set. A claim code is used once and not stored. An
	// unreachable relay is added anyway; its state says so.
	Add(ctx context.Context, addr, label, claimCode string) (Relay, error)
	Remove(ctx context.Context, id string) error
	// Check connects, reserves, reads the admin status, opens a test circuit
	// and compares the digest. It changes nothing on the relay.
	Check(ctx context.Context, id string) (Check, error)
	// Sync pushes the full paired set now.
	Sync(ctx context.Context, id string) (Relay, error)
	Claim(ctx context.Context, id, code string) (Relay, error)
	// RegisterPass registers a one-time relay pass with a TTL.
	RegisterPass(ctx context.Context, relayID, code string, ttl time.Duration) error
	CancelPass(ctx context.Context, relayID, code string) error
	// PeerAdded and PeerRemoved queue the change for every relay. They return
	// at once; the manager retries until the relay takes the change.
	PeerAdded(peer string)
	PeerRemoved(peer string)
	// Start runs the reconnect loop: a full push on every connect and a
	// digest comparison every minute. It returns at once.
	Start(ctx context.Context) error
	Close() error
}
