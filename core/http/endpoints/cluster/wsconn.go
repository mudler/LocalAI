// SPDX-License-Identifier: MIT

package cluster

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebsocketConn adapts a gorilla WebSocket into the net.Conn that a yamux
// session drives.
//
// The two disagree about framing: WebSocket delivers whole messages, yamux
// wants an undelimited byte stream. The adapter therefore keeps the reader of
// the message it is part-way through between calls, so a Read whose buffer is
// smaller than the message hands back a prefix now and the rest next time
// instead of dropping the tail. That case is not hypothetical: yamux reads
// through a 4 KiB bufio.Reader while a single stream write can put a much
// larger data frame on the wire in one Write, so any message above the buffer
// size is read in pieces.
//
// The returned conn is safe for one reader and one writer concurrently, which
// is all yamux uses: its recvLoop reads and its sendLoop writes. It is not a
// general-purpose net.Conn.
func WebsocketConn(ws *websocket.Conn) net.Conn {
	return &wsConn{ws: ws}
}

type wsConn struct {
	ws *websocket.Conn

	// readMu guards frame, which carries a partially consumed message across
	// Read calls. gorilla allows a single concurrent reader, and this keeps
	// the adapter to that contract even if a caller reads from two goroutines.
	readMu sync.Mutex
	frame  io.Reader

	// writeMu keeps to gorilla's one-concurrent-writer contract.
	writeMu sync.Mutex
}

func (c *wsConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	c.readMu.Lock()
	defer c.readMu.Unlock()

	for {
		if c.frame == nil {
			messageType, r, err := c.ws.NextReader()
			if err != nil {
				return 0, translateReadErr(err)
			}
			// Binary is the only type this link speaks. Skipping an unexpected
			// text message would silently desynchronise the yamux framing, so
			// it is reported instead.
			if messageType != websocket.BinaryMessage {
				return 0, fmt.Errorf("cluster: peer link received websocket message type %d, want binary", messageType)
			}
			c.frame = r
		}

		n, err := c.frame.Read(p)
		if err == io.EOF {
			// End of one message, not end of the stream: drop the reader so
			// the next call pulls the next message. Passing io.EOF up would
			// end the yamux session at an arbitrary message boundary.
			c.frame = nil
			err = nil
		}
		if n > 0 || err != nil {
			return n, err
		}
		// A zero-length message yields nothing to return, and (0, nil) reads
		// look like a stalled stream to some callers, so wait for the next one.
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close drops the underlying network connection without negotiating a
// WebSocket close handshake. yamux has already sent its own go-away by this
// point, and a close frame would need the write lock that a blocked sendLoop
// may still hold.
func (c *wsConn) Close() error {
	return c.ws.Close()
}

func (c *wsConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.ws.SetReadDeadline(t); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(t)
}

func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }

// translateReadErr maps a peer hanging up cleanly onto io.EOF, which is how a
// yamux session recognises a normal ending. Any other close code, and any
// transport error, is passed through so the session reports a real failure.
func translateReadErr(err error) error {
	if websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived,
	) {
		return io.EOF
	}
	return err
}
