package tunnel

import (
	"time"

	"github.com/gorilla/websocket"
)

// BufferSize is the size of the read buffer and of the write buffer of every
// websocket of the tunnel. The default of gorilla is 4 KiB. With 4 KiB the
// direction that sends from the worker to the frontend was five times slower
// than the other direction on a loopback link, because the sending side masks
// every payload and does it in pieces of the size of the buffer. A buffer of
// 64 KiB matches the largest frame yamux sends.
const BufferSize = 64 << 10

// NewUpgrader returns the upgrader that every handler of the tunnel uses. It
// keeps the default origin check of gorilla, which refuses a browser from
// another origin and admits a client that sends no Origin header, as a worker
// does.
func NewUpgrader() *websocket.Upgrader {
	return &websocket.Upgrader{
		ReadBufferSize:  BufferSize,
		WriteBufferSize: BufferSize,
	}
}

// NewDialer returns the dialer that every client of the tunnel uses.
func NewDialer(handshakeTimeout time.Duration) *websocket.Dialer {
	return &websocket.Dialer{
		HandshakeTimeout: handshakeTimeout,
		ReadBufferSize:   BufferSize,
		WriteBufferSize:  BufferSize,
	}
}
