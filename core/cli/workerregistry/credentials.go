package workerregistry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/xlog"
)

// statusPending mirrors nodes.StatusPending. It is duplicated rather than
// imported so the lightweight registration client does not pull in the nodes
// package (and its gorm/DB dependencies).
const statusPending = "pending"

// defaultMaxAttempts bounds how many times Acquire registers before giving up.
// It is high enough to ride out a slow admin approval or a transient frontend
// outage, but finite so an unauthorized/unapprovable worker exits and surfaces
// the problem (via a non-zero exit and the resulting restart) rather than
// waiting forever.
const defaultMaxAttempts = 100

// RegisterFunc performs one idempotent registration round-trip.
type RegisterFunc func(ctx context.Context) (*RegisterResponse, error)

// CredentialManager acquires a node's own credentials at startup, waiting
// through admin approval when that is required, and holds the tunnel token the
// most recent registration minted. Safe for concurrent use.
//
// Renamed from NATSCredentialManager and stripped rather than deleted. The JWT
// half went with the message bus: nothing mints a broker credential and nothing
// opens a connection to present one on. The tunnel token did not go with it,
// and it is the reason a manager is still worth having: it is ROTATED by a
// registration rather than expiring on a clock, and the frontend keeps only its
// hash, so the dialer has to read the current value at dial time instead of
// being handed one at startup.
type CredentialManager struct {
	register RegisterFunc
	// requireApproval blocks Acquire until the node is out of pending.
	//
	// Narrower than the requireCreds it replaces: there is no credential left
	// to wait for being MINTED, only an admin decision to wait through. A
	// worker that proceeds while pending registers and heartbeats fine, and is
	// then refused at every tunnel dial, so an operator who wants the wait
	// rather than the refusal loop asks for it here.
	requireApproval bool

	// Tunables; defaults set by NewCredentialManager, overridable in tests.
	initialBackoff time.Duration
	maxBackoff     time.Duration
	maxAttempts    int // bound on Acquire attempts (<=0 = unlimited)

	mu     sync.RWMutex
	nodeID string
	// tunnelToken is the node's own tunnel credential from the most recent
	// registration. It is kept here because every re-registration this manager
	// performs ROTATES it, so the tunnel client has to read the current value
	// at dial time rather than be handed one at startup.
	tunnelToken string
}

// NewCredentialManager builds a manager over register. When requireApproval is
// true, Acquire blocks through admin approval instead of returning a pending
// response.
func NewCredentialManager(register RegisterFunc, requireApproval bool) *CredentialManager {
	return &CredentialManager{
		register:        register,
		requireApproval: requireApproval,
		initialBackoff:  2 * time.Second,
		maxBackoff:      30 * time.Second,
		maxAttempts:     defaultMaxAttempts,
	}
}

func (m *CredentialManager) store(res *RegisterResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodeID = res.ID
	// A response that carries no tunnel token (a frontend that predates them,
	// or one whose minting failed) must not wipe a working credential this
	// worker already holds. Overwriting with "" would lock the tunnel out until
	// the next registration that did carry one, which is the opposite of what
	// an empty field means.
	if res.TunnelToken != "" {
		m.tunnelToken = res.TunnelToken
	}
}

// TunnelToken returns the node's current tunnel credential, empty until one has
// been issued. It is the callback the tunnel client reads on every dial.
func (m *CredentialManager) TunnelToken() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tunnelToken
}

// NodeID returns the node ID from the most recent registration.
func (m *CredentialManager) NodeID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nodeID
}

// Acquire registers and, when requireApproval is set, keeps re-registering with
// exponential backoff until the node is approved (status != pending). Without
// requireApproval it returns the first successful response.
func (m *CredentialManager) Acquire(ctx context.Context) (*RegisterResponse, error) {
	backoff := m.initialBackoff
	var lastReason error
	for attempt := 1; m.maxAttempts <= 0 || attempt <= m.maxAttempts; attempt++ {
		res, err := m.register(ctx)
		switch {
		case errors.Is(err, ErrRegistrationRejected):
			// The frontend refused rather than failed. Waiting through the full
			// attempt ladder would delay the operator's only explanation by the
			// length of the ladder and change nothing about the answer.
			return nil, err
		case err != nil:
			lastReason = err
			xlog.Warn("Registration failed, retrying", "attempt", attempt, "next_retry", backoff, "error", err)
		case !m.requireApproval:
			m.store(res)
			return res, nil
		case res.Status == statusPending:
			lastReason = fmt.Errorf("node %s still pending admin approval", res.ID)
			xlog.Info("Node pending admin approval; waiting", "node", res.ID, "attempt", attempt, "next_retry", backoff)
		default:
			m.store(res)
			return res, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, m.maxBackoff)
	}
	return nil, fmt.Errorf("giving up registering after %d attempts: %w", m.maxAttempts, lastReason)
}
