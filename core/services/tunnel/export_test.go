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
