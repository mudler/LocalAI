package p2pnet

import (
	"container/list"
	"context"
	"net"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/net/swarm"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// Redial: when this host's own listen addresses change, dial the allowlisted
// peers again at the addresses they announced before, so that they learn the
// new address from the identify exchange. The addresses live in memory only,
// in a bounded LRU: the cache holds at most addrCachePeers peers (the least
// recently used peer is evicted first) and at most addrCachePerPeer addresses
// of each.
const (
	addrCachePeers   = 256
	addrCachePerPeer = 8
	// redialParallel caps the dials in flight; redialAttempts and the
	// backoff bound the work per peer after one trigger.
	redialParallel = 4
	redialAttempts = 6
	redialBackoff  = 2 * time.Second
	redialMaxDelay = 30 * time.Second
	redialTimeout  = 10 * time.Second
)

// allowed is the optional part of a Gate that names the paired peers.
type allowed interface{ Allowed(peer.ID) bool }

type addrEntry struct {
	peer  peer.ID
	addrs []ma.Multiaddr
}

// addrCache is a bounded LRU of the direct addresses of paired peers.
type addrCache struct {
	mu  sync.Mutex
	ll  *list.List
	idx map[peer.ID]*list.Element
}

func newAddrCache() *addrCache {
	return &addrCache{ll: list.New(), idx: map[peer.ID]*list.Element{}}
}

func (c *addrCache) put(p peer.ID, addrs []ma.Multiaddr) {
	if len(addrs) == 0 {
		return
	}
	if len(addrs) > addrCachePerPeer {
		addrs = addrs[:addrCachePerPeer]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.idx[p]; ok {
		e.Value.(*addrEntry).addrs = addrs
		c.ll.MoveToFront(e)
		return
	}
	c.idx[p] = c.ll.PushFront(&addrEntry{peer: p, addrs: addrs})
	for c.ll.Len() > addrCachePeers {
		old := c.ll.Back()
		c.ll.Remove(old)
		delete(c.idx, old.Value.(*addrEntry).peer)
	}
}

func (c *addrCache) forget(p peer.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.idx[p]; ok {
		c.ll.Remove(e)
		delete(c.idx, p)
	}
}

func (c *addrCache) snapshot() []addrEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]addrEntry, 0, c.ll.Len())
	for e := c.ll.Front(); e != nil; e = e.Next() {
		v := e.Value.(*addrEntry)
		out = append(out, addrEntry{peer: v.peer, addrs: append([]ma.Multiaddr(nil), v.addrs...)})
	}
	return out
}

type redialer struct {
	n     *Host
	gate  allowed
	cache *addrCache
	sem   chan struct{}
	// interfaceAddrs lists the addresses of this host's interfaces. It is a
	// field so that tests can make the lookup fail; the default is
	// net.InterfaceAddrs.
	interfaceAddrs func() ([]net.Addr, error)

	mu       sync.Mutex
	inflight map[peer.ID]bool
}

// startRedial runs the redialer when the gate can tell the paired peers. It
// learns the addresses of a peer after the identify exchange, and dials the
// paired peers again when w reports a change of the host's addresses.
func (n *Host) startRedial(w *addrWatcher) error {
	g, ok := n.gate.(allowed)
	if !ok {
		return nil
	}
	sub, err := n.h.EventBus().Subscribe(new(event.EvtPeerIdentificationCompleted))
	if err != nil {
		return err
	}
	r := &redialer{n: n, gate: g, cache: newAddrCache(), sem: make(chan struct{}, redialParallel), interfaceAddrs: net.InterfaceAddrs, inflight: map[peer.ID]bool{}}
	n.redial = r
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
				if ev, ok := e.(event.EvtPeerIdentificationCompleted); ok {
					r.learn(ev.Peer)
				}
			}
		}
	})
	w.subscribe(r.addressesChanged)
	return nil
}

// ipOf returns the IP of a, or nil.
func ipOf(a ma.Multiaddr) net.IP {
	ip, err := manet.ToIP(a)
	if err != nil {
		return nil
	}
	return ip
}

// directAddrsOnIPs returns the direct addresses of addrs whose IP is in ips:
// no relay, loopback or link-local address, and none on an IP that is not
// listed.
func directAddrsOnIPs(addrs []ma.Multiaddr, ips map[string]bool) []ma.Multiaddr {
	var keep []ma.Multiaddr
	for _, a := range addrs {
		if isRelayed(a) {
			continue
		}
		if ip := ipOf(a); ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && ips[ip.String()] {
			keep = append(keep, a)
		}
	}
	return keep
}

// learn keeps the direct addresses that p announced, only for a paired peer
// and only those on an IP that a live connection to p comes from, so a peer
// cannot make this host dial a third party.
func (r *redialer) learn(p peer.ID) {
	if !r.gate.Allowed(p) {
		return
	}
	seen := map[string]bool{}
	for _, c := range r.n.h.Network().ConnsToPeer(p) {
		if isRelayed(c.RemoteMultiaddr()) {
			continue
		}
		if ip := ipOf(c.RemoteMultiaddr()); ip != nil {
			seen[ip.String()] = true
		}
	}
	r.cache.put(p, directAddrsOnIPs(r.n.h.Peerstore().Addrs(p), seen))
}

// addressesChanged closes the connections that use an address this host no
// longer has (they are dead, and a dial would reuse them), then redials.
//
// When the interface addresses cannot be read, it keeps every connection: a
// failed lookup says nothing about which addresses are gone (on Android 11
// and later, apps cannot read them at all), so closing would drop live
// connections. The redial still runs.
func (r *redialer) addressesChanged() {
	for _, p := range r.n.h.Network().Peers() {
		r.learn(p)
	}
	ips, err := r.localIPs()
	if err != nil {
		xlog.Debug("p2p: cannot read the interface addresses; keeping the direct connections", "error", err)
		r.redialAll()
		return
	}
	for _, c := range r.n.h.Network().Conns() {
		if isRelayed(c.LocalMultiaddr()) || !r.gate.Allowed(c.RemotePeer()) {
			continue
		}
		if ip := ipOf(c.LocalMultiaddr()); ip != nil && !ips[ip.String()] {
			_ = c.Close()
		}
	}
	r.redialAll()
}

func (r *redialer) redialAll() {
	for _, e := range r.cache.snapshot() {
		r.redialPeer(e.peer)
	}
}

func (r *redialer) redialPeer(p peer.ID) {
	r.mu.Lock()
	if r.inflight[p] {
		r.mu.Unlock()
		return
	}
	r.inflight[p] = true
	r.mu.Unlock()
	r.n.wg.Go(func() {
		defer func() { r.mu.Lock(); delete(r.inflight, p); r.mu.Unlock() }()
		delay := redialBackoff
		for i := 0; i < redialAttempts; i++ {
			if !r.gate.Allowed(p) || r.n.ctx.Err() != nil {
				return
			}
			if r.n.h.Network().Connectedness(p) == network.Connected {
				return
			}
			if r.dial(p) {
				return
			}
			select {
			case <-r.n.ctx.Done():
				return
			case <-time.After(delay):
			}
			delay = min(delay*2, redialMaxDelay)
		}
	})
}

func (r *redialer) dial(p peer.ID) bool {
	var addrs []ma.Multiaddr
	for _, e := range r.cache.snapshot() {
		if e.peer == p {
			addrs = e.addrs
		}
	}
	if len(addrs) == 0 {
		return true // nothing to dial; the peer was forgotten
	}
	select {
	case r.sem <- struct{}{}:
	case <-r.n.ctx.Done():
		return true
	}
	defer func() { <-r.sem }()
	if sw, ok := r.n.h.Network().(*swarm.Swarm); ok {
		sw.Backoff().Clear(p)
	}
	ctx, cancel := context.WithTimeout(r.n.ctx, redialTimeout)
	defer cancel()
	err := r.n.h.Connect(ctx, peer.AddrInfo{ID: p, Addrs: addrs})
	if err != nil {
		xlog.Debug("p2p: redial of a paired peer failed", "peer", p.String(), "error", err)
	}
	if err == nil && !r.gate.Allowed(p) {
		// Revoked while the dial ran.
		r.cache.forget(p)
		_ = r.n.h.Network().ClosePeer(p)
	}
	return err == nil
}

// forget drops p from the cache; Revoke calls it.
func (r *redialer) forget(p peer.ID) { r.cache.forget(p) }

// Learn records the addresses of p as it announced them, when p is paired and
// connected. Call it after a peer was added to the gate, because the identify
// exchange of the connection that paired it ran before that.
func (n *Host) Learn(p peer.ID) {
	if n.redial != nil {
		n.redial.learn(p)
	}
}

// localIPs returns the unicast addresses of the interfaces of this machine.
// A failed lookup is an error and not an empty set, because the caller must
// not read "unknown" as "no addresses".
func (r *redialer) localIPs() (map[string]bool, error) {
	addrs, err := r.interfaceAddrs()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
			out[ipn.IP.String()] = true
		}
	}
	return out, nil
}

// Redial dials p at its cached addresses unless it is connected. It does
// nothing for a peer that is not paired or not cached.
func (n *Host) Redial(p peer.ID) {
	if n.redial != nil {
		n.redial.redialPeer(p)
	}
}
