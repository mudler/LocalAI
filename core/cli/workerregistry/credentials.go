package workerregistry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/mudler/xlog"
)

// statusPending mirrors nodes.StatusPending. It is duplicated rather than
// imported so the lightweight registration client does not pull in the nodes
// package (and its gorm/DB dependencies).
const statusPending = "pending"

// defaultMaxAttempts bounds how many times Acquire registers (and how many
// consecutive times RefreshLoop may fail) before giving up. It is high enough
// to ride out a slow admin approval or a transient frontend outage, but finite
// so an unauthorized/unapprovable worker exits and surfaces the problem (via a
// non-zero exit and the resulting restart) rather than waiting forever.
const defaultMaxAttempts = 100

// RegisterFunc performs one idempotent registration round-trip.
type RegisterFunc func(ctx context.Context) (*RegisterResponse, error)

// NATSCredentialManager acquires NATS credentials at startup — waiting through
// admin approval when required — and refreshes them before the minted JWT
// expires, by re-registering (which mints a fresh JWT). The live NATS
// connection adopts a refreshed JWT on its next reconnect via Provider. Safe
// for concurrent use.
//
// It addresses two failure modes: a worker that needs credentials but registers
// while still pending approval (it would otherwise give up and never connect),
// and a long-running worker whose 24h JWT expires with no way to renew it.
type NATSCredentialManager struct {
	register     RegisterFunc
	requireCreds bool // block until credentials are present (frontend minting in use)

	// Tunables; defaults set by NewNATSCredentialManager, overridable in tests.
	initialBackoff time.Duration
	maxBackoff     time.Duration
	maxAttempts    int     // bound on Acquire attempts / consecutive refresh failures (<=0 = unlimited)
	refreshLead    float64 // refresh once this fraction of the JWT lifetime has elapsed
	refreshRetry   time.Duration
	expiryOf       func(jwt string) (time.Time, bool)

	mu     sync.RWMutex
	jwt    string
	seed   string
	nodeID string
}

// NewNATSCredentialManager builds a manager over register. When requireCreds is
// true, Acquire blocks until the node is approved and credentials are minted.
func NewNATSCredentialManager(register RegisterFunc, requireCreds bool) *NATSCredentialManager {
	return &NATSCredentialManager{
		register:       register,
		requireCreds:   requireCreds,
		initialBackoff: 2 * time.Second,
		maxBackoff:     30 * time.Second,
		maxAttempts:    defaultMaxAttempts,
		refreshLead:    0.75,
		refreshRetry:   30 * time.Second,
		expiryOf:       jwtExpiry,
	}
}

// jwtExpiry decodes the expiry of a minted user JWT. ok is false when the token
// is empty/undecodable or carries no expiry (e.g. a non-expiring service JWT).
func jwtExpiry(token string) (time.Time, bool) {
	if token == "" {
		return time.Time{}, false
	}
	uc, err := natsauth.DecodeUserClaims(token)
	if err != nil || uc.Expires == 0 {
		return time.Time{}, false
	}
	return time.Unix(uc.Expires, 0), true
}

func (m *NATSCredentialManager) store(res *RegisterResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodeID = res.ID
	if res.NatsJWT != "" && res.NatsUserSeed != "" {
		m.jwt, m.seed = res.NatsJWT, res.NatsUserSeed
	}
}

// Current returns the latest NATS credentials (both empty until acquired).
func (m *NATSCredentialManager) Current() (jwt, seed string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jwt, m.seed
}

// NodeID returns the node ID from the most recent registration.
func (m *NATSCredentialManager) NodeID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nodeID
}

// Provider returns a callback compatible with messaging.WithUserJWTProvider,
// supplying the current credentials on each (re)connect.
func (m *NATSCredentialManager) Provider() func() (string, string) {
	return m.Current
}

// HasCredentials reports whether complete NATS credentials have been obtained.
func (m *NATSCredentialManager) HasCredentials() bool {
	jwt, seed := m.Current()
	return jwt != "" && seed != ""
}

// Acquire registers and, when requireCreds is set, keeps re-registering with
// exponential backoff until the node is approved (status != pending) and
// credentials are minted. Without requireCreds it returns the first successful
// response (the historical one-shot behavior, preserved for anonymous NATS).
func (m *NATSCredentialManager) Acquire(ctx context.Context) (*RegisterResponse, error) {
	backoff := m.initialBackoff
	var lastReason error
	for attempt := 1; m.maxAttempts <= 0 || attempt <= m.maxAttempts; attempt++ {
		res, err := m.register(ctx)
		switch {
		case err != nil:
			lastReason = err
			xlog.Warn("Registration failed, retrying", "attempt", attempt, "next_retry", backoff, "error", err)
		case !m.requireCreds:
			m.store(res)
			return res, nil
		case res.Status == statusPending:
			lastReason = fmt.Errorf("node %s still pending admin approval", res.ID)
			xlog.Info("Node pending admin approval; waiting", "node", res.ID, "attempt", attempt, "next_retry", backoff)
		case res.NatsJWT == "" || res.NatsUserSeed == "":
			lastReason = fmt.Errorf("node %s approved but NATS credentials not minted", res.ID)
			xlog.Info("Node approved but NATS credentials not yet minted; waiting", "node", res.ID, "attempt", attempt, "next_retry", backoff)
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
	return nil, fmt.Errorf("giving up acquiring NATS credentials after %d attempts: %w", m.maxAttempts, lastReason)
}

// RefreshLoop re-registers to mint a fresh JWT before the current one expires,
// updating the credentials returned by Current/Provider so the NATS connection
// adopts them on its next reconnect. It returns nil when ctx is cancelled or
// when the current credential has no expiry (nothing to refresh), and a non-nil
// error after maxAttempts consecutive refresh failures — letting the caller
// exit the worker so it restarts and re-acquires (or surfaces the outage)
// rather than silently drifting toward an expired, unrenewable JWT.
func (m *NATSCredentialManager) RefreshLoop(ctx context.Context) error {
	failures := 0
	for {
		jwt, _ := m.Current()
		exp, ok := m.expiryOf(jwt)
		if !ok {
			xlog.Debug("NATS credential has no expiry; refresh loop exiting")
			return nil
		}
		wait := max(time.Duration(float64(time.Until(exp))*m.refreshLead), 0)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}

		res, err := m.register(ctx)
		if err == nil && res.NatsJWT != "" && res.NatsUserSeed != "" {
			m.store(res)
			failures = 0
			xlog.Info("Refreshed NATS credentials", "node", res.ID)
			continue
		}
		failures++
		if err != nil {
			xlog.Warn("NATS credential refresh failed; will retry", "attempt", failures, "error", err)
		} else {
			xlog.Warn("NATS credential refresh returned no credentials; will retry", "attempt", failures)
		}
		if m.maxAttempts > 0 && failures >= m.maxAttempts {
			return fmt.Errorf("NATS credential refresh failed %d times in a row", failures)
		}
		// Back off before retrying so a persistent failure near expiry does not spin.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(m.refreshRetry):
		}
	}
}
