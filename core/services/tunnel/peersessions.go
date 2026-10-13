package tunnel

import (
	"net"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/mudler/xlog"
)

// peerClientSession starts the dialling side of a peer link on ws.
//
// A peer link is a session with the windows of the bulk lane, because it carries
// the same kind of traffic, including the artifacts of models that are relayed to
// the owner of a tunnel. Both ends use the same configuration, since a window is
// announced by the side that receives.
//
// The dialling side accepts no stream. The far side opens none to it: it has a
// link of its own, dialled by itself, for what it sends to this replica. A
// stream that arrives here is reset, like a stream that a worker opens.
func peerClientSession(ws *websocket.Conn) (*Session, error) {
	return clientSessionWith(ws, LaneBulk, 0)
}

// PeerServerSession starts the accepting side of a peer link on ws. It accepts
// the streams of the dialling replica, up to the default limit of the
// multiplexer. A window is a bound on data received and not read, so a replica is
// sized against the windows times the limit for each link.
func PeerServerSession(ws *websocket.Conn) (*Session, error) {
	return serverSessionWith(ws, LaneBulk, defaultIncomingStreams)
}

// PeerSessions holds the peer links that this replica has accepted. It is the
// mirror of PeerPool: the pool owns the sessions that this replica dialled, and
// this owns those that its peers dialled into it.
//
// Something has to own an accepted session. The HTTP handler cannot, because it
// returns when the upgrade is done and the hijacked connection outlives it. And
// something has to accept the streams that arrive, because a stream is
// acknowledged only when the far side accepts it, so a session nobody accepts on
// does not fail an Open of the peer, it hangs it.
type PeerSessions struct {
	// onStream handles one accepted stream and owns closing it. Nil closes the
	// stream at once, which is what a replica with no relay does: it refuses
	// promptly and does not leave a peer parked.
	onStream func(peerID string, stream net.Conn)

	mu       sync.Mutex
	sessions map[string]*Session
	closed   bool
}

// NewPeerSessions returns a holder whose accepted streams go to onStream.
func NewPeerSessions(onStream func(peerID string, stream net.Conn)) *PeerSessions {
	return &PeerSessions{onStream: onStream, sessions: map[string]*Session{}}
}

// Accept takes the ownership of a session that a peer dialled in, and returns at
// once: the loop that serves it runs on its own goroutine, because the return of
// the handler completes the hijack.
func (s *PeerSessions) Accept(peerID string, sess *Session) {
	if sess == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = sess.Close()
		return
	}
	previous := s.sessions[peerID]
	s.sessions[peerID] = sess
	s.mu.Unlock()

	// A peer that dials again has lost its previous link, whether or not this side
	// noticed. Keeping both would leave a session that nothing can be routed to.
	if previous != nil {
		xlog.Debug("tunnel peer dialled again, dropping its previous link", "peer", peerID)
		_ = previous.Close()
	}
	go s.serve(peerID, sess)
}

// Holds reports whether this replica holds an accepted link from peerID. A false
// answer is not an absent peer: it may be about to dial.
func (s *PeerSessions) Holds(peerID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[peerID]
	return ok && !sess.IsClosed()
}

func (s *PeerSessions) serve(peerID string, sess *Session) {
	defer func() {
		s.forget(peerID, sess)
		_ = sess.Close()
	}()
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			// A link that ends is ordinary: a rolling update closes every session.
			xlog.Debug("tunnel peer link ended", "peer", peerID, "error", err)
			return
		}
		if s.onStream == nil {
			xlog.Debug("tunnel peer stream refused: no relay installed", "peer", peerID)
			_ = stream.Close()
			continue
		}
		// One goroutine for each stream: the handler relays a whole request.
		go s.onStream(peerID, stream)
	}
}

// forget drops the entry only if it still names this session. A peer that dialled
// again replaced it, and deleting blindly would evict the live link when the old
// one noticed that it died.
func (s *PeerSessions) forget(peerID string, sess *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[peerID] == sess {
		delete(s.sessions, peerID)
	}
}

// Close drops every held session. An Accept after it closes the session, so a
// dial that races the shutdown cannot leak a link.
func (s *PeerSessions) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	held := s.sessions
	s.sessions = map[string]*Session{}
	s.mu.Unlock()
	for _, sess := range held {
		_ = sess.Close()
	}
}
