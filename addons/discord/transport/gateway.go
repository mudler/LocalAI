// SPDX-License-Identifier: MIT
package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/websocket"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Gateway holds one Discord Gateway link for each bot token that is polled,
// and the host pulls: it asks the route for updates with the op poll. The
// link is a websocket that discordgo runs (hello, identify, heartbeats and
// their acknowledgements, resume). The add-on reads the events it needs,
// queues the messages that address the bot, and hands them over on the next
// poll. The cursor of an update is a time in microseconds, so a new process
// never hands out a cursor below the offset that the host stored before. A
// link that is not polled for IdleAfter is closed: the binding was switched
// off or deleted.
//
// State mode: per instance. The links and the queues live in the add-on
// process; the host polls them from its one polling frontend. A queued
// message that was not handed over when the process stops is lost. The
// threads the bot follows are kept in StateDir.
//
// Two safeguards protect the queue: a sender who writes more than
// SenderPerMinute messages a minute is dropped, and a queue that is full
// drops its oldest message. Every drop is counted (Dropped) and logged.
type Gateway struct {
	API *API
	// Client sends the REST calls of the Gateway client (discovery of the
	// gateway address). Nil: the client of discordgo.
	Client *http.Client
	// Dialer opens the websocket. Nil: the dialer of discordgo.
	Dialer *websocket.Dialer
	// IdleAfter is two minutes when zero.
	IdleAfter time.Duration
	// Backoff is the first wait before a link is opened again after it failed;
	// it doubles up to five minutes. Zero: five seconds. Discord allows 1000
	// identifies a day for a bot, so a loop must not be tight.
	Backoff time.Duration
	// Logger is slog.Default when nil. It never gets a token.
	Logger *slog.Logger
	// StateDir is where the add-on keeps the threads it follows, so that a
	// restart does not forget them. Empty: they are kept in memory only.
	StateDir string

	dropped atomic.Uint64
	mu      sync.Mutex
	links   map[string]*link
}

// Limits of the queue of a link and of what it remembers.
const (
	maxQueued  = 500
	maxPoll    = 100
	maxSeen    = 2000
	maxThreads = 1000
	maxChans   = 5000
	inboxSize  = 256
	// SenderPerMinute is how many messages of one sender a link queues in a
	// minute. The host limits what it answers; this keeps a flood in a public
	// channel from filling the queue before the host sees it.
	SenderPerMinute = 30
	threadLife      = 24 * time.Hour
)

// Intents the link asks for: servers (for their channels and threads), the
// messages of servers and of direct conversations, and their content. The
// last one is privileged: it must be on for the bot in the developer portal.
const intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentMessageContent

type chanInfo struct {
	typ    int
	parent string
}

type link struct {
	key    string
	token  string
	cancel context.CancelFunc
	done   chan struct{}
	g      *Gateway

	// in carries the messages of the Gateway reader to the worker, so that
	// the reader never waits for a lookup.
	in   chan *discordgo.Message
	down chan struct{}

	mu       sync.Mutex
	queue    []contract.ConnectorMessage
	wake     chan struct{}
	last     int64
	lastPoll time.Time
	fatal    error
	bot      User
	seen     map[string]struct{}
	seenList []string
	threads  map[string]time.Time
	chans    map[string]chanInfo
	rate     map[string][]time.Time
	mode     string
	dir      string
	reported uint64

	saveFailed bool
}

func keyOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (g *Gateway) logger() *slog.Logger {
	if g.Logger != nil {
		return g.Logger
	}
	return slog.Default()
}

// Dropped is how many messages the links dropped: a full queue, a full inbox
// or a sender over the limit.
func (g *Gateway) Dropped() uint64 { return g.dropped.Load() }

// Links is the number of open links, for tests.
func (g *Gateway) Links() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.links)
}

func (g *Gateway) idleAfter() time.Duration {
	if g.IdleAfter > 0 {
		return g.IdleAfter
	}
	return 2 * time.Minute
}

func (g *Gateway) backoff() time.Duration {
	if g.Backoff > 0 {
		return g.Backoff
	}
	return 5 * time.Second
}

func missing() error {
	return fmt.Errorf("discord: the bot token is missing: %w", connector.ErrUnauthorized)
}

// Poll hands over the updates from offset on, and waits up to wait seconds
// for one when there is none. The first poll of a token opens its link.
func (g *Gateway) Poll(ctx context.Context, token string, offset int64, wait int) ([]contract.ConnectorMessage, error) {
	if token == "" {
		return nil, missing()
	}
	l := g.link(token, offset)
	l.mu.Lock()
	l.mode = followMode(connector.OptionsFrom(ctx)[FollowOption])
	l.mu.Unlock()
	deadline := time.NewTimer(time.Duration(wait) * time.Second)
	defer deadline.Stop()
	for {
		l.mu.Lock()
		l.lastPoll = time.Now()
		// What the host has handled is below its offset.
		l.queue = slices.DeleteFunc(l.queue, func(m contract.ConnectorMessage) bool { return m.Cursor < offset })
		if l.fatal != nil {
			err := l.fatal
			l.mu.Unlock()
			g.drop(l)
			return nil, err
		}
		if len(l.queue) > 0 || wait <= 0 {
			out := slices.Clone(l.queue[:min(len(l.queue), maxPoll)])
			l.mu.Unlock()
			l.reportDrops()
			return out, nil
		}
		wake := l.wake
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			wait = 0
		case <-wake:
		}
	}
}

// reportDrops logs once when messages were dropped since the last report.
func (l *link) reportDrops() {
	n := l.g.dropped.Load()
	l.mu.Lock()
	changed := n != l.reported
	l.reported = n
	l.mu.Unlock()
	if changed {
		l.g.logger().Warn("discord: messages were dropped (queue full, inbox full or sender over the limit)", "dropped_total", n)
	}
}

// link returns the link of a token, opening it when there is none.
func (g *Gateway) link(token string, offset int64) *link {
	key := keyOf(token)
	g.mu.Lock()
	defer g.mu.Unlock()
	if l := g.links[key]; l != nil {
		return l
	}
	if g.links == nil {
		g.links = map[string]*link{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &link{key: key, token: token, cancel: cancel, done: make(chan struct{}), g: g,
		in: make(chan *discordgo.Message, inboxSize), down: make(chan struct{}, 1), wake: make(chan struct{}),
		last: offset - 1, lastPoll: time.Now(), seen: map[string]struct{}{}, threads: loadThreads(g.StateDir, key, time.Now()),
		chans: map[string]chanInfo{}, rate: map[string][]time.Time{}, mode: FollowMentioned, dir: g.StateDir}
	g.links[key] = l
	go l.work(ctx)
	go func() {
		defer close(l.done)
		g.run(ctx, l)
	}()
	return l
}

// drop forgets a link and stops it.
func (g *Gateway) drop(l *link) {
	g.mu.Lock()
	if g.links[l.key] == l {
		delete(g.links, l.key)
	}
	g.mu.Unlock()
	l.cancel()
}

// Close stops every link and waits for them.
func (g *Gateway) Close() {
	g.mu.Lock()
	all := make([]*link, 0, len(g.links))
	for _, l := range g.links {
		all = append(all, l)
	}
	g.links = nil
	g.mu.Unlock()
	for _, l := range all {
		l.cancel()
		<-l.done
	}
}

// botID is the ID of the bot of a token: from the open link when there is
// one, else from the REST API. It is empty when it cannot be told.
func (g *Gateway) botID(ctx context.Context, token string) string {
	g.mu.Lock()
	l := g.links[keyOf(token)]
	g.mu.Unlock()
	if l != nil {
		l.mu.Lock()
		id := l.bot.ID
		l.mu.Unlock()
		if id != "" {
			return id
		}
	}
	u, err := g.API.Me(ctx, token)
	if err != nil {
		return ""
	}
	return u.ID
}

// run keeps the link of a token open until it is idle, stopped, or Discord
// refuses the token or the intents.
func (g *Gateway) run(ctx context.Context, l *link) {
	// A link that failed for good stays known until a poll reads the failure,
	// or until it is idle, so the host learns why it stopped.
	defer func() {
		l.mu.Lock()
		fatal := l.fatal
		l.mu.Unlock()
		if fatal == nil {
			g.drop(l)
		}
	}()
	idle := time.NewTicker(max(g.idleAfter()/4, 10*time.Millisecond))
	defer idle.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-idle.C:
				l.mu.Lock()
				gone := time.Since(l.lastPoll) > g.idleAfter()
				l.mu.Unlock()
				if gone {
					g.drop(l)
					return
				}
			}
		}
	}()
	var dg *discordgo.Session
	defer func() {
		if dg != nil {
			_ = dg.Close()
		}
	}()
	backoff := g.backoff()
	for ctx.Err() == nil {
		started := time.Now()
		var err error
		dg, err = g.connect(ctx, l, dg)
		if ctx.Err() != nil {
			return
		}
		var ra *connector.RetryAfter
		wait := backoff
		switch {
		case errors.Is(err, connector.ErrUnauthorized):
			l.mu.Lock()
			l.fatal = err
			close(l.wake)
			l.wake = make(chan struct{})
			l.mu.Unlock()
			return
		case errors.As(err, &ra):
			wait = time.Duration(ra.Seconds) * time.Second
		default:
			g.logger().Warn("discord: the Gateway link failed; it is opened again", "error", err, "in", backoff.String())
		}
		if time.Since(started) > time.Minute {
			backoff = g.backoff()
		} else {
			backoff = min(backoff*2, 5*time.Minute)
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

// Close codes of the Gateway that no new try fixes.
const (
	closeAuthFailed       = 4004
	closeInvalidIntents   = 4013
	closeDisallowedIntent = 4014
)

var errLinkLost = errors.New("discord: the Gateway link was closed")

// connect opens the link, or opens it again with the session of the last
// try (discordgo then resumes), and holds it until it is lost or ctx ends.
func (g *Gateway) connect(ctx context.Context, l *link, dg *discordgo.Session) (*discordgo.Session, error) {
	bot, err := g.API.Me(ctx, l.token)
	if err != nil {
		return dg, err
	}
	l.mu.Lock()
	l.bot = bot
	l.mu.Unlock()
	if dg == nil {
		dg = g.newSession(l)
	}
	select {
	case <-l.down:
	default:
	}
	if err := dg.Open(); err != nil {
		return dg, openError(err)
	}
	select {
	case <-ctx.Done():
		return dg, ctx.Err()
	case <-l.down:
		return dg, errLinkLost
	}
}

// openError maps the way a Gateway open failed. A close code that says the
// token or the intents are wrong is final.
func openError(err error) error {
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		switch ce.Code {
		case closeAuthFailed:
			return fmt.Errorf("discord: the Gateway refused the bot token: %w", connector.ErrUnauthorized)
		case closeInvalidIntents, closeDisallowedIntent:
			return fmt.Errorf("discord: the Gateway refused the intents: turn on the Message Content intent of the bot in the developer portal: %w", connector.ErrUnauthorized)
		}
	}
	return err
}

// newSession makes the discordgo session of a link. The session keeps its
// Gateway state between opens, so a lost link resumes. discordgo does not
// reconnect by itself: connect and run do, with a backoff.
func (g *Gateway) newSession(l *link) *discordgo.Session {
	dg, _ := discordgo.New("Bot " + l.token)
	dg.StateEnabled = false
	dg.ShouldReconnectOnError = false
	dg.ShouldReconnectVoiceOnSessionError = false
	dg.SyncEvents = true
	dg.Identify.Intents = intents
	if g.Client != nil {
		dg.Client = g.Client
	}
	if g.Dialer != nil {
		dg.Dialer = g.Dialer
	}
	dg.AddHandler(func(_ *discordgo.Session, e *discordgo.MessageCreate) { l.offer(e.Message) })
	dg.AddHandler(func(_ *discordgo.Session, _ *discordgo.Disconnect) {
		select {
		case l.down <- struct{}{}:
		default:
		}
	})
	dg.AddHandler(func(_ *discordgo.Session, e *discordgo.GuildCreate) {
		for _, c := range e.Channels {
			l.learn(c)
		}
		for _, c := range e.Threads {
			l.learn(c)
		}
	})
	dg.AddHandler(func(_ *discordgo.Session, e *discordgo.ChannelCreate) { l.learn(e.Channel) })
	dg.AddHandler(func(_ *discordgo.Session, e *discordgo.ChannelUpdate) { l.learn(e.Channel) })
	dg.AddHandler(func(_ *discordgo.Session, e *discordgo.ThreadCreate) { l.learn(e.Channel) })
	dg.AddHandler(func(_ *discordgo.Session, e *discordgo.ThreadUpdate) { l.learn(e.Channel) })
	dg.AddHandler(func(_ *discordgo.Session, e *discordgo.ThreadListSync) {
		for _, c := range e.Threads {
			l.learn(c)
		}
	})
	return dg
}

// learn remembers the type and the parent of a channel or thread.
func (l *link) learn(c *discordgo.Channel) {
	if c == nil || c.ID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.chans) >= maxChans {
		clear(l.chans)
	}
	l.chans[c.ID] = chanInfo{typ: int(c.Type), parent: c.ParentID}
}

// offer hands a message of the Gateway reader to the worker. It never waits:
// a full inbox drops the message and counts it.
func (l *link) offer(m *discordgo.Message) {
	if m == nil {
		return
	}
	select {
	case l.in <- m:
	default:
		l.g.dropped.Add(1)
	}
}

// work turns the messages of the inbox into queued updates, in order.
func (l *link) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-l.in:
			l.take(ctx, m)
		}
	}
}

// channel tells the type and parent of a channel, from what the Gateway
// sent, else from the REST API.
func (l *link) channel(ctx context.Context, id string) (chanInfo, bool) {
	l.mu.Lock()
	c, ok := l.chans[id]
	l.mu.Unlock()
	if ok {
		return c, true
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ch, err := l.g.API.Channel(cctx, l.token, id)
	if err != nil {
		return chanInfo{}, false
	}
	c = chanInfo{typ: ch.Type, parent: ch.ParentID}
	l.mu.Lock()
	if len(l.chans) >= maxChans {
		clear(l.chans)
	}
	l.chans[id] = c
	l.mu.Unlock()
	return c, true
}

// take queues the update of a message, when the bot should read it.
func (l *link) take(ctx context.Context, m *discordgo.Message) {
	l.mu.Lock()
	if _, dup := l.seen[m.ID]; dup {
		l.mu.Unlock()
		return
	}
	l.seen[m.ID] = struct{}{}
	l.seenList = append(l.seenList, m.ID)
	if len(l.seenList) > maxSeen {
		delete(l.seen, l.seenList[0])
		l.seenList = l.seenList[1:]
	}
	botID := l.bot.ID
	l.mu.Unlock()
	if !readable(m, botID) {
		return
	}
	var info chanInfo
	var known bool
	if m.GuildID != "" {
		info, known = l.channel(ctx, m.ChannelID)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	msg, ok := l.message(m, info, known)
	if !ok {
		return
	}
	if !l.allowSender(msg.SenderID) {
		l.g.dropped.Add(1)
		return
	}
	l.last = max(l.last+1, time.Now().UnixMicro())
	msg.Cursor = l.last
	l.queue = append(l.queue, msg)
	if len(l.queue) > maxQueued {
		l.g.dropped.Add(uint64(len(l.queue) - maxQueued))
		l.queue = l.queue[len(l.queue)-maxQueued:]
	}
	close(l.wake)
	l.wake = make(chan struct{})
}

// allowSender applies the per-sender limit of the queue. The caller holds l.mu.
func (l *link) allowSender(id string) bool {
	now := time.Now()
	if len(l.rate) > maxThreads {
		for k, ts := range l.rate {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > time.Minute {
				delete(l.rate, k)
			}
		}
	}
	ts := slices.DeleteFunc(l.rate[id], func(t time.Time) bool { return now.Sub(t) > time.Minute })
	if len(ts) >= SenderPerMinute {
		l.rate[id] = ts
		return false
	}
	l.rate[id] = append(ts, now)
	return true
}

// follow remembers a thread that the bot answers in, and keeps it on disk
// when the add-on has a state directory. The caller holds l.mu.
func (l *link) follow(channel, thread string) {
	now := time.Now()
	if len(l.threads) >= maxThreads {
		for k, at := range l.threads {
			if now.Sub(at) > threadLife || len(l.threads) >= maxThreads {
				delete(l.threads, k)
			}
		}
	}
	l.threads[threadKey(channel, thread)] = now
	l.saveThreads()
}

// follows says whether a message without a mention in the thread is addressed
// to the bot. A follow mode of never follows nothing. A followed thread that
// is active is renewed, so it lives for threadLife after its last message.
// The caller holds l.mu.
func (l *link) follows(channel, thread string) bool {
	if l.mode == FollowNever {
		return false
	}
	k := threadKey(channel, thread)
	at, ok := l.threads[k]
	if !ok || time.Since(at) >= threadLife {
		return false
	}
	if time.Since(at) >= touchEvery {
		l.threads[k] = time.Now()
		l.saveThreads()
	}
	return true
}

func (l *link) saveThreads() {
	if err := saveThreads(l.dir, l.key, l.threads); err != nil && !l.saveFailed {
		l.saveFailed = true
		l.g.logger().Warn("discord: could not save the followed threads", "error", err)
	}
}
