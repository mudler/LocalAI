// SPDX-License-Identifier: MIT

// Package pgbus carries fan-out broadcasts between frontend replicas over the
// PostgreSQL a distributed deployment already runs, using LISTEN/NOTIFY.
//
// It exists so that a deployment needs a database and its own HTTP listener and
// nothing else. It replaces the fan-out half of the NATS surface only:
// request/reply and queue groups are not here.
//
// Delivery is at-most-once, and deliberately so. A NOTIFY reaches the sessions
// that are LISTENing when it is issued and nobody else, so a replica that is
// reconnecting misses what was published in that window, exactly as a NATS core
// subscriber does. Two consequences are load bearing:
//
//   - Nothing downstream may treat a message it did not receive as evidence
//     about a node. A carrier that cannot deliver is not a worker that is gone,
//     and the difference between those two is the invariant this whole
//     programme is built around. Absence is read from the database, never from
//     silence on a bus.
//   - Anything that must survive a gap belongs in a table, with the broadcast
//     as a hint to go and look.
package pgbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// channelPrefix namespaces this carrier's LISTEN channels away from anything
// else that may be using the same database.
const channelPrefix = "localai_"

// maxNotifyPayloadBytes is PostgreSQL's own cap on a pg_notify payload, and it
// is EXCLUSIVE: async.c refuses a payload when strlen(payload) >=
// NOTIFY_PAYLOAD_MAX_LENGTH, so 7999 bytes is the largest that is accepted and
// 8000 already fails. Verified against postgres:16.
//
// What is measured against it is the ENCODED NOTIFICATION and not the caller's
// payload: the subject, the JSON escaping and the envelope's own keys all
// travel too, and a payload sized against the cap alone would ship 7999 bytes
// of data in a notification the server rejects.
const maxNotifyPayloadBytes = 8000

// deliveryQueueDepth is how far one subscriber may fall behind before its
// broadcasts are dropped.
//
// Per subscriber, not per carrier. Handlers here write to SSE streams and to
// caller-owned channels, and running them on the listener goroutine would let
// one blocked writer stop delivery for every subject in the deployment. The
// drop is logged at error level: losing a broadcast loudly is recoverable,
// wedging the carrier silently is not.
const deliveryQueueDepth = 256

// broadcastRoots is the closed set of subject roots this carrier serves.
// A subject whose first token is not here is REFUSED at publish and at
// subscribe, rather than being mapped to a channel of its own.
//
// Refused rather than mapped, because a LISTEN channel name is capped at 63
// bytes and a per-subject channel would mean one LISTEN per job id, per gallery
// operation and per response id, which is unbounded. Refused rather than
// silently dropped, because a new subject that goes nowhere is exactly the
// class of defect this programme exists to remove.
var broadcastRoots = map[string]struct{}{
	"jobs": {}, "agent": {}, "gallery": {}, "cache": {},
	"staging": {}, "prefixcache": {}, "responses": {}, "state": {},
}

// BroadcastRoots returns the roots this carrier serves, sorted.
//
// Exported so a spec can assert over the whole set rather than over a sample it
// re-spells, which is what makes "no channel can exceed PostgreSQL's identifier
// limit" a property of the set instead of a property of the examples someone
// remembered to write down.
func BroadcastRoots() []string {
	roots := make([]string, 0, len(broadcastRoots))
	for root := range broadcastRoots {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots
}

// ErrUnservedSubject is the class every root refusal belongs to, so a caller
// can tell "this carrier does not serve that family" apart from a database
// failure without matching on strings.
var ErrUnservedSubject = errors.New("subject root is not served by the broadcast carrier")

// ChannelFor maps a subject onto the LISTEN channel that carries it. It is
// exported so a spec can assert the mapping directly rather than inferring it
// from delivery.
//
// This is the single spelling of the channel rule. Publish and Subscribe both
// call it, and neither may re-derive a channel name of its own: a publisher and
// a subscriber that disagree about a channel is a subject that is published
// successfully and delivered to nobody, with no error anywhere.
func ChannelFor(subject string) (string, error) {
	root, _, _ := strings.Cut(subject, ".")
	if root == "" {
		return "", fmt.Errorf("%w: %q has no root token", ErrUnservedSubject, subject)
	}
	if _, ok := broadcastRoots[root]; !ok {
		return "", fmt.Errorf("%w: %q (root %q); add the root to broadcastRoots if it is meant to fan out", ErrUnservedSubject, subject, root)
	}
	return channelPrefix + root, nil
}

// Config configures the deployment's broadcast carrier.
type Config struct {
	// DSN is the PostgreSQL connection string the LISTEN connection is opened
	// from. It is separate from DB because a LISTEN connection cannot be
	// borrowed from a pool: LISTEN registrations belong to one backend session,
	// so a pooled handle would register them on whichever connection it handed
	// out and lose them on the next one. This connection is pinned for the life
	// of the Bus.
	//
	// Its ONE source is config.ApplicationConfig.Auth.DatabaseURL, fed by
	// --auth-database-url / LOCALAI_AUTH_DATABASE_URL. It is not a flag of its
	// own and it is never read from the environment: a second knob would let a
	// deployment point the LISTEN connection and the pool at different
	// databases, which is a carrier that publishes and never delivers with no
	// error anywhere. New refuses that combination rather than trusting the
	// convention.
	DSN string
	// DB is the pooled handle NOTIFY and the spill table are written on.
	DB *gorm.DB
	// Queue bounds how many notifications may wait for the resolver before this
	// carrier starts dropping them. Zero means DefaultQueueDepth.
	//
	// A field so a deployment whose subjects spill heavily can buy itself more
	// slack, and so a spec can fill the queue without publishing a thousand
	// messages to do it.
	Queue int
	// Retention is how long a spilled row outlives its notification. Zero means
	// DefaultSpillRetention.
	Retention time.Duration
	// SweepInterval is how often this carrier retires spilled broadcasts that
	// have aged out. Zero means Retention / 2.
	//
	// It is a field rather than a derived value alone so that the purge loop
	// being STARTED is a testable fact. PurgeBefore, SweepSpill and
	// SpillSweepSQL can all be exercised directly, and none of them proves the
	// carrier ever calls them: a deleted `go b.purge()` left the whole suite
	// green and bus_messages growing forever.
	SweepInterval time.Duration
}

// notification is what travels in a pg_notify payload. The keys are one byte
// each because every byte of the envelope is a byte the caller's payload cannot
// use before it has to spill.
type notification struct {
	Subject string          `json:"s"`
	Data    json.RawMessage `json:"d,omitempty"`
	SpillID string          `json:"i,omitempty"`
}

// Bus is one replica's end of the broadcast carrier.
type Bus struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	// connected is a fact about this process's link to the database and about
	// nothing else. No consumer may branch on it: "the carrier is down" is not
	// one of the four conditions a node can be in, and turning it into absence
	// evidence would let a database hiccup evict models.
	connected atomic.Bool

	// dropped counts notifications the listener discarded because the resolver
	// was behind. Same rule as connected: reportable, never actionable.
	dropped atomic.Uint64

	// appName identifies this carrier's LISTEN session in pg_stat_activity, and
	// retention is Config.Retention after defaulting. Both are set once in New
	// and read from other goroutines afterwards, so neither may be reassigned.
	appName   string
	retention time.Duration

	cmds         chan listenCmd
	inbound      chan inbound
	listenerDone chan struct{}
	resolverDone chan struct{}
	closeOnce    sync.Once

	// cbMu guards reconnectCbs. Its own lock, because a registration arrives on
	// a caller's goroutine while the listener is redialling and neither should
	// wait on delivery to finish.
	cbMu         sync.Mutex
	reconnectCbs []func()

	// listenMu orders a channel's refcount decision with the LISTEN or
	// UNLISTEN that decision implies, as ONE step.
	//
	// Deciding under mu and issuing outside it is a real defect and not a
	// theoretical one. A last Unsubscribe that has decided to UNLISTEN can be
	// overtaken by a Subscribe that has decided to LISTEN; the two reach the
	// connection in that order; the channel ends up not listened with a live
	// subscription on it. It does not self-heal, because the next Subscribe on
	// that root sees first == false and never re-LISTENs, so the whole root is
	// silently deaf on this replica until a connection drop triggers relisten.
	//
	// A second lock rather than mu, because command waits on the listener
	// goroutine and delivery takes mu: holding mu across that wait would
	// deadlock the carrier. Neither the listener nor the resolver ever takes
	// this one.
	listenMu sync.Mutex

	// listenBarrier is a test seam and is nil in production. See barrier.
	listenBarrier func(stage, op string)

	mu     sync.Mutex
	nextID uint64
	subs   map[string]map[uint64]*subscription
}

// New opens the carrier: one pinned LISTEN connection, and the pooled handle
// publishes travel on.
func New(ctx context.Context, cfg Config) (*Bus, error) {
	if cfg.DB == nil {
		return nil, errors.New("pgbus: no database handle")
	}
	// The one dialect statement in this package. Everything below it is
	// PostgreSQL-only (pg_notify, LISTEN, now(), make_interval), and an
	// unguarded now() on SQLite reads as a missing migration rather than as a
	// carrier that was never meant to run there.
	if name := cfg.DB.Dialector.Name(); name != "postgres" {
		return nil, fmt.Errorf("pgbus: the broadcast carrier requires PostgreSQL, got %q", name)
	}
	if cfg.DSN == "" {
		return nil, errors.New("pgbus: no DSN for the LISTEN connection")
	}

	// Per carrier and not per deployment, so two replicas on one database are
	// separable in pg_stat_activity and a spec can drop one listener's session
	// without touching the peer it is asserting against.
	appName := applicationNamePrefix + uuid.NewString()

	conn, err := dial(ctx, cfg.DSN, appName)
	if err != nil {
		return nil, fmt.Errorf("pgbus: opening the LISTEN connection: %w", err)
	}
	if err := verifySameDatabase(ctx, conn, cfg.DB); err != nil {
		_ = conn.Close(ctx)
		return nil, err
	}

	queue := cfg.Queue
	if queue <= 0 {
		queue = DefaultQueueDepth
	}
	retention := cfg.Retention
	if retention <= 0 {
		retention = DefaultSpillRetention
	}

	busCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	b := &Bus{
		cfg:          cfg,
		ctx:          busCtx,
		cancel:       cancel,
		appName:      appName,
		retention:    retention,
		cmds:         make(chan listenCmd),
		inbound:      make(chan inbound, queue),
		listenerDone: make(chan struct{}),
		resolverDone: make(chan struct{}),
		subs:         map[string]map[uint64]*subscription{},
	}
	b.connected.Store(true)
	go b.listen(conn)
	go b.resolve()
	go b.purge()
	return b, nil
}

// verifySameDatabase refuses a carrier whose LISTEN connection and whose pool
// are not on the same database.
//
// That combination has no other symptom: every publish succeeds, every
// subscribe succeeds, and nothing is ever delivered, on every replica, forever.
// It is cheap to exclude here and expensive to diagnose anywhere else.
func verifySameDatabase(ctx context.Context, conn *pgx.Conn, db *gorm.DB) error {
	const identity = `SELECT current_database() || '@' || coalesce(host(inet_server_addr()), 'local') || ':' || coalesce(inet_server_port(), 0)`

	var listenSide string
	if err := conn.QueryRow(ctx, identity).Scan(&listenSide); err != nil {
		return fmt.Errorf("pgbus: identifying the LISTEN connection's database: %w", err)
	}
	var poolSide string
	if err := db.WithContext(ctx).Raw(identity).Row().Scan(&poolSide); err != nil {
		return fmt.Errorf("pgbus: identifying the pool's database: %w", err)
	}
	if listenSide != poolSide {
		return fmt.Errorf("pgbus: the LISTEN connection and the pool are on a different database (%s vs %s); every broadcast would be published and never delivered", listenSide, poolSide)
	}
	return nil
}

// DSN returns the connection string this carrier listens on. It exists so the
// wiring can be pinned to the deployment's one database URL by equality rather
// than by shape: any DSN passes a shape check, including one that addresses a
// different database from the pool.
func (b *Bus) DSN() string { return b.cfg.DSN }

// IsConnected reports whether the pinned LISTEN connection is up. See the
// comment on Bus.connected: it is for logging and specs, not for decisions.
func (b *Bus) IsConnected() bool { return b.connected.Load() }

// Close stops the carrier. It is idempotent and does not wait for handlers that
// are still running: a handler that never returns must not be able to hold up
// the shutdown of the process it is in.
func (b *Bus) Close() {
	b.closeOnce.Do(func() {
		b.cancel()
		<-b.listenerDone
		<-b.resolverDone
		b.connected.Store(false)
	})
}

// inlineNotification encodes what Publish puts on the wire when a message fits
// in the notification itself, and returns it together with the caller's encoded
// payload so the spill path does not marshal a second time.
//
// It is the ONE place the wire form of an inline broadcast is built. Publish
// measures what it returns and FitsInline asks about the same bytes, so a
// caller that has proved its worst case fits cannot be proved wrong later by a
// change to the envelope's keys.
func inlineNotification(subject string, data any) ([]byte, json.RawMessage, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, nil, fmt.Errorf("pgbus: encoding a broadcast on %q: %w", subject, err)
	}
	encoded, err := json.Marshal(notification{Subject: subject, Data: payload})
	if err != nil {
		return nil, nil, fmt.Errorf("pgbus: encoding the notification for %q: %w", subject, err)
	}
	return encoded, payload, nil
}

// FitsInline reports whether Publish would carry this message in the
// notification itself rather than writing it to a row and notifying the id.
//
// It is a PREDICATE and never an action: nothing here refuses, drops or
// truncates a message, and no caller may use it to decide not to publish. The
// spill path is correct for every family on this carrier, and a message that
// does not fit costs a row and a SELECT, never its contents.
//
// It exists for the one question that is worth asking BEFORE a message is ever
// published: can a family whose payload has a known upper bound put that bound
// on the wrong side of the cap? A family whose worst case fits is a family that
// never spills, and a configuration change that moves the bound past the cap
// can then be refused at startup instead of turning every request on the
// inference path into a table write nobody notices. See
// requirePrefixCacheFitsInline in core/application.
//
// It shares its size decision with Publish rather than restating one: the same
// encoder, the same comparison against the same constant. A second comparison
// would be a second constant in disguise.
func FitsInline(subject string, data any) (bool, error) {
	encoded, _, err := inlineNotification(subject, data)
	if err != nil {
		return false, err
	}
	return len(encoded) < maxNotifyPayloadBytes, nil
}

// Publish fans a message out to every subscriber of the subject on every
// replica, including this one.
func (b *Bus) Publish(subject string, data any) error {
	channel, err := ChannelFor(subject)
	if err != nil {
		return err
	}
	encoded, payload, err := inlineNotification(subject, data)
	if err != nil {
		return err
	}
	// The one size decision in this package. Several subjects on this carrier
	// exceed the cap in normal operation (a job result carries a whole LLM
	// output; a gallery progress event carries one entry per node), so the
	// spill is the ordinary path for them and not an error case.
	if len(encoded) >= maxNotifyPayloadBytes {
		id, err := b.spill(subject, payload)
		if err != nil {
			return err
		}
		encoded, err = json.Marshal(notification{Subject: subject, SpillID: id})
		if err != nil {
			return fmt.Errorf("pgbus: encoding the spill notification for %q: %w", subject, err)
		}
	}

	// pg_notify rather than a NOTIFY statement, because the channel is a value
	// here and NOTIFY would need it interpolated as an identifier.
	if err := b.cfg.DB.WithContext(b.ctx).Exec("SELECT pg_notify(?, ?)", channel, string(encoded)).Error; err != nil {
		return fmt.Errorf("pgbus: publishing on %q: %w", subject, err)
	}
	return nil
}

// Subscribe registers a handler for every subject matching filter. The filter
// grammar is the shared one: an exact subject, or single-token `*` wildcards.
func (b *Bus) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	// The shared rule, asked rather than re-spelled. A carrier that decided for
	// itself which filters it accepts would drift from the matcher that decides
	// which of them deliver, and the drift presents as a peer receiving an
	// event on one replica and missing it on another.
	if err := messaging.ValidFilter(subject); err != nil {
		return nil, err
	}
	channel, err := ChannelFor(subject)
	if err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, fmt.Errorf("pgbus: no handler for %q", subject)
	}

	// Before the lock, so a spec can observe that this registration has
	// started even when the lock is what stops it going any further.
	b.barrier("enter", "LISTEN")
	b.listenMu.Lock()
	defer b.listenMu.Unlock()

	b.mu.Lock()
	b.nextID++
	sub := &subscription{
		bus:     b,
		channel: channel,
		filter:  subject,
		id:      b.nextID,
		queue:   make(chan []byte, deliveryQueueDepth),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	first := len(b.subs[channel]) == 0
	b.mu.Unlock()

	// The LISTEN before the registration, not after it.
	//
	// Both orders satisfy "Subscribe has listened by the time it returns", so
	// the CALLER cannot tell them apart. An observer can: with the registration
	// first, Subscribers() counts a handler whose channel is not listened yet,
	// so anything waiting on that count as a readiness signal proceeds into a
	// window where a publish is silently lost, which is at-most-once delivery
	// doing exactly what it says while looking like a broken subscription.
	// Registering last makes the counter mean what its name says, and it
	// removes the failed-LISTEN rollback: a registration never made needs no
	// undoing.
	if first {
		b.barrier("issue", "LISTEN")
		if err := b.command("LISTEN " + pgx.Identifier{channel}.Sanitize()); err != nil {
			close(sub.stop)
			return nil, err
		}
	}

	b.mu.Lock()
	if b.subs[channel] == nil {
		b.subs[channel] = map[uint64]*subscription{}
	}
	b.subs[channel][sub.id] = sub
	b.mu.Unlock()

	go sub.run(handler)
	return sub, nil
}

// Subscribers reports how many handlers are registered right now, across every
// channel. A counted handler is a LIVE one: its channel was listened before it
// was counted, so this number is usable as a readiness signal and not only as a
// figure to compare against itself.
//
// It exists to be asserted, because the leak it makes visible has no other
// symptom. Two subscriptions in this deployment are opened and closed PER HTTP
// REQUEST (a job's progress stream and an agent's event stream), and on this
// carrier only the FIRST subscriber of a channel issues a LISTEN: every later
// one just registers an in-process filter. An Unsubscribe that failed to remove
// its filter would therefore leave a replica that has served ten thousand SSE
// requests running ten thousand closures per notification, and nothing in the
// tree would fail. It would just get slower.
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, channel := range b.subs {
		n += len(channel)
	}
	return n
}

// barrier is a test seam and does nothing in production.
//
// It exists because the ordering listenMu enforces cannot be observed from
// outside this package and cannot be provoked from outside it either: the
// natural window is microseconds wide, and it was measured at zero hits in
// forty attempts while being ten out of ten once widened. A spec that waits for
// that window to open is a spec that passes by luck, which is worse than no
// spec at all for a defect that leaves a whole subject root deaf.
func (b *Bus) barrier(stage, op string) {
	if b.listenBarrier != nil {
		b.listenBarrier(stage, op)
	}
}

// deliver resolves one notification and hands it to the subscribers whose
// filters match.
func (b *Bus) deliver(channel, payload string) {
	b.barrier("enter", "DELIVER")

	var n notification
	if err := json.Unmarshal([]byte(payload), &n); err != nil {
		xlog.Error("Broadcast carrier received an undecodable notification", "channel", channel, "error", err)
		return
	}

	data := []byte(n.Data)
	if n.SpillID == "" && len(data) == 0 {
		// Publish cannot produce this: json.Marshal never returns an empty
		// encoding. It is reachable only if something other than this carrier
		// notifies on a localai_ channel, and delivering nil to a handler
		// would be a message that says nothing rather than no message at all.
		xlog.Error("Broadcast carrier received a notification with no payload", "channel", channel, "subject", n.Subject)
		return
	}
	if n.SpillID != "" {
		resolved, err := b.resolveSpill(n.SpillID)
		if err != nil {
			// A dropped broadcast, and never anything more than that. Handing
			// a handler an empty message instead would let a carrier failure
			// be read as a fact about the deployment.
			xlog.Error("Broadcast carrier could not resolve a spilled message", "subject", n.Subject, "id", n.SpillID, "error", err)
			return
		}
		data = resolved
	}

	b.mu.Lock()
	targets := make([]*subscription, 0, len(b.subs[channel]))
	for _, sub := range b.subs[channel] {
		// The shared matcher, in the one place delivery is decided. A second
		// spelling here is the divergence the single-sourcing exists to
		// prevent: the doubles the specs publish through call the same
		// function.
		if messaging.SubjectMatches(sub.filter, n.Subject) {
			targets = append(targets, sub)
		}
	}
	b.mu.Unlock()

	for _, sub := range targets {
		sub.enqueue(n.Subject, data)
	}
}

// subscription is one handler's registration.
type subscription struct {
	bus     *Bus
	channel string
	filter  string
	id      uint64

	queue   chan []byte
	stop    chan struct{}
	done    chan struct{}
	dropped atomic.Uint64
	once    sync.Once
}

// DropCounter is the optional interface a Subscription satisfies when it can
// report how many broadcasts it lost.
//
// It exists because the drop is otherwise invisible to the party that needs to
// know. The error log lands on the receiving replica, there is no sequence
// number and no gap signal, so "anything that must survive a gap belongs in a
// table, with the broadcast as a hint to go and look" cannot be acted on by the
// subscriber that missed the hint. A subscriber whose subject has no successor
// message, a job result rather than a progress tick, can type-assert to this
// and go read the row.
//
// It is not on messaging.Subscription: a subscription only this carrier can
// answer for does not belong on the interface every carrier implements, and
// widening that interface would make every existing consumer claim a guarantee
// it does not have.
type DropCounter interface {
	Dropped() uint64
}

// Dropped reports how many broadcasts this subscription lost because its
// handler was too far behind. It only ever grows.
func (s *subscription) Dropped() uint64 { return s.dropped.Load() }

func (s *subscription) run(handler func([]byte)) {
	defer close(s.done)
	for {
		select {
		case data := <-s.queue:
			handler(data)
		case <-s.stop:
			return
		case <-s.bus.ctx.Done():
			return
		}
	}
}

func (s *subscription) enqueue(subject string, data []byte) {
	select {
	case s.queue <- data:
	default:
		s.dropped.Add(1)
		xlog.Error("Broadcast carrier dropped a message: subscriber is not keeping up",
			"filter", s.filter, "subject", subject, "depth", deliveryQueueDepth, "dropped", s.dropped.Load())
	}
}

// Unsubscribe stops delivery to this handler and, when it was the last
// subscriber on its channel, stops listening on it.
func (s *subscription) Unsubscribe() error {
	var err error
	s.once.Do(func() {
		b := s.bus
		b.barrier("enter", "UNLISTEN")
		// The whole decision AND its issuance, as one step. See listenMu.
		b.listenMu.Lock()
		defer b.listenMu.Unlock()

		b.mu.Lock()
		delete(b.subs[s.channel], s.id)
		last := len(b.subs[s.channel]) == 0
		if last {
			delete(b.subs, s.channel)
		}
		b.mu.Unlock()

		close(s.stop)
		if last && b.ctx.Err() == nil {
			b.barrier("issue", "UNLISTEN")
			err = b.command("UNLISTEN " + pgx.Identifier{s.channel}.Sanitize())
		}
	})
	return err
}

// spill writes a broadcast that does not fit in a notification to a row, and
// returns the id the notification carries in its place.
func (b *Bus) spill(subject string, payload []byte) (string, error) {
	row := BusMessage{ID: uuid.New().String(), Subject: subject, Payload: payload}
	if err := b.cfg.DB.WithContext(b.ctx).Create(&row).Error; err != nil {
		return "", fmt.Errorf("pgbus: spilling a broadcast on %q: %w", subject, err)
	}
	return row.ID, nil
}

func (b *Bus) resolveSpill(id string) ([]byte, error) {
	var row BusMessage
	if err := b.cfg.DB.WithContext(b.ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return row.Payload, nil
}

var _ messaging.Broadcaster = (*Bus)(nil)
