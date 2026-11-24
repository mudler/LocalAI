package p2prelaymgr

import (
	"context"
	"errors"
	"time"
)

// Relay states. They are what the interface shows as the badge of a relay.
const (
	// StateInSync: this server is an admin of the relay and the relay holds
	// exactly its paired set, and the node peers that fit.
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
	// ErrNotAdmin means that this server cannot change the limits of the
	// relay because it is not an admin of a gated relay.
	ErrNotAdmin = errors.New("p2prelaymgr: this server is not an admin of the relay")
	// ErrInvalidLimits means that the relay refused the limits.
	ErrInvalidLimits = errors.New("p2prelaymgr: the relay refused the limits")
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

// LimitsPatch changes limits. A nil field stays as it is; zero removes the limit.
type LimitsPatch struct {
	CircuitBytes   *int64
	CircuitSeconds *int64
	RateBPS        *int64
}

// LimitsInfo is what a relay reports about its limits and whether this
// server may change them.
type LimitsInfo struct {
	Limits Limits
	// Ceiling is the most an admin may set for each limit. Zero means no
	// ceiling. A value above it is refused with ErrInvalidLimits, and with a
	// ceiling a limit cannot be removed.
	Ceiling Limits
	// CanChange is true when this server is an admin of a gated relay.
	CanChange bool
	// Restarted is set on the result of SetLimits when the relay restarted
	// its circuit service to apply the change: its reservations were
	// dropped and the other peers reserve again within moments.
	Restarted bool
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

// Admission states of a node peer on one relay (see Manager.NodeAdmission).
const (
	// AdmissionAdmitted: the relay holds the peer in this server's list.
	AdmissionAdmitted = "admitted"
	// AdmissionRefused: the peer does not fit in the list the relay allows
	// this server; the paired devices come first.
	AdmissionRefused = "refused"
	// AdmissionPending: the server has not brought the relay in line yet, or
	// cannot reach it.
	AdmissionPending = "pending"
	// AdmissionSkipped: this server does not keep the list of the relay (an
	// open relay, or a relay it is not an admin of).
	AdmissionSkipped = "skipped"
)

// Admission is the state of a node peer on one relay.
type Admission struct {
	RelayID string
	Label   string
	// State is one of the Admission constants.
	State string
	// Reason is a short text for people; empty when admitted.
	Reason string
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
	// Limits reads the limits the relay runs with now. Any member can read
	// them; CanChange says whether this server can change them.
	Limits(ctx context.Context, id string) (LimitsInfo, error)
	// SetLimits changes the limits of a relay this server administers. It
	// returns ErrNotAdmin otherwise.
	SetLimits(ctx context.Context, id string, patch LimitsPatch) (LimitsInfo, error)
	// RegisterPass registers a one-time relay pass with a TTL.
	RegisterPass(ctx context.Context, relayID, code string, ttl time.Duration) error
	CancelPass(ctx context.Context, relayID, code string) error
	// PeerAdded and PeerRemoved queue the change for every relay. They return
	// at once; the manager retries until the relay takes the change.
	PeerAdded(peer string)
	PeerRemoved(peer string)
	// Resync compares the list of every relay with the paired set and the
	// node peers, and repairs it, in the background. A relay whose list is
	// already right gets no write. Call it when the node peers change.
	Resync()
	// NodeAdmission reports, relay by relay, whether the relays admit a node
	// peer. It is empty for a peer that Config.NodePeers does not list.
	NodeAdmission(peer string) []Admission
	// Start runs the reconnect loop: a full push on every connect and a
	// digest comparison every minute. It returns at once.
	Start(ctx context.Context) error
	Close() error
}
