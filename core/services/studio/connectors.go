package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/singleflight"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/media"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/oauthtoken"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The sentences the bot says are keys of the host message catalog (see
// hostmsg and msg). They name no internal error.
const (
	// voiceMark starts the transcript of a voice message in the text of a
	// turn, so the agent knows that the sender spoke and did not write.
	voiceMark = "[Voice message, transcribed]"
	// transcribeTimeout bounds the transcription of one voice message.
	transcribeTimeout = 2 * time.Minute

	conflictTries   = 6
	maxMessageRunes = 8000
)

// ConnectorKind is a kind of binding that a running connector add-on serves.
type ConnectorKind = connectors.Kind

// ConnectorTransport reaches the connector add-ons. The application implements
// it over the add-on manager; a test implements it with a script.
type ConnectorTransport interface {
	// Kinds lists the kinds that running add-ons serve now. It is empty when the
	// process does not run add-ons.
	Kinds(ctx context.Context) ([]ConnectorKind, error)
	// Call sends one request to the connector route of an add-on.
	Call(ctx context.Context, scope ConnectorCallScope, req contract.ConnectorRequest) (contract.ConnectorResponse, error)
}

// ConnectorOptions configures the connector manager.
type ConnectorOptions struct {
	// Store holds bindings and the state of their loops. Required.
	Store connectors.Store
	// Transport reaches the connector add-ons. Required.
	Transport ConnectorTransport
	Clock     func() time.Time
	// LeaseWait pauses a caller that lost the refresh lease of an OAuth grant
	// before it looks for the token the winner writes. Nil sleeps for real;
	// a test passes a hook that does not.
	LeaseWait func(ctx context.Context, d time.Duration) error
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
	Lead func(ctx context.Context) bool
	// EditEvery is the shortest time between two previews (edits of the
	// working messages, or drafts) of one binding, when the kind of the
	// binding declares none; zero means one second. Platforms limit how often
	// a bot may write, and a preview counts.
	EditEvery time.Duration
	// DraftHeartbeat is how often a draft that shows nothing new is sent
	// again; zero means 25 seconds.
	DraftHeartbeat time.Duration
	// VerifyToken checks a bot token that a binding is saved with, before it
	// is stored. Nil: tokens are not checked. The application sets
	// VerifyTokenWithMe; a test that fakes no platform leaves it nil.
	VerifyToken func(ctx context.Context, b connectors.Binding, token string) error
	// FileCeiling reads the host ceiling of a connector file from the owner
	// policy, in bytes. Nil or zero: contract.DefaultFileCeilingBytes.
	FileCeiling func(ctx context.Context) (int64, error)
	// OwnerLanguage returns the interface language of an owner (a tag such as
	// "it" or "pt-BR"), or "" when the server does not know it. It is asked
	// for a connection whose reply language is auto and that holds no language
	// of its own (Binding.UILanguage). Nil: not known.
	OwnerLanguage func(ctx context.Context, owner string) string
	// OutboxPoll is how often a queued message looks again for room under
	// the limit of its binding; zero means one second.
	OutboxPoll time.Duration
	// OAuthDescriptor returns the OAuth descriptor that the credential slot of
	// an installed add-on declares now. Nil: this process cannot run OAuth
	// logins or refreshes, and an OAuth binding is unavailable here.
	OAuthDescriptor func(ctx context.Context, addonID string) (contract.OAuthDescriptor, error)
	// TokenClient calls the token endpoints. Nil: oauthtoken.New.
	TokenClient *oauthtoken.Client
	// InstanceID names this process in the lease of a grant. Empty: a
	// random name.
	InstanceID string
	// Shares is the sharing policy of the bindings (see ConnectionShares).
	// Nil: no binding is shared.
	Shares ConnectionShares
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

	// afterSettle runs in the reply after each check whether to give the chat
	// up, with the outcome. Only the specs set it.
	afterSettle func(settled bool)

	// beforeJoin runs in deliver after a message has joined a running reply
	// and before it reaches the chat. Only the specs set it, to hold a
	// message in that gap.
	beforeJoin func()

	sealMu sync.Mutex
	sealer *connectors.Sealer

	// refreshes runs one refresh per grant and version in this process; the
	// lease in the grant row does the same across processes.
	refreshes    singleflight.Group
	instance     string
	defaultToken *oauthtoken.Client
	wmu          sync.Mutex // serialises writes of bindings
	// logins counts the login starts of each person in this process (see
	// allowLoginStart).
	loginMu sync.Mutex
	logins  map[string]loginWindow

	mu    sync.Mutex // guards loops and shut, and orders wg.Add before Close's Wait
	loops map[string]context.CancelFunc
	shut  bool // Close has begun: no goroutine may be added to wg

	smu     sync.Mutex
	senders map[string]*senderState

	amu2         sync.Mutex
	accountTried map[string]time.Time // by binding: when this process last asked its add-on for its account

	emu      sync.Mutex
	nextEdit map[string]time.Time // by binding: when its next edit may go

	outbox outboxes // text the host writes on its own, by binding

	lang langMemory // the last clear language of each sender and chat

	gmu   sync.Mutex
	gates []CapabilitySwitch // extra switches of a capability (see AddCapabilitySwitch)

	amu      sync.Mutex
	attached map[string][]string // by chat: files that attach_file marked for the answer of the turn

	wake chan struct{}
	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

type senderState struct {
	active     bool
	times      []time.Time
	lastNotice time.Time
	// failures are the times of wrong pairing codes; lockedUntil ends a
	// lockout after too many.
	failures    []time.Time
	lockedUntil time.Time
	// grant is what the senders of the running turn of a conversation may
	// do: the intersection over the messages the turn took. Nil while no turn
	// runs.
	grant *turnGrant
	// joining counts the messages that found a reply running and are on their
	// way to the chat. The reply may not give the chat up while one is: the
	// turn it starts would have no reply to carry its answer.
	joining int
}

// NewConnectors returns the manager. It does nothing until Run or Sync is called.
func NewConnectors(svc *Service, o ConnectorOptions) (*Connectors, error) {
	if svc == nil || o.Store == nil || o.Transport == nil {
		return nil, errors.New("studio: the connector manager needs a service, a store and a transport")
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.LeaseWait == nil {
		o.LeaseWait = sleep
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
	if o.EditEvery <= 0 {
		o.EditEvery = time.Second
	}
	if o.DraftHeartbeat <= 0 {
		o.DraftHeartbeat = draftHeartbeat
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.InstanceID == "" {
		o.InstanceID = newID()
	}
	return &Connectors{svc: svc, o: o, log: o.Logger, instance: o.InstanceID, defaultToken: oauthtoken.New(), logins: map[string]loginWindow{}, loops: map[string]context.CancelFunc{}, senders: map[string]*senderState{}, nextEdit: map[string]time.Time{},
		wake: make(chan struct{}, 1), done: make(chan struct{}), gates: []CapabilitySwitch{agentGrantSwitch(svc), toolsSwitch(svc), knowledgeSwitch(svc), resourcesSwitch(svc)}}, nil
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
	// Set shut under mu: a goroutine is added to wg only with mu held and shut
	// false, so no Add can run once Wait starts.
	c.mu.Lock()
	c.shut = true
	c.mu.Unlock()
	c.once.Do(func() { close(c.done) })
	c.stopAll()
	c.wg.Wait()
}

// goLocked runs fn in a goroutine that Close waits for. The caller holds c.mu.
// It reports false, and runs nothing, once Close has begun.
func (c *Connectors) goLocked(fn func()) bool {
	if c.shut {
		return false
	}
	c.wg.Go(func() {
		fn()
	})
	return true
}

// spawn is goLocked for a caller that does not hold c.mu.
func (c *Connectors) spawn(fn func()) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.goLocked(fn)
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
		// A binding without an agent has nothing to answer with: no loop.
		if b.CredentialsOnly() {
			continue
		}
		// A tool-only kind never listens, whatever an older row says.
		if k, ok := kindFor(kinds, b); ok && !k.ToolOnly && k.Delivery == contract.DeliveryPull {
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
		started := c.goLocked(func() {
			c.poll(lctx, b.ID)
			// A loop that ended by itself (not stopped) leaves no entry, so
			// Sync can start it again.
			c.mu.Lock()
			if cur, ok := c.loops[b.ID]; ok && lctx.Err() == nil {
				_ = cur
				delete(c.loops, b.ID)
			}
			c.mu.Unlock()
		})
		if started {
			c.loops[id] = cancel
		} else {
			cancel()
		}
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
		if err != nil || !b.Enabled || b.CredentialsOnly() {
			return
		}
		token, err := c.openToken(ctx, b)
		if err != nil {
			c.disable(b, "its bot token cannot be read")
			return
		}
		// A binding whose add-on told no account yet is asked again every so often.
		b = c.learnAccount(ctx, b, token)
		offset, _, err := c.o.Store.Offset(ctx, id)
		if err != nil {
			c.log.Warn("studio: could not read a connector offset", "binding", id, "error", err)
			c.sleep(ctx, backoff)
			continue
		}
		resp, err := c.o.Transport.Call(ctx, BindingConnectorScope(b), contract.ConnectorRequest{Op: contract.ConnectorOpPoll, Kind: b.Kind, Token: token, Offset: offset, TimeoutSeconds: c.o.Poll, Options: c.optionsOf(ctx, b)})
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

// optionsOf is the value in effect of each option that the kind of the
// binding declares: the owner's choice, or the default. A choice that the
// kind no longer offers is replaced by the default.
func (c *Connectors) optionsOf(ctx context.Context, b connectors.Binding) map[string]string {
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return nil
	}
	k, ok := kindFor(kinds, b)
	if !ok || len(k.Options) == 0 {
		return nil
	}
	if out, err := k.ResolveOptions(b.Options); err == nil {
		return out
	}
	out, _ := k.ResolveOptions(nil)
	return out
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
		_, _ = c.svc.d.Notify.Add(context.Background(), notify.Notification{Principal: b.Owner, Kind: notify.KindNeedsAttention, Status: notify.StatusFailed, Source: notify.SourceConnector,
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
	resp, err := c.o.Transport.Call(ctx, BindingConnectorScope(b), req)
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
	if err != nil || b.AddonID != addonID || addonID == "" || !b.Enabled || b.CredentialsOnly() {
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
	// The add-on owns the roles of a sender; the host keeps the ones that the
	// kind declares and drops the rest.
	m.Roles = c.declaredRoles(ctx, b, m.Roles)
	token, err := c.openToken(ctx, b)
	if err != nil {
		c.disable(b, "its bot token cannot be read")
		return connectors.ErrForbidden
	}
	// A push platform has no loop that learns the account, so it is learned here.
	b = c.learnAccount(ctx, b, token)
	// Two connections of one bot get the same message: the oldest answers.
	if older, twin := c.olderTwin(ctx, b, m); twin {
		c.log.Info("studio: a connection stays silent because an older connection of the same bot answers", "binding", b.ID, "answering", older.ID, "kind", b.Kind, "message", m.MessageID)
		return nil
	}
	c.handle(ctx, b, token, m)
	return nil
}

// declaredRoles keeps the role IDs that the kind of the binding declares. A
// kind that is not running, or declares no roles, keeps none.
func (c *Connectors) declaredRoles(ctx context.Context, b connectors.Binding, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return nil
	}
	k, ok := kindFor(kinds, b)
	if !ok {
		return nil
	}
	return k.FilterRoles(ids)
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
	return threadKey(m.Chat, m.Thread)
}

// hashKey is a key of the chat map that fits the sender column and never
// equals the ID of a sender.
func hashKey(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(sum[:16])
}

func threadKey(chat, thread string) string { return hashKey("thread:", chat, thread) }

// replyKey keeps the chat of an answer message of the bot, so that a reply
// to it goes on in that chat (ConversationThread).
func replyKey(chat, msg string) string { return hashKey("reply:", chat, msg) }

// conversationOf is the key of the chat that a message belongs to, by the
// conversation mode of the binding.
func (c *Connectors) conversationOf(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage) string {
	switch b.ConversationMode() {
	case connectors.ConversationChat:
		if m.Thread != "" {
			return threadKey(m.Chat, m.Thread)
		}
		return hashKey("chat:", m.Chat)
	case connectors.ConversationThread:
		known := func(k string) bool {
			_, ok, err := c.o.Store.ChatOf(ctx, b.ID, k)
			return err == nil && ok
		}
		// A reply to an answer of the bot, or a thread that one starts, goes
		// on in the chat of that answer.
		for _, ref := range []string{m.ReplyTo, m.Thread} {
			if k := replyKey(m.Chat, ref); ref != "" && known(k) {
				return k
			}
		}
		// A reply to a message that started a chat goes on in it.
		if k := threadKey(m.Chat, m.ReplyTo); m.ReplyTo != "" && known(k) {
			return k
		}
		if m.Thread != "" {
			return threadKey(m.Chat, m.Thread)
		}
		if m.MessageID != "" {
			return threadKey(m.Chat, m.MessageID)
		}
		// A platform that names no message: every message is a new chat.
		return hashKey("msg:", m.Chat, m.SenderID, time.Now().String())
	}
	return conversation(m)
}

// windowEnded tells whether a message of a sender starts a new chat in the
// time window mode: the sender's previous message is WindowMinutes old or
// older. A thread of the platform keeps its chat.
func (c *Connectors) windowEnded(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage) bool {
	if b.ConversationMode() != connectors.ConversationWindow || m.Thread != "" {
		return false
	}
	senders, err := c.o.Store.Senders(ctx, b.ID, 0)
	if err != nil {
		return false
	}
	for _, s := range senders {
		if s.ID == m.SenderID {
			return c.o.Clock().Sub(s.Last) >= time.Duration(b.WindowMinutes)*time.Minute
		}
	}
	return false
}

// handle takes one message of a sender.
func (c *Connectors) handle(ctx context.Context, b connectors.Binding, token string, m contract.ConnectorMessage) {
	if b.CredentialsOnly() {
		return // Ingest refuses it first; this keeps any other caller from answering
	}
	senderID := m.SenderID
	// A message is answered when it is direct, or names the bot in a group.
	// An entry of the allowlist names the sender or the chat: a listed group
	// lets everyone in it talk to the bot.
	allowed := (m.Direct || m.Mention) && (b.AcceptAny || b.Allows(senderID, m.SenderUsername) || (!m.Direct && b.Allows(m.Chat, "")))
	name := m.SenderName
	if name == "" {
		name = m.SenderUsername
	}
	// Read before the message is recorded: the window counts from the
	// previous one.
	fresh := allowed && c.windowEnded(ctx, b, m)
	if err := c.o.Store.RecordSender(ctx, b.ID, senderID, name, allowed, c.o.Clock()); err != nil {
		c.log.Warn("studio: could not record a sender", "binding", b.ID, "error", err)
	}
	if ns, ok := c.o.Store.(connectors.SenderNoter); ok {
		note := connectors.SenderNote{Chat: m.Chat, ChatName: m.ChatName}
		switch {
		case allowed:
		case !m.Direct && !m.Mention:
			note.Reason = connectors.ReasonNotAddressed
		default:
			note.Reason = connectors.ReasonNotListed
		}
		if err := ns.NoteSender(ctx, b.ID, senderID, note); err != nil {
			c.log.Warn("studio: could not note a sender", "binding", b.ID, "error", err)
		}
	}
	if !allowed {
		return // ignored: not answered, and in the sender log for the owner
	}
	c.noteLanguage(b, m)
	v := c.voice(ctx, b, senderID, m.Chat)
	say := func(text string) { c.say(b, token, m.Chat, m.Thread, text) }
	// What the host says to the sender about a message is also noted in the
	// chat of that conversation, when there is one, so the owner reads why a
	// message went unanswered.
	tell := func(key string, args ...any) {
		text := v.T(key, args...)
		say(text)
		if code, ok := hostNoteCodes[key]; ok {
			c.noteChat(ctx, b, m, code, text)
		}
	}
	st := c.sender(b.ID, senderID)
	switch ok, notice := c.allow(st); {
	case !ok && notice:
		tell("rate")
		return
	case !ok:
		return
	}
	// A pairing code is checked before any model turn and never reaches the
	// model or the log.
	if c.tryPairing(ctx, b, m, st, tell) {
		return
	}
	switch {
	case m.Unsupported && len(m.Media) == 0:
		tell("unsupported")
		return
	case utf8.RuneCountInString(m.Text) > maxMessageRunes:
		tell("too_long")
		return
	}
	if c.svc.d.Users != nil {
		owner, err := c.svc.d.Users(ctx, b.Owner)
		if err != nil {
			if errors.Is(err, ErrNoUser) {
				c.disable(b, "its owner no longer exists")
			}
			tell("unavailable")
			return
		}
		if c.svc.d.Allowed != nil && !c.svc.d.Allowed(owner, auth.FeatureConnectors) {
			tell("unavailable")
			return
		}
	}
	// The new chat command is decided here too, and the model never reads it.
	if isNewCommand(m) {
		c.newChat(ctx, b, m, tell)
		return
	}
	// "confirm 7K2Q" or "cancel 7K2Q" answers a proposal that this binding made;
	// it is decided here, before any model turn, and the model never reads it.
	if len(m.Media) == 0 && c.answerPhrase(ctx, b, m, say, v) {
		return
	}
	// A scoped agent whose folder is missing does not run, on any frontend
	// or worker: one plain reply, and the binding stays on, so it works
	// again once the folder is back.
	if _, err := c.svc.agentScope(ctx, b.Owner, b.AgentID); errors.Is(err, ErrAgentFolderMissing) {
		c.log.Warn("studio: a connector message was refused: the agent's folder is missing", "binding", b.ID)
		tell("no_folder")
		return
	}
	// The files are read before the chat is made: a message that cannot
	// start a turn leaves no empty chat. A sender without the right to send
	// files gets a plain refusal and no turn.
	var up []session.Upload
	if len(m.Media) > 0 {
		if d := c.Authorize(ctx, b, m, contract.CapFilesUpload); !d.Allowed {
			tell(refusalKey(b, m, d, "no_upload"))
			return
		}
		var ok bool
		if m.Text, up, ok = c.inputs(ctx, b, token, m, say, v); !ok {
			return
		}
	}
	key := c.conversationOf(ctx, b, m)
	chatID, created, err := c.chatFor(ctx, b, key, name, fresh)
	if err != nil {
		switch {
		case errors.Is(err, errAgentGone):
			c.disable(b, "its agent was deleted")
		case errors.Is(err, ErrNoModel):
			c.disable(b, "its agent has no model")
		case errors.Is(err, ErrAgentFolderLimit):
			c.log.Warn("studio: a connector message exceeds the folder limit", "binding", b.ID)
			tell("folder_limit")
			return
		case errors.Is(err, ErrAgentFolderMissing):
			c.log.Warn("studio: a connector message was refused: the agent's folder is missing", "binding", b.ID)
			tell("no_folder")
			return
		default:
			c.log.Warn("studio: could not make the chat of a sender", "binding", b.ID, "error", err)
		}
		tell("unavailable")
		return
	}
	c.rememberPlace(ctx, b, chatID, m)
	c.rememberInfo(ctx, b, chatID, m)
	// One reply goroutine serves a conversation; the rate limit stays the sender's.
	// The earlier messages of a thread go to the first turn of a chat that
	// the thread just started, once.
	var earlier string
	who := turnWho{Sender: c.senderFor(b, m)}
	if created {
		var st threadStats
		earlier, st = c.threadDirective(ctx, b, token, m)
		if earlier != "" {
			who.Thread, who.ThreadLines, who.ThreadCut = earlier, st.Lines, st.Truncated
		}
	}
	ctx = withTurnWho(ctx, who)
	c.deliver(ctx, b, token, m, c.sender(b.ID, "\x01"+key), chatID, up, earlier)
}

// refusalKey picks the sentence a sender reads for a refused capability, so
// the reply says why in one plain sentence and not only "not allowed": the
// channel is set to answer only, the sender is not on the list, the action
// needs a pairing, or the owner has not turned it on. fallback is the sentence
// of the feature, for a reason the sender cannot act on.
func refusalKey(b connectors.Binding, m contract.ConnectorMessage, d Decision, fallback string) string {
	switch d.Code {
	case CodeChannelNotTrusted:
		if !m.Direct && b.Allows(m.Chat, "") {
			return "refuse.channel"
		}
		return "refuse.unlisted"
	case CodeSenderNeedsPairing:
		return "refuse.pairing"
	case CodeConnectionPermissionOff, CodeAgentPermissionOff, CodeToolsSwitchOff:
		return "refuse.off"
	}
	return fallback
}

// hostNoteCodes maps the sentence keys of the host's own messages to the
// code of the note the chat keeps (session.RoleHostNote). A sentence that has
// no code here is told to the sender and not noted.
var hostNoteCodes = map[string]string{
	"unsupported":  session.HostNoteUnsupported,
	"too_long":     session.HostNoteTooLong,
	"no_upload":    session.HostNoteNoUpload,
	"rate":         session.HostNoteRate,
	"pairing.done": session.HostNotePairing,
	"no_folder":    session.HostNoteRefused,
	"folder_limit": session.HostNoteRefused,
	"no_files":     session.HostNoteRefused,
	// The reasons a capability was refused (see refusalKey): the sender read
	// one sentence, and so does the owner.
	"refuse.channel":  session.HostNoteRefused,
	"refuse.unlisted": session.HostNoteRefused,
	"refuse.pairing":  session.HostNoteRefused,
	"refuse.off":      session.HostNoteRefused,
}

// inputs turns the files of a message into what the turn gets: the uploads
// of the chat, as if the owner had attached them in the chat, and the text.
// An audio file is a voice message: the transcription model of the
// instance, the one the chats use for voice notes, makes it text, which goes
// in the text under voiceMark; the recording is not kept. The sender is told
// about a file that could not be read or was too large. It reports false when
// the message must not start a turn: nothing is left to send, or a voice
// message cannot be transcribed.
func (c *Connectors) inputs(ctx context.Context, b connectors.Binding, token string, m contract.ConnectorMessage, say func(string), v hostmsg.Voice) (string, []session.Upload, bool) {
	got, note := c.media(ctx, b, token, m.Media, v)
	if note != "" {
		say(note)
	}
	var (
		up       []session.Upload
		voices   []string
		filesOff bool
	)
	for _, f := range got {
		if media.IsAudio(f.Name) {
			t, refusal := c.transcribe(ctx, b, f.Name, f.body(c), v)
			if refusal != "" {
				say(refusal)
				return "", nil, false
			}
			voices = append(voices, t)
			continue
		}
		if !c.svc.d.Files.Available() {
			filesOff = true
			continue
		}
		up = append(up, session.Upload{Name: f.Name, Body: f.body(c)})
	}
	if filesOff {
		say(v.T("no_files"))
	}
	text := m.Text
	if len(voices) > 0 {
		parts := make([]string, 0, len(voices)+1)
		for _, v := range voices {
			parts = append(parts, voiceMark+"\n"+v)
		}
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
		text = strings.Join(parts, "\n\n")
	}
	return text, up, len(up) > 0 || strings.TrimSpace(text) != ""
}

// transcribe makes a voice message text, through the same media service and
// model as a voice note attached in a chat, as the owner of the binding: the
// owner's model allowlist and quota apply. It returns the sentence for the
// sender when it cannot.
func (c *Connectors) transcribe(ctx context.Context, b connectors.Binding, name string, body io.Reader, v hostmsg.Voice) (string, string) {
	m := c.svc.d.Media
	if !m.CanTranscribe() {
		return "", v.T("no_listen")
	}
	tctx, cancel := context.WithTimeout(ctx, transcribeTimeout)
	defer cancel()
	t, err := m.Transcribe(tctx, principal(b.Owner), name, body)
	if t = strings.TrimSpace(t); err != nil || t == "" {
		c.log.Warn("studio: could not transcribe the voice message of a connector", "binding", b.ID, "kind", b.Kind, "error", err)
		return "", v.T("no_transcript")
	}
	return t, ""
}

// media reads the files of a message through the add-on. A file that cannot
// be read is left out, and the sentence for the sender says why: too large,
// or not read. The name of a file is made safe here too, whatever the add-on
// sent; the chat saves it in its own folder. The add-on writes each file to the
// host in chunks by a handle (see connectorxfer.go). A kind that declares no
// way to receive files gets the sender a plain sentence.
func (c *Connectors) media(ctx context.Context, b connectors.Binding, token string, refs []string, v hostmsg.Voice) ([]inboundFile, string) {
	var (
		out    []inboundFile
		note   string
		total  int64
		limits = c.FileLimits(ctx, b)
		start  = time.Now()
	)
	if limits.Receive.Bytes <= 0 {
		return nil, v.T("no_file_ability")
	}
	for _, ref := range refs {
		req := contract.ConnectorRequest{Op: contract.ConnectorOpMedia, Kind: b.Kind, Token: token, Media: ref}
		req.Transfer = &contract.ConnectorTransfer{Handle: newHandle(), MaxBytes: limits.Receive.Bytes}
		mctx, cancel := context.WithTimeout(ctx, contract.ConnectorTransferTimeout)
		resp, err := c.o.Transport.Call(mctx, BindingConnectorScope(b), req)
		cancel()
		if err == nil && resp.Error != nil {
			err = resp.Error
		}
		f, ferr := c.inboundOf(b, limits, resp, err)
		var ce *contract.ConnectorError
		switch {
		case errors.Is(ferr, errFileTooLarge) || (errors.As(err, &ce) && ce.Code == contract.ConnectorTooLarge):
			note = c.sentFileTooBig(v, limits, f.Size)
			c.auditFile(b, "in", f.Size, "too_large", limits.Receive, time.Since(start))
			continue
		case ferr != nil:
			c.log.Warn("studio: could not read the file of a connector message", "binding", b.ID, "kind", b.Kind, "error", ferr)
			if note == "" {
				note = v.T("no_file")
			}
			c.auditFile(b, "in", 0, "failed", limits.Receive, time.Since(start))
			continue
		}
		if !typeAllowed(limits.ReceiveTypes, f.MIME) {
			c.log.Warn("studio: a connector file has a type the kind does not declare", "binding", b.ID, "kind", b.Kind)
			note = v.T("file_type")
			c.auditFile(b, "in", f.Size, "type", limits.Receive, time.Since(start))
			continue
		}
		// Files of one message may not add up to more than a few times the limit.
		if total += f.Size; total > limits.Receive.Bytes*fileTurnFactor {
			note = v.T("file_total")
			c.auditFile(b, "in", f.Size, "total", limits.Receive, time.Since(start))
			continue
		}
		c.auditFile(b, "in", f.Size, "done", limits.Receive, time.Since(start))
		out = append(out, f)
	}
	return out, note
}

var errAgentGone = errors.New("the agent was deleted")

// chatFor returns the chat of a conversation (see conversationOf), making it
// when there is none, when it ran with another agent than the binding has
// now, or when fresh asks for a new one. created says that the chat is new.
func (c *Connectors) chatFor(ctx context.Context, b connectors.Binding, key, name string, fresh bool) (string, bool, error) {
	if id, ok, err := c.o.Store.ChatOf(ctx, b.ID, key); err == nil && ok && !fresh {
		if rec, err := c.svc.Get(ctx, b.Owner, id); err == nil && rec.AgentID == b.AgentID {
			c.followAgentModel(ctx, b, rec)
			return id, false, nil
		}
	}
	title := c.kindTitle(ctx, b) + " · " + name
	rec, err := c.svc.CreateAs(ctx, b.Owner, CreateInput{Title: title, AgentID: b.AgentID}, Origin{Kind: session.OriginConnector, ID: b.ID})
	if err != nil {
		if isAgentMissing(err) {
			return "", false, errAgentGone
		}
		return "", false, err
	}
	if err := c.o.Store.SetChat(ctx, b.ID, key, rec.ID); err != nil {
		return "", false, err
	}
	return rec.ID, true, nil
}

// followAgentModel gives a kept chat the model its agent has now. A chat takes
// the agent's model when it is made, so a later change of that model would not
// reach the sender's chat. A chat that is busy keeps its model until the next
// message.
func (c *Connectors) followAgentModel(ctx context.Context, b connectors.Binding, rec session.ChatRecord) {
	ag, err := c.svc.Agent(ctx, b.Owner, b.AgentID)
	if err != nil || strings.TrimSpace(ag.Model) == "" || ag.Model == rec.Model {
		return
	}
	if _, err := c.svc.Update(ctx, b.Owner, rec.ID, UpdateInput{Model: &ag.Model}); err != nil {
		c.log.Warn("studio: could not move a connector chat to the model of its agent", "binding", b.ID, "error", err)
	}
}

// deliver hands the message to the chat and makes sure that the answer goes
// back. One reply goroutine serves a conversation at a time: it sends the
// answer of each turn until the chat is idle.
func (c *Connectors) deliver(ctx context.Context, b connectors.Binding, token string, m contract.ConnectorMessage, st *senderState, chatID string, up []session.Upload, earlier string) {
	// What the sender may do in this turn. A message that joins a turn that
	// runs, or waits for the next, narrows the grant to what both senders may
	// do: a turn never does more for one sender than it may for another.
	grant := c.grantFor(ctx, b, m)
	c.smu.Lock()
	was := st.active
	st.active = true
	if was {
		st.joining++
	}
	if was && st.grant != nil {
		grant = st.grant.merge(grant)
	}
	st.grant = &grant
	c.smu.Unlock()
	c.svc.access.set(chatID, grant)
	ctx = withTurnGrant(ctx, grant)
	if was && c.beforeJoin != nil {
		c.beforeJoin()
	}
	var events <-chan session.Event
	var cancel func()
	if !was {
		var err error
		if events, cancel, err = c.svc.Events(ctx, b.Owner, chatID); err != nil {
			c.smu.Lock()
			st.active, st.grant = false, nil
			c.smu.Unlock()
			c.svc.access.clear(chatID)
			c.say(b, token, m.Chat, m.Thread, c.voice(ctx, b, m.SenderID, m.Chat).T("unavailable"))
			return
		}
	}
	_, err := c.start(ctx, b, chatID, m.Text, up, earlier)
	if was {
		c.smu.Lock()
		st.joining--
		c.smu.Unlock()
	}
	if err != nil {
		if !was {
			cancel()
			c.smu.Lock()
			st.active, st.grant = false, nil
			c.smu.Unlock()
			c.svc.access.clear(chatID)
		}
		v := c.voice(ctx, b, m.SenderID, m.Chat)
		text := v.T("failed")
		if errors.Is(err, ErrAgentFolderLimit) {
			c.log.Warn("studio: a connector message exceeds the folder limit", "binding", b.ID)
			text = v.T("folder_limit")
		}
		if errors.Is(err, ErrAgentFolderMissing) {
			// The folder went away between the check and the turn.
			c.log.Warn("studio: a connector message was refused: the agent's folder is missing", "binding", b.ID)
			text = v.T("no_folder")
		}
		c.say(b, token, m.Chat, m.Thread, text)
		return
	}
	if was {
		return
	}
	if !c.spawn(func() {
		defer cancel()
		c.reply(b, token, m, st, chatID, events)
	}) {
		// Closed meanwhile: give up the reply.
		cancel()
		c.smu.Lock()
		st.active, st.grant = false, nil
		c.smu.Unlock()
		c.svc.access.clear(chatID)
	}
}

// start gives the chat the message. earlier, when set, comes with this
// message only (session.Message.Directive): the model reads it before the
// text and the transcript does not keep it. A turn that is already running
// cannot take a directive, so the message then goes without it.
func (c *Connectors) start(ctx context.Context, b connectors.Binding, chatID, text string, up []session.Upload, earlier string) (SendResult, error) {
	if earlier != "" {
		res, err := c.svc.sendWithFiles(ctx, b.Owner, chatID, text, up, earlier, nil)
		if !errors.Is(err, ErrCommandBusy) {
			return res, err
		}
	}
	return c.svc.SendBackground(ctx, b.Owner, chatID, text, up)
}

// reply follows each turn of the chat and sends its answer. While a turn runs
// it shows typing and, unless the binding's reply mode is off, a preview of
// the turn: a working message that it edits, which the answer replaces, or in
// the stream mode of a direct conversation a draft, which the answer
// follows. Previews are coalesced to the pace of the binding's kind. The
// files the turn made follow the answer.
func (c *Connectors) reply(b connectors.Binding, token string, m contract.ConnectorMessage, st *senderState, chatID string, events <-chan session.Event) {
	typing := time.NewTicker(typingEvery)
	defer typing.Stop()
	pace := c.livePaceOf(b)
	flush := time.NewTicker(pace.every)
	defer flush.Stop()
	var poll <-chan time.Time
	errText := ""
	l, since, turn := c.startLive(b, token, m, pace), time.Now(), 0
	// settle gives the chat up when it is idle (see settled).
	settle := func() bool {
		ok := c.settled(b, st, chatID, events)
		if c.afterSettle != nil {
			c.afterSettle(ok)
		}
		return ok
	}
	// quiet is set when settle saw a turn on its way and none of its events
	// has come yet. The claim that starts a turn from the waiting messages is
	// also held for a moment when it finds none, so that sign can be false:
	// recheck then ends the reply instead of waiting for a turn that never
	// comes, which would leave the sender refused as busy.
	quiet := false
	recheck := time.NewTicker(time.Second)
	defer recheck.Stop()
	for {
		ended := false
		var again <-chan time.Time
		if quiet {
			again = recheck.C
		}
		select {
		case <-again:
			if settle() {
				return
			}
			continue
		case ev, ok := <-events:
			if !ok {
				events = nil
				p := time.NewTicker(time.Second)
				defer p.Stop()
				poll = p.C
				continue
			}
			quiet = false
			l.observe(ev)
			switch ev.Kind {
			case session.KindError:
				errText = ev.Text
			case session.KindTurnEnd:
				ended, turn = true, ev.Turn
			}
		case <-poll:
			if info, err := c.svc.Turn(context.Background(), b.Owner, chatID); err == nil && !info.Running && info.Last != "" {
				ended = true
			}
		case <-flush.C:
			c.flush(b, token, m, l, pace)
		case <-typing.C:
			c.typing(b, token, m, l.id)
		case <-c.done:
			return
		}
		if !ended {
			continue
		}
		answer := c.answerOf(b, chatID, turn, errText, c.voice(context.Background(), b, m.SenderID, m.Chat))
		ids := c.finish(b, token, m, l, answer)
		c.remember(b, m, chatID, ids)
		c.sendFiles(b, token, m, chatID, turn, since, answer)
		errText = ""
		if settle() {
			return
		}
		// A waiting message started the next turn.
		l, since, turn, quiet = c.startLive(b, token, m, pace), time.Now(), 0, true
	}
}

// settled gives the chat up when it is idle. It reports false when another turn
// runs, when its events are queued, or when one is on its way. The check is
// under smu on purpose: a message that arrives now must see either the running
// turn or the cleared flag.
func (c *Connectors) settled(b connectors.Binding, st *senderState, chatID string, events <-chan session.Event) bool {
	c.smu.Lock()
	tctx, tcancel := context.WithTimeout(context.Background(), 5*time.Second)
	info, err := c.svc.Turn(tctx, b.Owner, chatID)
	tcancel()
	// A waiting message starts the next turn at once, and that turn may be
	// over before this point; its events are then still queued.
	if (err == nil && info.Running) || len(events) > 0 || st.joining > 0 {
		c.smu.Unlock()
		return false
	}
	// A turn that finished starts the next one from the waiting messages
	// after it has announced its end, so for a moment the chat is idle with a
	// message waiting. That turn needs this reply.
	if err == nil && info.Last.Finished() {
		if wait, perr := c.svc.Pending(context.Background(), b.Owner, chatID); perr == nil && len(wait) > 0 {
			c.smu.Unlock()
			return false
		}
	}
	st.active, st.grant = false, nil
	c.smu.Unlock()
	c.svc.access.clear(chatID)
	return true
}

// remember keeps the chat of the messages that hold an answer, in the thread
// mode, so that a reply to one of them goes on in this chat.
func (c *Connectors) remember(b connectors.Binding, m contract.ConnectorMessage, chatID string, ids []string) {
	if b.ConversationMode() != connectors.ConversationThread {
		return
	}
	for _, id := range ids {
		if err := c.o.Store.SetChat(context.Background(), b.ID, replyKey(m.Chat, id), chatID); err != nil {
			c.log.Warn("studio: could not keep the chat of a connector answer", "binding", b.ID, "error", err)
		}
	}
}

// answerOf is what the sender reads for the turn that just ended.
func (c *Connectors) answerOf(b connectors.Binding, chatID string, turn int, errText string, v hostmsg.Voice) string {
	var info store.TurnInfo
	// The event comes before the store records the outcome, so look again for
	// up to five seconds. Close ends the wait at once.
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
wait:
	for range 250 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var err error
		info, err = c.svc.Turn(ctx, b.Owner, chatID)
		cancel()
		if err != nil || !info.Running {
			break
		}
		select {
		case <-c.done:
			break wait
		case <-tick.C:
		}
	}
	switch info.Last {
	case store.TurnStopped:
		return v.T("stopped")
	case store.TurnInterrupted:
		return v.T("interrupted")
	case store.TurnFailed:
		if errText == "" {
			return v.T("failed")
		}
		return errText
	}
	fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if t := strings.TrimSpace(c.svc.finalTextOf(fctx, chatID, turn)); t != "" {
		return t
	}
	return v.T("no_answer")
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
		if !b.Enabled || b.NotifyChat == "" || b.CredentialsOnly() {
			continue
		}
		token, err := c.openToken(ctx, b)
		if err != nil {
			c.log.Warn("studio: could not read a bot token to forward a notification", "binding", b.ID)
			continue
		}
		if !c.spawn(func() { c.say(b, token, b.NotifyChat, "", text) }) {
			return // closed meanwhile
		}
	}
}

// sleep waits for d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
