package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
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

// ReadRelayRequest reads and parses the opening frame of a peer stream, as the
// relay does when it accepts one.
func ReadRelayRequest(r io.Reader) (nodeID, lane string, budget time.Duration, err error) {
	payload, err := readFrame(r)
	if err != nil {
		return "", "", 0, fmt.Errorf("reading a relay request: %w", err)
	}
	return parseRelayRequest(payload)
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

// RequestThrough sends a request over streams that open returns, as
// PeerPool.Request does over the peer link.
func RequestThrough(ctx context.Context, open func(context.Context, string) (net.Conn, error), target, subject string, data []byte, maxBytes int, openWithin time.Duration) ([]byte, error) {
	return request(ctx, open, target, subject, data, maxBytes, openWithin)
}

// ReadBody exposes the read of a request or reply body to the specs.
func ReadBody(r io.Reader, maxBytes int) ([]byte, error) { return readBody(r, maxBytes) }

// SetLimits changes how many requests the replica serves at once, for each
// peer and in all.
func (r *Replies) SetLimits(perPeer, total int) {
	r.slotsMu.Lock()
	defer r.slotsMu.Unlock()
	r.perPeer, r.total = perPeer, total
}
