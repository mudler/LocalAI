package tunnel

import (
	"time"

	"github.com/libp2p/go-yamux/v5"
)

// SessionConfigFor exposes the yamux configuration of a lane to the specs.
func SessionConfigFor(lane Lane) *yamux.Config { return sessionConfig(lane) }

// SetClaimTimeout changes the bound of a claim for one spec and returns a
// function that restores it.
func SetClaimTimeout(d time.Duration) (restore func()) {
	old := claimTimeout
	claimTimeout = d
	return func() { claimTimeout = old }
}

// NewRelayWithTimeouts builds a relay with timeouts that a spec can reach.
func NewRelayWithTimeouts(tunnels *Registry, header, open time.Duration) *Relay {
	return newRelay(tunnels, header, open)
}

// RouteFailure exposes the classification of a failed dial to the specs.
func RouteFailure(nodeID string, cause error) error { return routeFailure(nodeID, cause) }

// SetHandshakeTimeout changes the backstop of the handshake for one spec and
// returns a function that restores it.
func SetHandshakeTimeout(d time.Duration) (restore func()) {
	old := dialHandshakeTimeout
	dialHandshakeTimeout = d
	return func() { dialHandshakeTimeout = old }
}
