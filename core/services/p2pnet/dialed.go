package p2pnet

import (
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// WatchDialedAddrs calls fn each time the host dials a peer and the
// connection works over a direct path, with the address that worked. The
// address has no /p2p/ part and is never a circuit address, so a caller can
// keep it as a dial address of the peer. The address comes from a dial of
// this host, whether a request, mDNS or another discovery started it.
//
// Connections that the peer opened are not reported, because their remote
// address is a port the peer picked for that connection. The same goes for
// a connection that appears while a relayed one to the peer is open, which
// is what a hole punch looks like: its address is a temporary NAT mapping.
//
// fn runs on the thread that opened the connection and must return
// quickly. The watch ends when the host closes.
func (n *Host) WatchDialedAddrs(fn func(p peer.ID, addr ma.Multiaddr)) {
	n.h.Network().Notify(&network.NotifyBundle{ConnectedF: func(nw network.Network, c network.Conn) {
		addr, ok := dialedAddr(c)
		if !ok {
			return
		}
		for _, o := range nw.ConnsToPeer(c.RemotePeer()) {
			if o != c && (o.Stat().Limited || isRelayed(o.RemoteMultiaddr())) {
				return
			}
		}
		fn(c.RemotePeer(), addr)
	}})
}

// dialedAddr returns the address that c was dialed on, when c is an
// outbound connection over a direct path.
func dialedAddr(c network.Conn) (ma.Multiaddr, bool) {
	if c.Stat().Direction != network.DirOutbound || c.Stat().Limited {
		return nil, false
	}
	a := c.RemoteMultiaddr()
	if a == nil || isRelayed(a) {
		return nil, false
	}
	if rest, last := ma.SplitLast(a); last != nil && last.Protocol().Code == ma.P_P2P {
		a = rest
	}
	if len(a) == 0 {
		return nil, false
	}
	return a, true
}
