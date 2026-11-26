package p2pnet

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// hintRelays keeps, for each peer that a dial named hints for (see
// WithHints), the relays that may carry its data: the carriers that dials to
// that peer named with WithRelays. Any other relay of such a peer is treated
// as a public relay: it may carry a connection only for identify and a hole
// punch, never application data. That covers the hints themselves and every
// other public relay of the peer that identify or a DHT lookup stored in the
// peerstore, which is host-wide. The sets are per peer, so that a relay that
// one peer's invite names as a carrier carries nothing for another peer that
// uses it as a hint. A peer that no dial named hints for keeps the plain
// libp2p behaviour.
type hintRelays struct {
	mu sync.Mutex
	// guarded holds the peers that a dial named hints for.
	guarded map[peer.ID]struct{}
	// carriers holds, for each peer, the relays that dials to it named with
	// WithRelays.
	carriers map[peer.ID]map[peer.ID]struct{}
	// punching counts, for each peer, the dials that wait for a hole punch
	// through a hint. Only those may open a connection through a relay that
	// does not carry the peer's data.
	punching map[peer.ID]int
}

func newHintRelays() *hintRelays {
	return &hintRelays{guarded: map[peer.ID]struct{}{}, carriers: map[peer.ID]map[peer.ID]struct{}{}, punching: map[peer.ID]int{}}
}

// note records a dial to p that named carriers with WithRelays and hints
// with WithHints. A relay stays a carrier of p once a dial to p named it as
// one, also when a later dial names it as a hint: the user allowed it.
func (h *hintRelays) note(p peer.ID, carriers []peer.ID, hints bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if hints {
		h.guarded[p] = struct{}{}
	}
	if len(carriers) == 0 {
		return
	}
	set := h.carriers[p]
	if set == nil {
		set = map[peer.ID]struct{}{}
		h.carriers[p] = set
	}
	for _, id := range carriers {
		set[id] = struct{}{}
	}
}

// carries reports whether relay may carry application data to p.
func (h *hintRelays) carries(p, relay peer.ID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.carriesLocked(p, relay)
}

func (h *hintRelays) carriesLocked(p, relay peer.ID) bool {
	if _, ok := h.guarded[p]; !ok {
		return true
	}
	_, ok := h.carriers[p][relay]
	return ok
}

// begin lets dials to p go through hint relays until the returned function
// runs. That function reports how many dials to p still wait.
func (h *hintRelays) begin(p peer.ID) func() int {
	h.mu.Lock()
	h.punching[p]++
	h.mu.Unlock()
	var once sync.Once
	left := 0
	return func() int {
		once.Do(func() {
			h.mu.Lock()
			h.punching[p]--
			left = h.punching[p]
			if left <= 0 {
				delete(h.punching, p)
			}
			h.mu.Unlock()
		})
		return left
	}
}

// punchingFor reports whether a dial to p waits for a hole punch.
func (h *hintRelays) punchingFor(p peer.ID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.punching[p] > 0
}

// allowDial reports whether the host may dial a, an address of p. A circuit
// address through a relay that does not carry p's data is dialled only while
// a dial to p waits for a hole punch.
func (h *hintRelays) allowDial(p peer.ID, a ma.Multiaddr) bool {
	if !isRelayed(a) {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.carriesLocked(p, relayOf(a)) || h.punching[p] > 0
}

// hintRelayOf returns the relay of c when c goes through a relay that does
// not carry the data of its peer (a hint or another public relay of the
// peer), or "" when c may carry data.
func (n *Host) hintRelayOf(c network.Conn) peer.ID {
	a := c.RemoteMultiaddr()
	if !isRelayed(a) {
		return ""
	}
	if r := relayOf(a); !n.hints.carries(c.RemotePeer(), r) {
		return r
	}
	return ""
}

// hintConns returns the open connections to p that go through relays that
// do not carry p's data, and whether p has any other open connection.
func (n *Host) hintConns(p peer.ID) (hints []network.Conn, other bool) {
	for _, c := range n.h.Network().ConnsToPeer(p) {
		if c.IsClosed() {
			continue
		}
		if n.hintRelayOf(c) != "" {
			hints = append(hints, c)
		} else {
			other = true
		}
	}
	return hints, other
}

// closeHintConns closes the connections to p through relays that do not
// carry p's data, which
// frees their slot on the public relay. It returns the relay of one of them.
func (n *Host) closeHintConns(p peer.ID) peer.ID {
	hints, _ := n.hintConns(p)
	var relay peer.ID
	for _, c := range hints {
		relay = n.hintRelayOf(c)
		_ = c.Close()
	}
	return relay
}
