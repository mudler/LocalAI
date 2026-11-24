package p2prelay

import (
	"fmt"
	"io"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
)

// Admit reports whether a peer may use an in-process relay, for example
// because it is paired with the server.
type Admit func(peer.ID) bool

// NewInProcess runs a relay on a LocalAI server's own host ("Via your
// server"). A peer may reserve if admit accepts it, and a circuit is allowed
// only if admit accepts both ends. There is no admin protocol: the server's
// pairing is the allowlist. The server's own connection gate still decides
// who may connect at all. Close the returned value to stop the relay.
func NewInProcess(h host.Host, admit Admit, lim Limits) (io.Closer, error) {
	if admit == nil {
		return nil, fmt.Errorf("p2prelay: admit is required")
	}
	if err := lim.validate(); err != nil {
		return nil, err
	}
	tr := &tracer{}
	acl := &inProcessACL{admit: admit, maxCircuits: int64(lim.MaxCircuits), circuits: tr.circuits.Load}
	rel, err := relayv2.New(RateLimitedHost(h, lim.RateLimit, lim.TotalRateLimit),
		relayv2.WithResources(lim.resources()),
		relayv2.WithACL(acl),
		relayv2.WithMetricsTracer(tr))
	if err != nil {
		return nil, fmt.Errorf("p2prelay: start in-process relay: %w", err)
	}
	return rel, nil
}

type inProcessACL struct {
	admit       Admit
	maxCircuits int64
	circuits    func() int64
}

func (a *inProcessACL) AllowReserve(p peer.ID, _ ma.Multiaddr) bool { return a.admit(p) }

func (a *inProcessACL) AllowConnect(src peer.ID, _ ma.Multiaddr, dest peer.ID) bool {
	if a.maxCircuits > 0 && a.circuits() >= a.maxCircuits {
		return false
	}
	return a.admit(src) && a.admit(dest)
}
