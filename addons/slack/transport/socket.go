// SPDX-License-Identifier: MIT
package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Socket Mode pushes the events of an app over a websocket, and the host
// pulls: it asks the route for updates with the op poll. Socket keeps one
// Socket Mode link for each pair of tokens that is polled, acknowledges each
// event at once (Slack sends it again after three seconds otherwise) and
// queues it until a poll hands it over. The cursor of an update is a time in
// microseconds, so a new process never hands out a cursor below the offset
// that the host stored before. A link that is not polled for IdleAfter is
// closed: the binding was switched off or deleted.
//
// State mode: per instance. The links and the queues live in the add-on
// process; the host polls them from its one polling frontend. A queued event
// that was not handed over when the process stops is lost, as Slack considers
// it delivered.
type Socket struct {
	API *API
	// Dialer opens the websocket. Nil: websocket.DefaultDialer.
	Dialer *websocket.Dialer
	// IdleAfter is two minutes when zero.
	IdleAfter time.Duration
	// Logger is slog.Default when nil. It never gets a token.
	Logger *slog.Logger

	mu       sync.Mutex
	sessions map[string]*session
}

// Limits of the queue of a link and of what it remembers.
const (
	maxQueued   = 500
	maxPoll     = 100
	maxSeen     = 2000
	maxThreads  = 1000
	threadLife  = 24 * time.Hour
	userLife    = time.Hour
	readTimeout = 2 * time.Minute
)

type session struct {
	key    string
	tokens Tokens
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	queue    []queued
	wake     chan struct{}
	last     int64
	lastPoll time.Time
	fatal    error
	bot      Identity
	seen     map[string]struct{}
	seenList []string
	threads  map[string]time.Time
	users    map[string]cachedUser
}

type queued struct {
	msg  contract.ConnectorMessage
	user string // the Slack user, whose names are read when the update is handed over
}

type cachedUser struct {
	user User
	at   time.Time
}

func keyOf(t Tokens) string {
	sum := sha256.Sum256([]byte(t.Bot + "\x00" + t.App))
	return hex.EncodeToString(sum[:])
}

func (s *Socket) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Poll hands over the updates from offset on, and waits up to wait seconds
// for one when there is none. The first poll of a pair of tokens opens its
// link.
func (s *Socket) Poll(ctx context.Context, t Tokens, offset int64, wait int) ([]contract.ConnectorMessage, error) {
	if t.Bot == "" || t.App == "" {
		return nil, missing(t)
	}
	ss := s.session(t, offset)
	deadline := time.NewTimer(time.Duration(wait) * time.Second)
	defer deadline.Stop()
	for {
		ss.mu.Lock()
		ss.lastPoll = time.Now()
		// What the host has handled is below its offset.
		ss.queue = slices.DeleteFunc(ss.queue, func(q queued) bool { return q.msg.Cursor < offset })
		if ss.fatal != nil {
			err := ss.fatal
			ss.mu.Unlock()
			s.drop(ss)
			return nil, err
		}
		if len(ss.queue) > 0 || wait <= 0 {
			out := slices.Clone(ss.queue[:min(len(ss.queue), maxPoll)])
			ss.mu.Unlock()
			return s.names(ctx, ss, out), nil
		}
		wake := ss.wake
		ss.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			wait = 0
		case <-wake:
		}
	}
}

func missing(t Tokens) error {
	if t.Bot == "" {
		return fmt.Errorf("slack: the bot token (xoxb-) is missing: %w", connector.ErrUnauthorized)
	}
	return fmt.Errorf("slack: the app-level token (xapp-) is missing: %w", connector.ErrUnauthorized)
}

// session returns the link of a pair of tokens, opening it when there is none.
func (s *Socket) session(t Tokens, offset int64) *session {
	key := keyOf(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.sessions[key]; ss != nil {
		return ss
	}
	if s.sessions == nil {
		s.sessions = map[string]*session{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	ss := &session{key: key, tokens: t, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}),
		last: offset - 1, lastPoll: time.Now(), seen: map[string]struct{}{}, threads: map[string]time.Time{}, users: map[string]cachedUser{}}
	s.sessions[key] = ss
	go func() {
		defer close(ss.done)
		s.run(ctx, ss)
	}()
	return ss
}

// drop forgets a link and stops it.
func (s *Socket) drop(ss *session) {
	s.mu.Lock()
	if s.sessions[ss.key] == ss {
		delete(s.sessions, ss.key)
	}
	s.mu.Unlock()
	ss.cancel()
}

// Close stops every link and waits for them.
func (s *Socket) Close() {
	s.mu.Lock()
	all := make([]*session, 0, len(s.sessions))
	for _, ss := range s.sessions {
		all = append(all, ss)
	}
	s.sessions = nil
	s.mu.Unlock()
	for _, ss := range all {
		ss.cancel()
		<-ss.done
	}
}

// Links is the number of open links, for tests.
func (s *Socket) Links() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *Socket) idleAfter() time.Duration {
	if s.IdleAfter > 0 {
		return s.IdleAfter
	}
	return 2 * time.Minute
}

// run keeps the link of a session open until it is idle, stopped, or Slack
// refuses a token.
func (s *Socket) run(ctx context.Context, ss *session) {
	// A link that failed for good stays known until a poll reads the failure,
	// or until it is idle, so the host learns why it stopped.
	defer func() {
		ss.mu.Lock()
		fatal := ss.fatal
		ss.mu.Unlock()
		if fatal == nil {
			s.drop(ss)
		}
	}()
	idle := time.NewTicker(max(s.idleAfter()/4, 10*time.Millisecond))
	defer idle.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-idle.C:
				ss.mu.Lock()
				gone := time.Since(ss.lastPoll) > s.idleAfter()
				ss.mu.Unlock()
				if gone {
					s.drop(ss)
					return
				}
			}
		}
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		err := s.connect(ctx, ss)
		if ctx.Err() != nil {
			return
		}
		var ra *connector.RetryAfter
		wait := backoff
		switch {
		case errors.Is(err, connector.ErrUnauthorized), errors.Is(err, errLinkDisabled):
			ss.mu.Lock()
			ss.fatal = err
			close(ss.wake)
			ss.wake = make(chan struct{})
			ss.mu.Unlock()
			return
		case errors.As(err, &ra):
			wait = time.Duration(ra.Seconds) * time.Second
		case err == nil:
			// Slack asked for a new link: open it at once.
			backoff, wait = time.Second, 0
		default:
			s.logger().Warn("slack: the Socket Mode link failed; it is opened again", "error", err)
			backoff = min(backoff*2, 30*time.Second)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

var errLinkDisabled = fmt.Errorf("slack: Socket Mode is off for this app: %w", connector.ErrUnauthorized)

// frame is a message of Socket Mode.
type frame struct {
	Type       string          `json:"type"`
	EnvelopeID string          `json:"envelope_id"`
	Reason     string          `json:"reason"`
	Payload    json.RawMessage `json:"payload"`
}

// connect opens one link and reads it until Slack closes it, asks for a new
// one, or ctx ends. It returns nil when Slack asks for a new link.
func (s *Socket) connect(ctx context.Context, ss *session) error {
	bot, err := s.API.AuthTest(ctx, ss.tokens.Bot)
	if err != nil {
		return err
	}
	ss.mu.Lock()
	ss.bot = bot
	ss.mu.Unlock()
	address, err := s.API.OpenConnection(ctx, ss.tokens.App)
	if err != nil {
		return err
	}
	dialer := s.Dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	conn, resp, err := dialer.DialContext(ctx, address, http.Header{})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		// The address holds a ticket: it is not repeated.
		return errors.New("slack: the Socket Mode link could not be opened")
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	ping := conn.PingHandler()
	conn.SetPingHandler(func(data string) error {
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		return ping(data)
	})
	conn.SetReadLimit(1 << 20)
	for {
		var f frame
		if err := conn.ReadJSON(&f); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("slack: the Socket Mode link was closed")
		}
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		if f.EnvelopeID != "" {
			// Acknowledge first: Slack waits three seconds for it.
			if err := conn.WriteJSON(map[string]string{"envelope_id": f.EnvelopeID}); err != nil {
				return errors.New("slack: the Socket Mode link was closed")
			}
		}
		switch f.Type {
		case "disconnect":
			if f.Reason == "link_disabled" {
				return errLinkDisabled
			}
			return nil
		case "events_api":
			ss.take(f.Payload)
		}
	}
}

// take queues the message of an event, when the bot should read it.
func (ss *session) take(payload json.RawMessage) {
	var p struct {
		EventID string `json:"event_id"`
		Event   event  `json:"event"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if p.EventID != "" {
		if _, dup := ss.seen[p.EventID]; dup {
			return
		}
		ss.seen[p.EventID] = struct{}{}
		ss.seenList = append(ss.seenList, p.EventID)
		if len(ss.seenList) > maxSeen {
			delete(ss.seen, ss.seenList[0])
			ss.seenList = ss.seenList[1:]
		}
	}
	m, ok := ss.message(p.Event)
	if !ok {
		return
	}
	ss.last = max(ss.last+1, time.Now().UnixMicro())
	m.Cursor = ss.last
	ss.queue = append(ss.queue, queued{msg: m, user: p.Event.User})
	if len(ss.queue) > maxQueued {
		ss.queue = ss.queue[len(ss.queue)-maxQueued:]
	}
	close(ss.wake)
	ss.wake = make(chan struct{})
}

// names fills in the names of the senders of updates, from a cache.
func (s *Socket) names(ctx context.Context, ss *session, out []queued) []contract.ConnectorMessage {
	msgs := make([]contract.ConnectorMessage, len(out))
	for i, q := range out {
		m := q.msg
		if u, ok := s.user(ctx, ss, q.user); ok {
			m.SenderName, m.SenderUsername = u.Display(), u.Name
		}
		msgs[i] = m
	}
	return msgs
}

func (s *Socket) user(ctx context.Context, ss *session, id string) (User, bool) {
	ss.mu.Lock()
	c, ok := ss.users[id]
	ss.mu.Unlock()
	if ok && time.Since(c.at) < userLife {
		return c.user, true
	}
	uctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u, err := s.API.UserInfo(uctx, ss.tokens.Bot, id)
	if err != nil {
		// Without the scope users:read the sender keeps only an ID.
		return User{}, false
	}
	ss.mu.Lock()
	if len(ss.users) > maxThreads {
		clear(ss.users)
	}
	ss.users[id] = cachedUser{user: u, at: time.Now()}
	ss.mu.Unlock()
	return u, true
}
