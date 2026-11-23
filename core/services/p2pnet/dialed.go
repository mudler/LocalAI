package p2pnet

import (
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
)

// WatchDialedAddrs calls fn with the direct addresses that a connection to a
// peer proves, so a caller can keep them as dial addresses of the peer. It
// calls fn in two cases:
//
//   - The host dialed the peer and the connection works over a direct path:
//     the address that was dialed. This is what a request, mDNS or another
//     discovery started.
//   - The identify exchange of a connection ended: the listen addresses that
//     the peer announced, only those on an IP that a live direct connection
//     to the peer comes from. This covers a peer that dialed this host, whose
//     connection has a port that the peer picked for it.
//
// An address has no /p2p/ part and is never a circuit address, loopback or
// link-local. Nothing is reported while a relayed connection to the peer is
// open, which is what a hole punch looks like: the address of a punched
// connection is a temporary NAT mapping.
//
// fn must return quickly: for a dial it runs on the thread that opened the
// connection. The watch ends when the host closes.
func (n *Host) WatchDialedAddrs(fn func(p peer.ID, addrs []ma.Multiaddr)) {
	n.h.Network().Notify(&network.NotifyBundle{ConnectedF: func(nw network.Network, c network.Conn) {
		addr, ok := dialedAddr(c)
		if !ok || relayedTo(nw, c.RemotePeer(), c) {
			return
		}
		fn(c.RemotePeer(), []ma.Multiaddr{addr})
	}})
	sub, err := n.h.EventBus().Subscribe(new(event.EvtPeerIdentificationCompleted))
	if err != nil {
		xlog.Warn("p2p: cannot watch the addresses of peers", "error", err)
		return
	}
	n.wg.Go(func() {
		defer func() { _ = sub.Close() }()
		for {
			select {
			case <-n.ctx.Done():
				return
			case e, ok := <-sub.Out():
				if !ok {
					return
				}
				ev, ok := e.(event.EvtPeerIdentificationCompleted)
				if !ok {
					continue
				}
				if addrs := n.announcedDirectAddrs(ev.Peer); len(addrs) > 0 {
					fn(ev.Peer, addrs)
				}
			}
		}
	})
}

// relayedTo reports whether a connection other than skip to p is relayed.
func relayedTo(nw network.Network, p peer.ID, skip network.Conn) bool {
	for _, o := range nw.ConnsToPeer(p) {
		if o != skip && (o.Stat().Limited || isRelayed(o.RemoteMultiaddr())) {
			return true
		}
	}
	return false
}

// announcedDirectAddrs returns the direct listen addresses of p, as p
// announced them, that are on the IP of a live direct connection to p.
func (n *Host) announcedDirectAddrs(p peer.ID) []ma.Multiaddr {
	ips := map[string]bool{}
	for _, c := range n.h.Network().ConnsToPeer(p) {
		if c.Stat().Limited || isRelayed(c.RemoteMultiaddr()) {
			return nil
		}
		if ip := ipOf(c.RemoteMultiaddr()); ip != nil {
			ips[ip.String()] = true
		}
	}
	return directAddrsOnIPs(n.h.Peerstore().Addrs(p), ips)
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
