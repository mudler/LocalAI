package studio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Sentences the bot says. They name no internal error.
const (
	sentUnsupported = "I can only read text messages for now."
	sentTooLong     = "That message is too long. Please send less than 8,000 characters."
	sentRate        = "Too many messages: please wait a minute."
	sentUnavailable = "This bot is not available right now."
	sentNoAnswer    = "The agent had nothing to say."
	sentStopped     = "The turn was stopped."
	sentInterrupted = "The agent was interrupted. Please send your message again."
	sentFailed      = "The agent stopped because of an error."
	sentNoFile      = "I could not read the file you sent."
	sentNoFiles     = "I can only read the text of your message here: files are turned off."

	conflictTries   = 6
	maxMessageRunes = 8000
)

// ConnectorKind is a kind of binding that a running connector add-on serves.
type ConnectorKind struct {
	AddonID string
	contract.ConnectorKind
}

// ConnectorTransport reaches the connector add-ons. The application implements
// it over the add-on manager; a test implements it with a script.
type ConnectorTransport interface {
	// Kinds lists the kinds that running add-ons serve now. It is empty when the
	// process does not run add-ons.
	Kinds(ctx context.Context) ([]ConnectorKind, error)
	// Call sends one request to the connector route of an add-on.
	Call(ctx context.Context, addonID string, req contract.ConnectorRequest) (contract.ConnectorResponse, error)
}

// ConnectorOptions configures the connector manager.
type ConnectorOptions struct {
	// Store holds bindings and the state of their loops. Required.
	Store connectors.Store
	// Transport reaches the connector add-ons. Required.
	Transport ConnectorTransport
	Clock     func() time.Time
	// Tick is how often the manager looks for bindings to start or stop; zero
	// means 2 seconds.
	Tick time.Duration
	// Poll is the long-poll time of a request, in seconds. Zero means no wait,
	// which only a test wants; the application sets 25.
	Poll int
	// Backoff is the first wait after an error; it doubles up to a minute. Zero
	// means one second.
	Backoff time.Duration
	// ConflictWait is how long to wait after the platform says another process
	// reads the updates of the bot; zero means 10 seconds. A new leader meets
	// this answer while the long poll of the old one is still open, so the
	// binding is only switched off after several answers in a row.
	ConflictWait time.Duration
	// RatePerMinute is how many messages one sender may send; zero means 20.
	RatePerMinute int
	// Lead says whether this frontend runs the bindings now. Nil: always.
	Lead   func(ctx context.Context) bool
	Logger *slog.Logger
}

// Connectors runs the bindings of the chats. A message becomes a chat of the
// binding's owner with the binding's agent, through the same service as any
// chat, so the model route, the quota and the agent's grants apply.
//
// The platform's API is spoken by a connector add-on. A message reaches the
// manager by one function, Ingest: the polling loop of a pull platform feeds it
// with what the add-on's poll route answers, and a push platform's add-on calls
// it through the host operation connector.inbound. Authorization, the
// allowlist and the sender log are therefore one code path. Nothing here
// depends on the HTTP frontend, so a worker node can run the same code.
//
// State mode: shared. Bindings, offsets, the sender log and the chat of each
// sender are in the store; the polling loops run on one frontend at a time, the
// leader, and a new leader continues from the stored offset. The add-on runs on
// the process that owns the add-ons, so the loops run only where the add-on
// answers.
type Connectors struct {
	svc *Service
	o   ConnectorOptions
	log *slog.Logger

	sealMu sync.Mutex
	sealer *connectors.Sealer
	wmu    sync.Mutex // serialises writes of bindings

	mu    sync.Mutex
	loops map[string]context.CancelFunc

	smu     sync.Mutex
	senders map[string]*senderState

	wake chan struct{}
	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

type senderState struct {
	active     bool
	times      []time.Time
	lastNotice time.Time
}

// NewConnectors returns the manager. It does nothing until Run or Sync is called.
func NewConnectors(svc *Service, o ConnectorOptions) (*Connectors, error) {
	if svc == nil || o.Store == nil || o.Transport == nil {
		return nil, errors.New("studio: the connector manager needs a service, a store and a transport")
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Tick <= 0 {
		o.Tick = 2 * time.Second
	}
	if o.Backoff <= 0 {
		o.Backoff = time.Second
	}
	if o.ConflictWait <= 0 {
		o.ConflictWait = 10 * time.Second
	}
	if o.RatePerMinute <= 0 {
		o.RatePerMinute = 20
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Connectors{svc: svc, o: o, log: o.Logger, loops: map[string]context.CancelFunc{}, senders: map[string]*senderState{},
		wake: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

// Notify asks Run to look at the bindings now.
func (c *Connectors) Notify() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Run starts and stops the loops of the bindings until ctx ends.
func (c *Connectors) Run(ctx context.Context) {
	tick := time.NewTicker(c.o.Tick)
	defer tick.Stop()
	defer c.stopAll()
	for {
		c.Sync(ctx)
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-tick.C:
		case <-c.wake:
		}
	}
}

// Close stops every loop. Replies that are being sent are given up.
func (c *Connectors) Close() {
	c.once.Do(func() { close(c.done) })
	c.stopAll()
	c.wg.Wait()
}

func (c *Connectors) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *Connectors) stopAll() {
	c.mu.Lock()
	for id, cancel := range c.loops {
		cancel()
		delete(c.loops, id)
	}
	c.mu.Unlock()
}

// Sync starts a loop for each enabled binding that has none, and stops the loops
// of bindings that are off or gone. A frontend that does not lead stops them all.
func (c *Connectors) Sync(ctx context.Context) {
	if c.closed() {
		return
	}
	if c.o.Lead != nil && !c.o.Lead(ctx) {
		c.stopAll()
		return
	}
	enabled, err := c.o.Store.EnabledBindings(ctx)
	if err != nil {
		c.log.Warn("studio: could not list the connector bindings", "error", err)
		return
	}
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		c.log.Warn("studio: could not list the connector kinds", "error", err)
		return
	}
	// A loop runs only for a binding whose add-on is running and pulls.
	want := map[string]connectors.Binding{}
	for _, b := range enabled {
		if k, ok := kindFor(kinds, b); ok && k.Delivery == contract.DeliveryPull {
			want[b.ID] = b
		}
	}
	c.mu.Lock()
	for id, cancel := range c.loops {
		if _, ok := want[id]; !ok {
			cancel()
			delete(c.loops, id)
		}
	}
	for id, b := range want {
		if _, ok := c.loops[id]; ok {
			continue
		}
		lctx, cancel := context.WithCancel(context.Background())
		c.loops[id] = cancel
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.poll(lctx, b.ID)
			// A loop that ended by itself (not stopped) leaves no entry, so
			// Sync can start it again.
			c.mu.Lock()
			if cur, ok := c.loops[b.ID]; ok && lctx.Err() == nil {
				_ = cur
				delete(c.loops, b.ID)
			}
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
}

// kindFor finds the running kind that serves a binding. A binding is served only
// by the add-on it names, so another add-on cannot take over its kind.
func kindFor(kinds []ConnectorKind, b connectors.Binding) (ConnectorKind, bool) {
	for _, k := range kinds {
		if k.ID == b.Kind && k.AddonID == b.AddonID {
			return k, true
		}
	}
	return ConnectorKind{}, false
}

// poll is the loop of one binding of a pull platform.
func (c *Connectors) poll(ctx context.Context, id string) {
	backoff := c.o.Backoff
	conflicts := 0
poll:
	for ctx.Err() == nil && !c.closed() {
		b, err := c.o.Store.GetBinding(ctx, id)
		if err != nil || !b.Enabled {
			return
		}
		token, err := c.openToken(ctx, b)
		if err != nil {
			c.disable(b, "its bot token cannot be read")
			return
		}
		offset, _, err := c.o.Store.Offset(ctx, id)
		if err != nil {
			c.log.Warn("studio: could not read a connector offset", "binding", id, "error", err)
			c.sleep(ctx, backoff)
			continue
		}
		resp, err := c.o.Transport.Call(ctx, b.AddonID, contract.ConnectorRequest{Op: contract.ConnectorOpPoll, Kind: b.Kind, Token: token, Offset: offset, TimeoutSeconds: c.o.Poll})
		if err == nil && resp.Error != nil {
			err = resp.Error
		}
		var ce *contract.ConnectorError
		switch {
		case ctx.Err() != nil:
			return
		case errors.As(err, &ce) && ce.Code == contract.ConnectorUnauthorized:
			c.disable(b, "the platform refused its bot token")
			return
		case errors.As(err, &ce) && ce.Code == contract.ConnectorConflict:
			if conflicts++; conflicts >= conflictTries {
				c.disable(b, "another process reads the updates of this bot")
				return
			}
			c.sleep(ctx, c.o.ConflictWait)
			continue
		case err != nil:
			wait := backoff
			if errors.As(err, &ce) && ce.Code == contract.ConnectorRateLimited {
				wait = time.Duration(ce.RetryAfter) * time.Second
				if ce.RetryAfter == 0 {
					wait = c.o.Backoff
				}
			} else {
				backoff = min(backoff*2, time.Minute)
				c.log.Warn("studio: a connector poll failed", "binding", id, "kind", b.Kind, "error", err)
			}
			c.sleep(ctx, wait)
			continue
		}
		backoff, conflicts = c.o.Backoff, 0
		for _, u := range resp.Updates {
			if ctx.Err() != nil {
				return
			}
			// The same function as the host operation: it reads the binding
			// again, so a changed allowlist, agent or token applies, and a
			// binding that was switched off ends the loop.
			if !u.Skip {
				err := c.Ingest(ctx, b.AddonID, contract.ConnectorInbound{BindingID: id, ConnectorMessage: u})
				switch {
				case errors.Is(err, connectors.ErrForbidden):
					return
				case errors.Is(err, connectors.ErrInvalid):
					// A malformed update can never succeed: log it and move past it.
					c.log.Warn("studio: a connector update was refused", "binding", id, "kind", b.Kind, "error", err)
				case err != nil:
					// The store failed: keep the offset, so the update is read again.
					c.log.Warn("studio: could not hand over a connector update", "binding", id, "error", err)
					c.sleep(ctx, backoff)
					continue poll
				}
			}
			if err := c.o.Store.SetOffset(ctx, id, u.Cursor+1); err != nil {
				c.log.Warn("studio: could not save a connector offset", "binding", id, "error", err)
			}
		}
		// The answer names where to go on, also when it held nothing.
		if resp.NextOffset > offset && (len(resp.Updates) == 0 || resp.NextOffset > resp.Updates[len(resp.Updates)-1].Cursor+1) {
			if err := c.o.Store.SetOffset(ctx, id, resp.NextOffset); err != nil {
				c.log.Warn("studio: could not save a connector offset", "binding", id, "error", err)
			}
		}
		if len(resp.Updates) == 0 && c.o.Poll == 0 {
			c.sleep(ctx, 5*time.Millisecond)
		}
	}
}

func (c *Connectors) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-c.done:
	case <-t.C:
	}
}

// disable switches a binding off and tells its owner why.
func (c *Connectors) disable(b connectors.Binding, why string) {
	c.wmu.Lock()
	cur, err := c.o.Store.GetBinding(context.Background(), b.ID)
	if err == nil && cur.Enabled {
		cur.Enabled, cur.Updated = false, c.o.Clock().UTC()
		err = c.o.Store.PutBinding(context.Background(), cur)
	}
	c.wmu.Unlock()
	if err != nil {
		c.log.Warn("studio: could not switch off a connector binding", "binding", b.ID, "error", err)
		return
	}
	if c.svc.d.Notify != nil {
		_, _ = c.svc.d.Notify.Add(context.Background(), notify.Notification{Principal: b.Owner, Kind: notify.KindNeedsAttention, Status: notify.StatusFailed,
			Title: b.Name + " was switched off", Body: "The connector was switched off because " + why + "."})
	}
}

func (c *Connectors) sender(binding, id string) *senderState {
	c.smu.Lock()
	defer c.smu.Unlock()
	k := binding + "\x00" + id
	s := c.senders[k]
	if s == nil {
		s = &senderState{}
		c.senders[k] = s
	}
	return s
}

// allow applies the rate limit of a sender. It returns whether the message may
// go on and whether the sender should be told.
func (c *Connectors) allow(s *senderState) (ok, notice bool) {
	now := c.o.Clock()
	c.smu.Lock()
	defer c.smu.Unlock()
	kept := s.times[:0]
	for _, t := range s.times {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	s.times = kept
	if len(s.times) >= c.o.RatePerMinute {
		if now.Sub(s.lastNotice) >= time.Minute || s.lastNotice.IsZero() {
			s.lastNotice = now
			return false, true
		}
		return false, false
	}
	s.times = append(s.times, now)
	return true, false
}

// say sends text to the chat of a platform through the add-on. The add-on cuts
// it to the limit of the platform.
func (c *Connectors) say(b connectors.Binding, token, chat, thread, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.send(ctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpSend, Token: token, Chat: chat, Thread: thread, Text: text}); err != nil {
		c.log.Warn("studio: could not send a connector message", "binding", b.ID, "kind", b.Kind, "error", err)
	}
}

// send makes one outbound call and turns an error of the platform into a Go error.
func (c *Connectors) send(ctx context.Context, b connectors.Binding, req contract.ConnectorRequest) error {
	req.Kind = b.Kind
	resp, err := c.o.Transport.Call(ctx, b.AddonID, req)
	if err == nil && resp.Error != nil {
		err = resp.Error
	}
	return err
}

// Ingest takes one message that a connector add-on brings for a binding. It is
// the one door of inbound messages, for the polling loop and for the host
// operation connector.inbound.
//
// The authority is the stored binding: the add-on must be the one the binding
// names and the binding must be on. The message cannot choose who runs the turn,
// because it is run as the owner of the binding, and it cannot skip the
// allowlist or the sender log.
func (c *Connectors) Ingest(ctx context.Context, addonID string, in contract.ConnectorInbound) error {
	b, err := c.o.Store.GetBinding(ctx, in.BindingID)
	if err != nil && !errors.Is(err, connectors.ErrNotFound) {
		// A store that fails is an outage, not a refusal: the caller may retry.
		return fmt.Errorf("studio: read a connector binding: %w", err)
	}
	// A missing binding and another add-on's binding look the same to the caller.
	if err != nil || b.AddonID != addonID || addonID == "" || !b.Enabled {
		return connectors.ErrForbidden
	}
	m := in.ConnectorMessage
	if m.Skip {
		return nil
	}
	if m.Chat == "" || m.SenderID == "" || len(m.SenderID) > contract.MaxConnectorSender || len(m.Chat) > contract.MaxConnectorSender ||
		len(m.Thread) > contract.MaxConnectorSender || len(m.Media) > contract.MaxConnectorMedia {
		return connectors.ErrInvalid
	}
	// The sender log keeps the name in a column of 255 characters.
	if r := []rune(m.SenderName); len(r) > 200 {
		m.SenderName = string(r[:200])
	}
	token, err := c.openToken(ctx, b)
	if err != nil {
		c.disable(b, "its bot token cannot be read")
		return connectors.ErrForbidden
	}
	c.handle(ctx, b, token, m)
	return nil
}

// conversation is the key of the chat that a message belongs to. A message in
// a thread of the platform belongs to the thread, so the people who write in
// one thread share one chat; any other message belongs to its sender. The key
// of a thread is a hash, so it fits the sender column of the stores and never
// equals the ID of a sender.
func conversation(m contract.ConnectorMessage) string {
	if m.Thread == "" {
		return m.SenderID
	}
	sum := sha256.Sum256([]byte(m.Chat + "\x00" + m.Thread))
	return "thread:" + hex.EncodeToString(sum[:16])
}

// handle takes one message of a sender.
func (c *Connectors) handle(ctx context.Context, b connectors.Binding, token string, m contract.ConnectorMessage) {
	senderID := m.SenderID
	// A message is answered when it is direct, or names the bot in a group.
	// An entry of the allowlist names the sender or the chat: a listed group
	// lets everyone in it talk to the bot.
	allowed := (m.Direct || m.Mention) && (b.AcceptAny || b.Allows(senderID, m.SenderUsername) || (!m.Direct && b.Allows(m.Chat, "")))
	name := m.SenderName
	if name == "" {
		name = m.SenderUsername
	}
	if err := c.o.Store.RecordSender(ctx, b.ID, senderID, name, allowed, c.o.Clock()); err != nil {
		c.log.Warn("studio: could not record a sender", "binding", b.ID, "error", err)
	}
	if !allowed {
		return // ignored: not answered, and in the sender log for the owner
	}
	say := func(text string) { c.say(b, token, m.Chat, m.Thread, text) }
	st := c.sender(b.ID, senderID)
	switch ok, notice := c.allow(st); {
	case !ok && notice:
		say(sentRate)
		return
	case !ok:
		return
	}
	switch {
	case m.Unsupported && len(m.Media) == 0:
		say(sentUnsupported)
		return
	case utf8.RuneCountInString(m.Text) > maxMessageRunes:
		say(sentTooLong)
		return
	}
	if c.svc.d.Users != nil {
		owner, err := c.svc.d.Users(ctx, b.Owner)
		if err != nil {
			if errors.Is(err, ErrNoUser) {
				c.disable(b, "its owner no longer exists")
			}
			say(sentUnavailable)
			return
		}
		if c.svc.d.Allowed != nil && !c.svc.d.Allowed(owner, auth.FeatureConnectors) {
			say(sentUnavailable)
			return
		}
	}
	key := conversation(m)
	chatID, err := c.chatFor(ctx, b, key, name)
	if err != nil {
		switch {
		case errors.Is(err, errAgentGone):
			c.disable(b, "its agent was deleted")
		case errors.Is(err, ErrNoModel):
			c.disable(b, "its agent has no model")
		default:
			c.log.Warn("studio: could not make the chat of a sender", "binding", b.ID, "error", err)
		}
		say(sentUnavailable)
		return
	}
	var up []session.Upload
	if len(m.Media) > 0 {
		switch {
		case !c.svc.d.Files.Available():
			say(sentNoFiles)
		default:
			if up = c.media(ctx, b, token, m.Media); len(up) < len(m.Media) {
				say(sentNoFile)
			}
		}
		if len(up) == 0 && strings.TrimSpace(m.Text) == "" {
			return
		}
	}
	// One reply goroutine serves a conversation; the rate limit stays the sender's.
	c.deliver(ctx, b, token, m, c.sender(b.ID, "\x01"+key), chatID, up)
}

// media reads the files of a message through the add-on. A file that cannot
// be read is left out; the caller tells the sender.
func (c *Connectors) media(ctx context.Context, b connectors.Binding, token string, refs []string) []session.Upload {
	var up []session.Upload
	for _, ref := range refs {
		mctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		resp, err := c.o.Transport.Call(mctx, b.AddonID, contract.ConnectorRequest{Op: contract.ConnectorOpMedia, Kind: b.Kind, Token: token, Media: ref})
		cancel()
		if err == nil && resp.Error != nil {
			err = resp.Error
		}
		f := resp.File
		if err != nil || f == nil || len(f.Data) == 0 || len(f.Data) > contract.MaxConnectorFileBytes {
			c.log.Warn("studio: could not read the file of a connector message", "binding", b.ID, "kind", b.Kind, "error", err)
			continue
		}
		up = append(up, session.Upload{Name: f.Name, Body: bytes.NewReader(f.Data)})
	}
	return up
}

var errAgentGone = errors.New("the agent was deleted")

// chatFor returns the chat of a conversation (a sender or a thread, see
// conversation), making it when there is none or when it ran with another
// agent than the binding has now.
func (c *Connectors) chatFor(ctx context.Context, b connectors.Binding, key, name string) (string, error) {
	if id, ok, err := c.o.Store.ChatOf(ctx, b.ID, key); err == nil && ok {
		if rec, err := c.svc.Get(ctx, b.Owner, id); err == nil && rec.AgentID == b.AgentID {
			return id, nil
		}
	}
	title := c.kindTitle(ctx, b) + " · " + name
	rec, err := c.svc.CreateAs(ctx, b.Owner, CreateInput{Title: title, AgentID: b.AgentID}, Origin{Kind: session.OriginConnector, ID: b.ID})
	if err != nil {
		if isAgentMissing(err) {
			return "", errAgentGone
		}
		return "", err
	}
	if err := c.o.Store.SetChat(ctx, b.ID, key, rec.ID); err != nil {
		return "", err
	}
	return rec.ID, nil
}

// deliver hands the message to the chat and makes sure that the answer goes
// back. One reply goroutine serves a conversation at a time: it sends the
// answer of each turn until the chat is idle.
func (c *Connectors) deliver(ctx context.Context, b connectors.Binding, token string, m contract.ConnectorMessage, st *senderState, chatID string, up []session.Upload) {
	c.smu.Lock()
	was := st.active
	st.active = true
	c.smu.Unlock()
	var events <-chan session.Event
	var cancel func()
	if !was {
		var err error
		if events, cancel, err = c.svc.Events(ctx, b.Owner, chatID); err != nil {
			c.smu.Lock()
			st.active = false
			c.smu.Unlock()
			c.say(b, token, m.Chat, m.Thread, sentUnavailable)
			return
		}
	}
	if _, err := c.svc.SendWithFiles(ctx, b.Owner, chatID, m.Text, up); err != nil {
		if !was {
			cancel()
			c.smu.Lock()
			st.active = false
			c.smu.Unlock()
		}
		c.say(b, token, m.Chat, m.Thread, sentFailed)
		return
	}
	if was {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer cancel()
		c.reply(b, token, m, st, chatID, events)
	}()
}

// reply waits for the end of each turn of the chat and sends its answer.
func (c *Connectors) reply(b connectors.Binding, token string, m contract.ConnectorMessage, st *senderState, chatID string, events <-chan session.Event) {
	typing := time.NewTicker(4 * time.Second)
	defer typing.Stop()
	var poll <-chan time.Time
	errText := ""
	for {
		ended := false
		select {
		case ev, ok := <-events:
			if !ok {
				events = nil
				p := time.NewTicker(time.Second)
				defer p.Stop()
				poll = p.C
				continue
			}
			switch ev.Kind {
			case session.KindError:
				errText = ev.Text
			case session.KindTurnEnd:
				ended = true
			}
		case <-poll:
			if info, err := c.svc.Turn(context.Background(), b.Owner, chatID); err == nil && !info.Running && info.Last != "" {
				ended = true
			}
		case <-typing.C:
			tctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = c.send(tctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpTyping, Token: token, Chat: m.Chat, Thread: m.Thread})
			cancel()
		case <-c.done:
			return
		}
		if !ended {
			continue
		}
		c.say(b, token, m.Chat, m.Thread, c.answerOf(b, chatID, errText))
		errText = ""
		c.smu.Lock()
		info, err := c.svc.Turn(context.Background(), b.Owner, chatID)
		if err == nil && info.Running {
			c.smu.Unlock()
			continue // a waiting message started the next turn
		}
		st.active = false
		c.smu.Unlock()
		return
	}
}

// answerOf is what the sender reads for the turn that just ended.
func (c *Connectors) answerOf(b connectors.Binding, chatID, errText string) string {
	ctx := context.Background()
	var info store.TurnInfo
	for i := 0; i < 250; i++ { // the event comes before the store records the outcome
		var err error
		if info, err = c.svc.Turn(ctx, b.Owner, chatID); err != nil || !info.Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	switch info.Last {
	case store.TurnStopped:
		return sentStopped
	case store.TurnInterrupted:
		return sentInterrupted
	case store.TurnFailed:
		if errText == "" {
			return sentFailed
		}
		return errText
	}
	if t := strings.TrimSpace(c.svc.finalText(ctx, chatID)); t != "" {
		return t
	}
	return sentNoAnswer
}

func isAgentMissing(err error) bool {
	return err != nil && strings.Contains(err.Error(), "agents: not found")
}

// Forward sends a notification of a user to the chat of each enabled binding of
// that user that has one. The title and the text go, never the link. The inbox
// calls it on the frontend that stored the notification, so a notification is
// sent once. It does not wait, and a failure is only logged.
func (c *Connectors) Forward(ctx context.Context, n notify.Notification) {
	if c.closed() {
		return
	}
	bindings, err := c.o.Store.ListBindings(ctx, n.Principal)
	if err != nil {
		c.log.Warn("studio: could not list the bindings to forward a notification", "error", err)
		return
	}
	text := strings.TrimSpace(n.Title + "\n" + n.Body)
	for _, b := range bindings {
		if !b.Enabled || b.NotifyChat == "" {
			continue
		}
		token, err := c.openToken(ctx, b)
		if err != nil {
			c.log.Warn("studio: could not read a bot token to forward a notification", "binding", b.ID)
			continue
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.say(b, token, b.NotifyChat, "", text)
		}()
	}
}
