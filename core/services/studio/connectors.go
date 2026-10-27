package studio

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
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

	conflictTries    = 6
	maxMessageRunes  = 8000
	telegramMaxRunes = 4096
)

// ConnectorOptions configures the connector manager.
type ConnectorOptions struct {
	// Store holds bindings and the state of their loops. Required.
	Store connectors.Store
	// Telegram speaks the Bot API; nil means the real one.
	Telegram *connectors.Telegram
	Clock    func() time.Time
	// Tick is how often the manager looks for bindings to start or stop; zero
	// means 2 seconds.
	Tick time.Duration
	// Poll is the long-poll time of a request, in seconds. Zero means no wait,
	// which only a test wants; the application sets 25.
	Poll int
	// Backoff is the first wait after an error; it doubles up to a minute. Zero
	// means one second.
	Backoff time.Duration
	// ConflictWait is how long to wait after Telegram says another process reads
	// the updates of the bot; zero means 10 seconds. A new leader meets this answer
	// while the long poll of the old one is still open at Telegram, so the binding
	// is only switched off after several answers in a row.
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
// State mode: shared. Bindings, offsets, the sender log and the chat of each
// sender are in the store; the polling loops run on one frontend at a time, the
// leader, and a new leader continues from the stored offset.
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
	if svc == nil || o.Store == nil {
		return nil, errors.New("studio: the connector manager needs a service and a store")
	}
	if o.Telegram == nil {
		o.Telegram = &connectors.Telegram{}
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
	want := map[string]connectors.Binding{}
	for _, b := range enabled {
		want[b.ID] = b
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

// poll is the loop of one binding.
func (c *Connectors) poll(ctx context.Context, id string) {
	backoff := c.o.Backoff
	conflicts := 0
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
		ups, err := c.o.Telegram.Updates(ctx, token, offset, c.o.Poll)
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, connectors.ErrUnauthorized):
			c.disable(b, "Telegram refused its bot token")
			return
		case errors.Is(err, connectors.ErrConflict):
			if conflicts++; conflicts >= conflictTries {
				c.disable(b, "another process reads the updates of this bot")
				return
			}
			c.sleep(ctx, c.o.ConflictWait)
			continue
		case err != nil:
			var ra *connectors.RetryAfter
			wait := backoff
			if errors.As(err, &ra) {
				wait = time.Duration(ra.Seconds) * time.Second
				if ra.Seconds == 0 {
					wait = c.o.Backoff
				}
			} else {
				backoff = min(backoff*2, time.Minute)
				c.log.Warn("studio: a Telegram poll failed", "binding", id, "error", err)
			}
			c.sleep(ctx, wait)
			continue
		}
		backoff, conflicts = c.o.Backoff, 0
		for _, u := range ups {
			if ctx.Err() != nil {
				return
			}
			// Read the binding again: its allowlist, agent or token may have changed.
			if cur, err := c.o.Store.GetBinding(ctx, id); err != nil || !cur.Enabled {
				return
			} else {
				b = cur
			}
			if t, err := c.openToken(ctx, b); err == nil {
				token = t
			}
			if !u.Skip {
				c.handle(ctx, b, token, u)
			}
			if err := c.o.Store.SetOffset(ctx, id, u.ID+1); err != nil {
				c.log.Warn("studio: could not save a connector offset", "binding", id, "error", err)
			}
		}
		if len(ups) == 0 && c.o.Poll == 0 {
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
			Title: b.Name + " was switched off", Body: "The Telegram connector was switched off because " + why + "."})
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

func (c *Connectors) say(token string, chatID int64, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, part := range connectors.Split(text, telegramMaxRunes) {
		err := c.o.Telegram.Send(ctx, token, chatID, part)
		var ra *connectors.RetryAfter
		if errors.As(err, &ra) {
			c.sleep(ctx, time.Duration(ra.Seconds)*time.Second)
			err = c.o.Telegram.Send(ctx, token, chatID, part)
		}
		if err != nil {
			c.log.Warn("studio: could not send a Telegram message", "error", err)
			return
		}
	}
}

// handle takes one message of a sender.
func (c *Connectors) handle(ctx context.Context, b connectors.Binding, token string, u connectors.Update) {
	senderID := strconv.FormatInt(u.FromID, 10)
	allowed := u.ChatType == "private" && (b.AcceptAny || b.Allows(senderID, u.FromUsername))
	name := u.FromName
	if name == "" {
		name = u.FromUsername
	}
	if err := c.o.Store.RecordSender(ctx, b.ID, senderID, name, allowed, c.o.Clock()); err != nil {
		c.log.Warn("studio: could not record a sender", "binding", b.ID, "error", err)
	}
	if !allowed {
		return // ignored: not answered, and in the sender log for the owner
	}
	st := c.sender(b.ID, senderID)
	switch ok, notice := c.allow(st); {
	case !ok && notice:
		c.say(token, u.ChatID, sentRate)
		return
	case !ok:
		return
	}
	switch {
	case u.Unsupported:
		c.say(token, u.ChatID, sentUnsupported)
		return
	case utf8.RuneCountInString(u.Text) > maxMessageRunes:
		c.say(token, u.ChatID, sentTooLong)
		return
	}
	if c.svc.d.Users != nil {
		if _, err := c.svc.d.Users(ctx, b.Owner); err != nil {
			if errors.Is(err, ErrNoUser) {
				c.disable(b, "its owner no longer exists")
			}
			c.say(token, u.ChatID, sentUnavailable)
			return
		}
	}
	chatID, err := c.chatFor(ctx, b, senderID, name)
	if err != nil {
		switch {
		case errors.Is(err, errAgentGone):
			c.disable(b, "its agent was deleted")
		case errors.Is(err, ErrNoModel):
			c.disable(b, "its agent has no model")
		default:
			c.log.Warn("studio: could not make the chat of a sender", "binding", b.ID, "error", err)
		}
		c.say(token, u.ChatID, sentUnavailable)
		return
	}
	c.deliver(ctx, b, token, u.ChatID, st, chatID, u.Text)
}

var errAgentGone = errors.New("the agent was deleted")

// chatFor returns the chat of a sender, making it when there is none or when it
// ran with another agent than the binding has now.
func (c *Connectors) chatFor(ctx context.Context, b connectors.Binding, senderID, name string) (string, error) {
	if id, ok, err := c.o.Store.ChatOf(ctx, b.ID, senderID); err == nil && ok {
		if rec, err := c.svc.Get(ctx, b.Owner, id); err == nil && rec.AgentID == b.AgentID {
			return id, nil
		}
	}
	title := "Telegram · " + name
	rec, err := c.svc.CreateAs(ctx, b.Owner, CreateInput{Title: title, AgentID: b.AgentID}, Origin{Kind: session.OriginConnector, ID: b.ID})
	if err != nil {
		if isAgentMissing(err) {
			return "", errAgentGone
		}
		return "", err
	}
	if err := c.o.Store.SetChat(ctx, b.ID, senderID, rec.ID); err != nil {
		return "", err
	}
	return rec.ID, nil
}

// deliver hands the message to the chat and makes sure that the answer goes
// back. One reply goroutine serves a sender at a time: it sends the answer of
// each turn until the chat is idle.
func (c *Connectors) deliver(ctx context.Context, b connectors.Binding, token string, tgChat int64, st *senderState, chatID, text string) {
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
			c.say(token, tgChat, sentUnavailable)
			return
		}
	}
	if _, err := c.svc.Send(ctx, b.Owner, chatID, text); err != nil {
		if !was {
			cancel()
			c.smu.Lock()
			st.active = false
			c.smu.Unlock()
		}
		c.say(token, tgChat, sentFailed)
		return
	}
	if was {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer cancel()
		c.reply(b, token, tgChat, st, chatID, events)
	}()
}

// reply waits for the end of each turn of the chat and sends its answer.
func (c *Connectors) reply(b connectors.Binding, token string, tgChat int64, st *senderState, chatID string, events <-chan session.Event) {
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
			_ = c.o.Telegram.Typing(tctx, token, tgChat)
			cancel()
		case <-c.done:
			return
		}
		if !ended {
			continue
		}
		c.say(token, tgChat, c.answerOf(b, chatID, errText))
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

// Forward sends a notification of a user to the Telegram chat of each enabled
// binding of that user that has one. The title and the text go, never the link.
// The inbox calls it on the frontend that stored the notification, so a
// notification is sent once. It does not wait, and a failure is only logged.
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
		chat, perr := strconv.ParseInt(b.NotifyChat, 10, 64)
		if !b.Enabled || b.NotifyChat == "" || perr != nil {
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
			c.say(token, chat, text)
		}()
	}
}
