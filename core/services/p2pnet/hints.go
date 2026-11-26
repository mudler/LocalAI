package p2pnet

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// hintRelays remembers the hint relays that dials named (see WithHints). A
// hint relay is a public relay: it may carry a connection only for identify
// and a hole punch, never application data. The set is host-wide because
// the peerstore is: identify stores the peer's circuit addresses through its
// public relays, so a later dial with no hints could reach the peer through
// one of them.
type hintRelays struct {
	mu     sync.Mutex
	relays map[peer.ID]struct{}
	// punching counts, for each peer, the dials that wait for a hole punch
	// through a hint. Only those may open a connection through a hint.
	punching map[peer.ID]int
}

func newHintRelays() *hintRelays {
	return &hintRelays{relays: map[peer.ID]struct{}{}, punching: map[peer.ID]int{}}
}

func (h *hintRelays) add(ids ...peer.ID) {
	h.mu.Lock()
	for _, id := range ids {
		h.relays[id] = struct{}{}
	}
	h.mu.Unlock()
}

// remove forgets ids. A relay that a dial names as a carrier (WithRelays)
// may carry data, even when an older link named it as a hint.
func (h *hintRelays) remove(ids ...peer.ID) {
	h.mu.Lock()
	for _, id := range ids {
		delete(h.relays, id)
	}
	h.mu.Unlock()
}

func (h *hintRelays) has(id peer.ID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.relays[id]
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

// allowDial reports whether the host may dial a, an address of p. A circuit
// address through a hint relay is dialled only while a dial to p waits for
// a hole punch.
func (h *hintRelays) allowDial(p peer.ID, a ma.Multiaddr) bool {
	if !isRelayed(a) {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.relays[relayOf(a)]; !ok {
		return true
	}
	return h.punching[p] > 0
}

// hintRelayOf returns the hint relay of c, or "" when c does not go through
// a hint relay.
func (n *Host) hintRelayOf(c network.Conn) peer.ID {
	a := c.RemoteMultiaddr()
	if !isRelayed(a) {
		return ""
	}
	if r := relayOf(a); n.hints.has(r) {
		return r
	}
	return ""
}

// hintConns returns the open connections to p that go through hint relays,
// and whether p has any other open connection.
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

// closeHintConns closes the connections to p through hint relays, which
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
