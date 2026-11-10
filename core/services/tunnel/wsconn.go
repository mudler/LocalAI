package tunnel

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// websocketConn adapts a gorilla websocket to the net.Conn that a yamux session
// drives.
//
// The two disagree about framing. A websocket delivers whole messages and yamux
// wants a stream of bytes. The adapter keeps the reader of the message that it
// is part-way through, so a Read with a buffer smaller than the message returns
// the first part now and the rest on the next call. This case is normal: yamux
// reads through a small buffered reader, and one stream write can put a much
// larger data frame on the wire in one message.
//
// The conn is safe for one reader and one writer at the same time, and for a
// third goroutine that sets deadlines. It is not a general net.Conn.
func websocketConn(ws *websocket.Conn) net.Conn {
	return &wsConn{ws: ws}
}

type wsConn struct {
	ws *websocket.Conn

	// readMu guards frame, the partly read message. gorilla allows one reader,
	// and this keeps to that even if a caller reads from two goroutines.
	readMu sync.Mutex
	frame  io.Reader

	// writeMu keeps to the rule of gorilla that there is one writer.
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
			// The link speaks binary messages only. If an unexpected text
			// message were skipped, the yamux framing would go out of step
			// without a sign, so it is an error.
			if messageType != websocket.BinaryMessage {
				return 0, fmt.Errorf("tunnel: received websocket message type %d, want binary", messageType)
			}
			c.frame = r
		}

		n, err := c.frame.Read(p)
		if errors.Is(err, io.EOF) {
			// The end of one message is not the end of the stream. If io.EOF
			// went up, the session would end at a message boundary.
			c.frame = nil
			err = nil
		}
		if n > 0 || err != nil {
			return n, err
		}
		// A message of zero length gives nothing to return, and a Read that
		// returns (0, nil) looks like a stalled stream to some callers.
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

// Close drops the network connection without the websocket close handshake.
// yamux has sent its own go-away by then, and a close frame needs the write
// lock that a blocked send loop can still hold.
func (c *wsConn) Close() error {
	return c.ws.Close()
}

func (c *wsConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

// SetReadDeadline takes no lock and must not take readMu. gorilla passes the
// deadline to the network connection, which allows this call from another
// goroutine, and readMu is held by the parked Read that this call must wake.
func (c *wsConn) SetReadDeadline(t time.Time) error { return c.ws.SetReadDeadline(t) }

// SetWriteDeadline takes writeMu because gorilla keeps the write deadline in a
// plain field and applies it at the next flush. To set it during a write is a
// data race.
func (c *wsConn) SetWriteDeadline(t time.Time) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return c.ws.SetWriteDeadline(t)
}

// translateReadErr maps a peer that hangs up in a clean way to io.EOF, which is
// how a yamux session knows a normal end. Other close codes and errors of the
// transport go up unchanged, so that the session reports a real failure.
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
