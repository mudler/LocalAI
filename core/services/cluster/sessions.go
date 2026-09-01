// SPDX-License-Identifier: MIT

package cluster

import (
	"net"
	"sync"

	"github.com/libp2p/go-yamux/v5"
	"github.com/mudler/xlog"
)

// SessionStore holds the peer links this replica has ACCEPTED, which is the
// mirror image of PeerPool: the pool owns the sessions this replica dialled,
// this owns the ones its peers dialled into it.
//
// Something has to own an accepted session. The HTTP handler cannot: it returns
// as soon as the upgrade is done, and the hijacked connection outlives it. And
// something has to accept the streams that arrive on it, because yamux only
// acknowledges a stream once the far side accepts it, so a session nobody
// accepts on does not fail a peer's Open, it hangs it.
type SessionStore struct {
	// onStream handles one accepted stream and owns closing it. A nil handler
	// closes the stream immediately, which is what a replica with no relay
	// installed should do: refuse promptly rather than leave a peer parked.
	//
	// In distributed mode this is Relay.Stream, which splices the stream onto
	// a worker tunnel this replica holds. Nil is reached only from specs, and
	// from a caller that wants a store with no relay.
	onStream func(peerID string, stream net.Conn)

	mu       sync.Mutex
	sessions map[string]*yamux.Session
	closed   bool
}

// NewSessionStore returns a store whose accepted streams are handled by
// onStream. Pass nil to refuse every stream, closing it at once.
func NewSessionStore(onStream func(peerID string, stream net.Conn)) *SessionStore {
	return &SessionStore{onStream: onStream, sessions: map[string]*yamux.Session{}}
}

// Accept takes ownership of a session a peer dialled in. It is the callback
// shape RegisterClusterRoutes wants, and it returns promptly: the serving loop
// runs on its own goroutine, because the handler's return is what completes the
// hijack.
func (s *SessionStore) Accept(peerID string, sess *yamux.Session) {
	if sess == nil {
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		// Shutdown raced the dial. Leaving the session open would keep the peer
		// believing it has a live link into a process that is going away.
		_ = sess.Close()
		return
	}
	previous := s.sessions[peerID]
	s.sessions[peerID] = sess
	s.mu.Unlock()

	// A peer that dials again has lost its previous link, whether or not this
	// side has noticed. Keeping both would leave a session nothing can ever be
	// routed to, since the map holds one per peer.
	if previous != nil {
		xlog.Debug("cluster peer re-dialled, dropping its previous link", "peer", peerID)
		_ = previous.Close()
	}

	go s.serve(peerID, sess)
}

// Get returns the session this replica accepted from peerID. The second result
// is false when no link from that peer is held, which a caller must not read as
// the peer being absent: it may be about to dial, or dialling this replica may
// simply not be its job.
func (s *SessionStore) Get(peerID string) (*yamux.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[peerID]
	return sess, ok
}

// serve accepts streams until the session dies, then forgets it.
func (s *SessionStore) serve(peerID string, sess *yamux.Session) {
	defer func() {
		s.forget(peerID, sess)
		_ = sess.Close()
	}()

	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			// A peer link ending is ordinary: a rolling update closes every
			// session it holds. The error is the session's, not one stream's,
			// so there is nothing to recover to.
			xlog.Debug("cluster peer link ended", "peer", peerID, "error", err)
			return
		}
		if s.onStream == nil {
			// No relay installed. Closing is deliberate and is not the same as
			// ignoring: a stream nobody answers parks the peer's request until
			// its own deadline, and reports nothing about why.
			xlog.Debug("cluster peer stream refused: no relay installed", "peer", peerID)
			_ = stream.Close()
			continue
		}
		// One goroutine per stream: the handler relays a whole request, and
		// serving them from the accept loop would let one request stall every
		// other stream on the link.
		go s.onStream(peerID, stream)
	}
}

// forget drops the entry only if it still names this session. A peer that
// re-dialled has already replaced it, and deleting blindly would evict the live
// link when the old one finally noticed it was dead.
func (s *SessionStore) forget(peerID string, sess *yamux.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[peerID] == sess {
		delete(s.sessions, peerID)
	}
}

// CloseAll drops every held session. An Accept after it closes the session
// rather than storing it, so a dial racing shutdown cannot leak a link.
func (s *SessionStore) CloseAll() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	held := s.sessions
	s.sessions = map[string]*yamux.Session{}
	s.mu.Unlock()

	for _, sess := range held {
		_ = sess.Close()
	}
}
