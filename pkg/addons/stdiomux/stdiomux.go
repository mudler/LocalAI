// SPDX-License-Identifier: MIT

// Package stdiomux carries the channels between the owner and an add-on over
// one pair of byte streams: the standard input and output of a process that
// runs in WSL 2, where no handle of the owner exists (see
// contract.ChannelStdio). It is one yamux session; the owner is the client
// and the add-on the server, and both sides open streams.
package stdiomux

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	yamux "github.com/libp2p/go-yamux/v5"
)

// config is the session configuration of both sides. Keep-alive finds a peer
// that stopped answering. The library logs nothing: the streams of the
// add-on are its log, and a session error ends the add-on anyway.
func config() *yamux.Config {
	c := yamux.DefaultConfig()
	c.LogOutput = io.Discard
	c.KeepAliveInterval = 10 * time.Second
	return c
}

// Client starts the owner's side of the session on conn.
func Client(conn net.Conn) (*yamux.Session, error) { return yamux.Client(conn, config(), nil) }

// Server starts the add-on's side of the session on conn.
func Server(conn net.Conn) (*yamux.Session, error) { return yamux.Server(conn, config(), nil) }

// Conn joins a reader and a writer into a net.Conn for the session. A
// deadline is passed to a stream that takes one; a pipe that takes none (a
// Windows pipe, or a blocking descriptor) ignores it, and the session then
// relies on its keep-alive and on the end of the process.
func Conn(r io.ReadCloser, w io.WriteCloser) net.Conn {
	return &conn{r: r, w: w}
}

type conn struct {
	r    io.ReadCloser
	w    io.WriteCloser
	once sync.Once
	err  error
}

func (c *conn) Read(b []byte) (int, error)  { return c.r.Read(b) }
func (c *conn) Write(b []byte) (int, error) { return c.w.Write(b) }
func (c *conn) Close() error {
	c.once.Do(func() { c.err = errors.Join(c.w.Close(), c.r.Close()) })
	return c.err
}
func (c *conn) LocalAddr() net.Addr  { return addr{} }
func (c *conn) RemoteAddr() net.Addr { return addr{} }
func (c *conn) SetDeadline(t time.Time) error {
	return errors.Join(c.SetReadDeadline(t), c.SetWriteDeadline(t))
}
func (c *conn) SetReadDeadline(t time.Time) error {
	return deadline(c.r, func(d interface{ SetReadDeadline(time.Time) error }) error { return d.SetReadDeadline(t) })
}
func (c *conn) SetWriteDeadline(t time.Time) error {
	return deadline(c.w, func(d interface{ SetWriteDeadline(time.Time) error }) error { return d.SetWriteDeadline(t) })
}

func deadline[T any](v any, set func(T) error) error {
	d, ok := v.(T)
	if !ok {
		return nil
	}
	if err := set(d); err != nil && !errors.Is(err, os.ErrNoDeadline) {
		return err
	}
	return nil
}

// addr is the address of both ends of the session: there is no network.
type addr struct{}

func (addr) Network() string { return "stdio" }
func (addr) String() string  { return "stdio" }

// Splice copies a to b and b to a until both directions end, then closes
// both. Each direction closes the write side of its destination when it
// can, so a half-closed connection (an HTTP client that closed its write side)
// still gets its answer.
func Splice(a, b net.Conn) {
	var wg sync.WaitGroup
	copyHalf := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	wg.Add(2)
	go copyHalf(a, b)
	go copyHalf(b, a)
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

// Relay accepts connections on l and splices each one to a stream that open
// returns, until l is closed. A connection for which open fails is closed.
func Relay(l net.Listener, open func() (net.Conn, error)) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			s, err := open()
			if err != nil {
				_ = c.Close()
				return
			}
			Splice(c, s)
		}()
	}
}
