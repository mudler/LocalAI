package p2pnet

import (
	"io"
	"net"
	"sync"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Addr is the net.Addr of a peer: its network is "libp2p" and its string
// form is the peer ID.
type Addr struct{ Peer peer.ID }

// Network returns "libp2p".
func (Addr) Network() string { return "libp2p" }

// String returns the peer ID.
func (a Addr) String() string { return a.Peer.String() }

// Conn is a libp2p stream as a net.Conn. LocalAddr and RemoteAddr are Addr
// values, so an http.Request served over a Conn has the peer ID as its
// RemoteAddr (see DecodePeerID). Deadlines work; an expired deadline
// returns a net.Error whose Timeout method reports true. CloseWrite closes
// only the write side, and Reset aborts both sides.
type Conn struct{ network.Stream }

var _ net.Conn = (*Conn)(nil)

// LocalAddr returns the local peer.
func (c *Conn) LocalAddr() net.Addr { return Addr{Peer: c.Stream.Conn().LocalPeer()} }

// RemoteAddr returns the remote peer.
func (c *Conn) RemoteAddr() net.Addr { return Addr{Peer: c.Stream.Conn().RemotePeer()} }

// PeerOf returns the remote peer of a connection that Host.Dial or a
// Host.Listen listener returned.
func PeerOf(c net.Conn) (peer.ID, bool) {
	if pc, ok := c.(*Conn); ok {
		return pc.Stream.Conn().RemotePeer(), true
	}
	return "", false
}

// Pipe copies data both ways between a and b until both directions end, and
// then closes both. When one direction reaches EOF, Pipe closes only the
// write side of its destination, so that a client which sends its request
// and half-closes still gets the reply. When a copy fails, Pipe resets both
// connections (a TCP connection gets an RST), so the far side sees a failure
// and not a clean but short stream. It returns the first copy error.
//
// Both connections should support CloseWrite, as *Conn and *net.TCPConn do.
// Without it, Pipe cannot pass a half-close on.
func Pipe(a, b net.Conn) error {
	errc := make(chan error, 2)
	var once sync.Once
	abort := func() {
		once.Do(func() {
			resetConn(a)
			resetConn(b)
		})
	}
	cp := func(dst, src net.Conn) {
		_, err := io.Copy(dst, src)
		if err == nil {
			err = closeWrite(dst)
		}
		if err != nil {
			abort()
		}
		errc <- err
	}
	go cp(b, a)
	go cp(a, b)
	first := <-errc
	second := <-errc
	_ = a.Close()
	_ = b.Close()
	if first != nil {
		return first
	}
	return second
}

func closeWrite(c net.Conn) error {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

func resetConn(c net.Conn) {
	switch c := c.(type) {
	case interface{ Reset() error }:
		_ = c.Reset()
	case *net.TCPConn:
		_ = c.SetLinger(0)
		_ = c.Close()
	default:
		_ = c.Close()
	}
}
