package p2pnet

import (
	"context"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/libp2p/go-libp2p/p2p/protocol/holepunch"
	ma "github.com/multiformats/go-multiaddr"
)

// PathKind is how the host reaches a peer.
type PathKind string

const (
	// PathNone means that the host has no connection to the peer.
	PathNone PathKind = "none"
	// PathDirect is a direct connection.
	PathDirect PathKind = "direct"
	// PathHolePunched is a direct connection that a hole punch opened.
	PathHolePunched PathKind = "hole_punched"
	// PathRelayed is a connection through a relay.
	PathRelayed PathKind = "relayed"
)

// Path describes the best connection to a peer.
type Path struct {
	Kind PathKind
	// Relay is the relay of a relayed path.
	Relay peer.ID
	// Limited reports that the relay limits the connection. LimitBytes and
	// LimitDuration are the limits the relay reported; zero means that the
	// relay set no limit of that kind.
	Limited       bool
	LimitBytes    int64
	LimitDuration time.Duration
	// RTT is the smoothed round-trip time to the peer, zero when unknown.
	RTT time.Duration
	// Since is when the connection opened.
	Since time.Time
}

// PathEvent reports the path to Peer after a connection to it opened or
// closed.
type PathEvent struct {
	Peer peer.ID
	Path Path
}

// Path returns the best connection to p: a direct one before a relayed one,
// and an unlimited one before a limited one.
func (n *Host) Path(p peer.ID) Path {
	var best network.Conn
	for _, c := range n.h.Network().ConnsToPeer(p) {
		if best == nil || connRank(c) < connRank(best) {
			best = c
		}
	}
	if best == nil {
		return Path{Kind: PathNone}
	}
	return n.pathOf(best)
}

func connRank(c network.Conn) int {
	switch {
	case c.Stat().Limited:
		return 2
	case isRelayed(c.RemoteMultiaddr()):
		return 1
	}
	return 0
}

func isRelayed(a ma.Multiaddr) bool {
	_, err := a.ValueForProtocol(ma.P_CIRCUIT)
	return err == nil
}

// relayOf returns the relay's peer ID in a /.../p2p/<relay>/p2p-circuit
// address.
func relayOf(a ma.Multiaddr) peer.ID {
	var relay peer.ID
	for _, c := range a {
		switch c.Code() {
		case ma.P_P2P:
			if id, err := peer.Decode(c.Value()); err == nil {
				relay = id
			}
		case ma.P_CIRCUIT:
			return relay
		}
	}
	return relay
}

func (n *Host) pathOf(c network.Conn) Path {
	st := c.Stat()
	path := Path{Kind: PathDirect, Since: st.Opened, RTT: n.h.Peerstore().LatencyEWMA(c.RemotePeer())}
	switch {
	case isRelayed(c.RemoteMultiaddr()):
		path.Kind = PathRelayed
		path.Relay = relayOf(c.RemoteMultiaddr())
	case n.punch.punched(c.RemotePeer()):
		path.Kind = PathHolePunched
	}
	if st.Limited {
		path.Limited = true
		if d, ok := st.Extra[client.StatLimitDuration].(time.Duration); ok {
			path.LimitDuration = d
		}
		switch v := st.Extra[client.StatLimitData].(type) {
		case uint64:
			path.LimitBytes = int64(min(v, uint64(1<<63-1)))
		case int64:
			path.LimitBytes = v
		}
	}
	return path
}

// WatchPaths sends a PathEvent each time a connection opens or closes,
// until ctx ends or the host closes; then it closes the channel. The
// channel is buffered and drops events that the reader does not take in
// time, so a reader that falls behind calls Path for the current state.
func (n *Host) WatchPaths(ctx context.Context) <-chan PathEvent {
	ch := make(chan PathEvent, 32)
	var mu sync.Mutex
	done := false
	send := func(p peer.ID) {
		ev := PathEvent{Peer: p, Path: n.Path(p)}
		mu.Lock()
		defer mu.Unlock()
		if done {
			return
		}
		select {
		case ch <- ev:
		default:
		}
	}
	nb := &network.NotifyBundle{
		ConnectedF:    func(_ network.Network, c network.Conn) { send(c.RemotePeer()) },
		DisconnectedF: func(_ network.Network, c network.Conn) { send(c.RemotePeer()) },
	}
	n.h.Network().Notify(nb)
	n.wg.Go(func() {
		select {
		case <-ctx.Done():
		case <-n.ctx.Done():
		}
		n.h.Network().StopNotify(nb)
		mu.Lock()
		done = true
		close(ch)
		mu.Unlock()
	})
	return ch
}

// punchTracer remembers the peers that a hole punch reached, so that Path
// can tell a punched connection from a plain direct one.
type punchTracer struct {
	mu sync.Mutex
	ok map[peer.ID]bool
}

func newPunchTracer() *punchTracer { return &punchTracer{ok: map[peer.ID]bool{}} }

func (t *punchTracer) Trace(e *holepunch.Event) {
	if ev, ok := e.Evt.(*holepunch.EndHolePunchEvt); ok && ev.Success {
		t.mu.Lock()
		t.ok[e.Remote] = true
		t.mu.Unlock()
	}
}

func (t *punchTracer) punched(p peer.ID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ok[p]
}

func (t *punchTracer) forget(p peer.ID) {
	t.mu.Lock()
	delete(t.ok, p)
	t.mu.Unlock()
}
