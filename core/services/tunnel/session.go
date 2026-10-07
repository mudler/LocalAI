package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-yamux/v5"
)

// Lane names one of the two sessions that a worker holds to a frontend.
//
// The inference lane carries the requests that need a short delay: model
// calls, control and health. The bulk lane carries file transfers. A large
// transfer on the inference lane delays every other stream of the session by the
// amount of data that is in flight ahead of them, because yamux queues up to one
// window of data for each stream before a new frame gets its turn on the
// websocket. A separate session has its own websocket and its own window, so the
// two cannot delay each other.
type Lane string

const (
	LaneInference Lane = "inference"
	LaneBulk      Lane = "bulk"
)

// Valid reports whether l is a lane that this package knows.
func (l Lane) Valid() bool { return l == LaneInference || l == LaneBulk }

// ParseLane maps the value of the lane parameter of the connect request to a
// lane. The empty value is the inference lane, which is what a worker that
// predates the bulk lane connects to.
func ParseLane(s string) (Lane, error) {
	if s == "" {
		return LaneInference, nil
	}
	if l := Lane(s); l.Valid() {
		return l, nil
	}
	return "", fmt.Errorf("unknown tunnel lane %q", s)
}

// The bulk windows are larger than the defaults of yamux (256 KiB at the start,
// 16 MiB at most). With the defaults a transfer on a link with 20 ms of latency
// runs about 20% slower. The larger windows also let more data queue ahead of a
// new frame, so they would raise the delay of small calls on a shared session.
// That is why they apply to the bulk lane only. The inference lane keeps the
// defaults.
const (
	bulkInitialWindow = 4 << 20
	bulkMaxWindow     = 32 << 20
)

// sessionConfig returns the yamux configuration of a lane. It returns a new
// value for every call, because yamux keeps the pointer for the life of the
// session and two sessions must not share a struct.
//
// Replica-to-replica links carry the same kind of traffic as the bulk lane and
// use its windows. Both ends of a session must use the same configuration for
// it to take effect in both directions, because a window is announced by the
// side that receives.
func sessionConfig(lane Lane) *yamux.Config {
	cfg := yamux.DefaultConfig()
	// yamux writes its warnings to standard error. The callers report a dead
	// session through the errors they get.
	cfg.LogOutput = io.Discard
	if lane == LaneBulk {
		cfg.InitialStreamWindowSize = bulkInitialWindow
		cfg.MaxStreamWindowSize = bulkMaxWindow
	}
	return cfg
}

// Session is a multiplexed session on one websocket.
type Session struct {
	s *yamux.Session
}

// ServerSession starts the frontend side of a session on ws. The frontend owns
// the streams with even numbers and opens the streams. It does not accept any,
// because only the frontend asks and the worker answers.
func ServerSession(ws *websocket.Conn, lane Lane) (*Session, error) {
	s, err := yamux.Server(websocketConn(ws), sessionConfig(lane), nil)
	if err != nil {
		return nil, fmt.Errorf("starting the server side of a tunnel session: %w", err)
	}
	return &Session{s: s}, nil
}

// ClientSession starts the worker side of a session on ws. The worker accepts
// the streams that the frontend opens.
func ClientSession(ws *websocket.Conn, lane Lane) (*Session, error) {
	s, err := yamux.Client(websocketConn(ws), sessionConfig(lane), nil)
	if err != nil {
		return nil, fmt.Errorf("starting the client side of a tunnel session: %w", err)
	}
	return &Session{s: s}, nil
}

// OpenStream opens a stream to the other side.
func (s *Session) OpenStream(ctx context.Context) (net.Conn, error) {
	st, err := s.s.OpenStream(ctx)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// AcceptStream waits for the other side to open a stream. It takes no context.
// Close the session to end the wait.
func (s *Session) AcceptStream() (net.Conn, error) {
	st, err := s.s.AcceptStream()
	if err != nil {
		return nil, err
	}
	return st, nil
}

// Close ends the session and every stream on it.
func (s *Session) Close() error { return s.s.Close() }

// CloseChan is closed when the session has ended.
func (s *Session) CloseChan() <-chan struct{} { return s.s.CloseChan() }

// IsClosed reports whether the session has ended.
func (s *Session) IsClosed() bool { return s.s.IsClosed() }
