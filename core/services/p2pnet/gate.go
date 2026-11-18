package p2pnet

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
)

// Gate decides which peers may connect to the host and which streams they
// may open.
//
// AdmitConn runs after the security handshake of an inbound connection, when
// the remote peer ID is authenticated and before any stream exists. A refused
// peer is disconnected. Outbound connections (to relays, bootstrap and DHT
// peers) are not gated.
//
// AdmitStream runs for every stream that reaches a protocol registered with
// Host.Listen, on inbound and outbound connections alike, before the
// listener sees the stream. A refused stream is reset.
type Gate interface {
	AdmitConn(p peer.ID) bool
	AdmitStream(p peer.ID, proto protocol.ID) bool
}

// Allowlist is a Gate that admits the paired peers. While pairing is open it
// also admits unknown peers at connection level, but lets them open streams
// only on the pairing protocols (by default ProtoHTTP, where the HTTP server
// must serve the redeem route and nothing else to such a peer).
//
// An Allowlist is safe for concurrent use.
type Allowlist struct {
	mu            sync.RWMutex
	peers         map[peer.ID]struct{}
	pairing       bool
	pairingProtos map[protocol.ID]struct{}
}

// NewAllowlist returns an Allowlist that admits the given peers. Pairing is
// closed.
func NewAllowlist(peers ...peer.ID) *Allowlist {
	a := &Allowlist{
		peers:         make(map[peer.ID]struct{}, len(peers)),
		pairingProtos: map[protocol.ID]struct{}{ProtoHTTP: {}},
	}
	for _, p := range peers {
		a.peers[p] = struct{}{}
	}
	return a
}

// Allow admits p.
func (a *Allowlist) Allow(p peer.ID) {
	a.mu.Lock()
	a.peers[p] = struct{}{}
	a.mu.Unlock()
}

// Disallow stops admitting p. Connections that are already open stay open;
// Host.Revoke also closes them.
func (a *Allowlist) Disallow(p peer.ID) {
	a.mu.Lock()
	delete(a.peers, p)
	a.mu.Unlock()
}

// Allowed reports whether p is a paired peer.
func (a *Allowlist) Allowed(p peer.ID) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.peers[p]
	return ok
}

// SetPairing opens or closes pairing. Open it while invites are pending.
func (a *Allowlist) SetPairing(open bool) {
	a.mu.Lock()
	a.pairing = open
	a.mu.Unlock()
}

// AllowPairingProtocol adds proto to the protocols that an unpaired peer may
// use while pairing is open.
func (a *Allowlist) AllowPairingProtocol(proto protocol.ID) {
	a.mu.Lock()
	a.pairingProtos[proto] = struct{}{}
	a.mu.Unlock()
}

// AdmitConn admits paired peers, and every peer while pairing is open.
func (a *Allowlist) AdmitConn(p peer.ID) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.peers[p]
	return ok || a.pairing
}

// AdmitStream admits every stream of a paired peer. An unpaired peer gets a
// stream only on a pairing protocol and only while pairing is open.
func (a *Allowlist) AdmitStream(p peer.ID, proto protocol.ID) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if _, ok := a.peers[p]; ok {
		return true
	}
	_, pairingProto := a.pairingProtos[proto]
	return a.pairing && pairingProto
}

// connGater applies a Gate as the libp2p connection gater.
type connGater struct{ gate Gate }

func (connGater) InterceptPeerDial(peer.ID) bool               { return true }
func (connGater) InterceptAddrDial(peer.ID, ma.Multiaddr) bool { return true }
func (connGater) InterceptAccept(network.ConnMultiaddrs) bool  { return true }

func (g connGater) InterceptSecured(d network.Direction, p peer.ID, _ network.ConnMultiaddrs) bool {
	if d == network.DirOutbound {
		return true
	}
	if !g.gate.AdmitConn(p) {
		xlog.Debug("p2p: refused inbound connection from an unknown peer", "peer", p.String())
		return false
	}
	return true
}

func (connGater) InterceptUpgraded(network.Conn) (bool, control.DisconnectReason) {
	return true, 0
}
