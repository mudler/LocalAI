package p2pnet

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	"github.com/libp2p/go-libp2p/p2p/host/eventbus"
)

// autoRelayOptions replaces two AutoRelay defaults that do not fit a host
// with its own relays:
//
//   - The backoff after a failed reservation and the minimum interval
//     between candidate searches are one hour by default, so a host whose
//     relay restarts stays unreachable for an hour. Both become RelayRetry.
//   - With static relays, the relay finder waits for as many candidates as
//     there are static relays, which it gets at once, so it never waits for
//     the three-minute boot delay. With one static relay it keeps one
//     reservation; the public candidates from the DHT are a fallback.
func autoRelayOptions(cfg Config) []autorelay.Option {
	retry := cfg.RelayRetry
	if retry <= 0 {
		retry = DefaultRelayRetry
	}
	opts := []autorelay.Option{
		autorelay.WithBackoff(retry),
		autorelay.WithMinInterval(retry),
	}
	if n := len(cfg.StaticRelays); n > 0 {
		opts = append(opts, autorelay.WithMinCandidates(n), autorelay.WithNumRelays(n))
	}
	return opts
}

// relayCandidates is the AutoRelay peer source: the static relays first,
// then the peers of the DHT routing table. AutoRelay keeps only the peers
// that speak the relay protocol.
func (n *Host) relayCandidates(ctx context.Context, num int) <-chan peer.AddrInfo {
	ch := make(chan peer.AddrInfo, num+len(n.static))
	go func() {
		defer close(ch)
		sent := 0
		for _, ai := range n.static {
			ch <- ai
			sent++
		}
		d := n.dht.Load()
		if d == nil {
			return
		}
		// The DHT's host, not n.h: AutoRelay may call this before New
		// has stored n.h.
		ps := d.Host().Peerstore()
		for _, p := range d.RoutingTable().ListPeers() {
			if sent >= num {
				return
			}
			addrs := ps.Addrs(p)
			if len(addrs) == 0 {
				continue
			}
			select {
			case ch <- peer.AddrInfo{ID: p, Addrs: addrs}:
				sent++
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// kickAutoRelay emits one "reachability unknown" event. AutoRelay starts
// its relay finder only on a reachability event, and AutoNAT sends one only
// after it has found AutoNAT servers. On a private network, or with the
// public DHT off, there are none, and the static relays would never be used.
// AutoNAT can still report "public" later, which stops the finder.
func kickAutoRelay(h host.Host) error {
	em, err := h.EventBus().Emitter(new(event.EvtLocalReachabilityChanged), eventbus.Stateful)
	if err != nil {
		return fmt.Errorf("p2pnet: start relay finder: %w", err)
	}
	defer func() { _ = em.Close() }()
	if err := em.Emit(event.EvtLocalReachabilityChanged{Reachability: network.ReachabilityUnknown}); err != nil {
		return fmt.Errorf("p2pnet: start relay finder: %w", err)
	}
	return nil
}
