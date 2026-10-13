// Package pgbus carries fan-out broadcasts between frontend replicas over the
// PostgreSQL that a distributed deployment already runs, using LISTEN and
// NOTIFY.
//
// It exists so that a deployment needs a database and nothing else. It is the
// fan-out half of the NATS surface only: request and reply and queue groups are
// not here.
//
// Delivery is at-most-once, on purpose. A NOTIFY reaches the sessions that are
// listening when it is issued and nobody else, so a replica that is
// reconnecting misses what was published in that window, as a NATS subscriber
// does. Two consequences matter:
//
//   - Nothing may read a message that did not arrive as evidence about a node.
//     A carrier that cannot deliver is not a worker that is gone. Absence is
//     read from the database and never from silence on a bus.
//   - Anything that must survive a gap belongs in a table, and the broadcast is
//     a hint to go and look.
//
// Limits, measured on a four-core database: about 16,000 broadcasts a second of
// 200 bytes, and about 8,000 a second with 20 listeners or with 2 to 8 KiB. A
// broadcast that does not fit in a notification is written to a row and costs a
// SELECT on every replica; a replica reads several rows at the same time. With
// one reader at a time, a replica lost half of a burst of 64 KiB broadcasts
// above about 1,000 a second. Above the limits, broadcasts are dropped and
// counted, and never queued without a bound.
//
// # Connections
//
// The database is shared, and a stock server accepts 100 connections for all of
// its clients. One replica of this carrier uses, at most:
//
//   - 1 pinned connection for LISTEN;
//   - spillFetchers (8) connections while it reads spilled rows;
//   - Config.MaxPublishers (16 by default) connections while it publishes.
//
// That is 25 connections for each replica, and the rest of the application
// comes on top. Publish waits for a free slot when all of them are in use, and
// does not open another connection. Size max_connections for the replicas times
// this budget, or lower MaxPublishers.
package pgbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/mudler/xlog"
	"go.opentelemetry.io/otel/metric"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// channelPrefix namespaces the LISTEN channels of this carrier away from
// anything else that uses the same database.
const channelPrefix = "localai_"

// maxNotifyPayloadBytes is the limit of PostgreSQL for a pg_notify payload, and
// it is exclusive: async.c refuses a payload when strlen(payload) >=
// NOTIFY_PAYLOAD_MAX_LENGTH. 7999 bytes is the largest payload that the server
// accepts, and 8000 fails. Checked against postgres:16.
//
// The size that is measured against it is the encoded notification and not the
// payload of the caller. The subject, the JSON escaping and the keys of the
// envelope travel too, so a payload that is sized against the limit alone
// would ship 7999 bytes of data in a notification that the server rejects.
const maxNotifyPayloadBytes = 8000

// deliveryQueueDepth is how far one subscriber may fall behind before its
// broadcasts are dropped.
//
// It is per subscriber and not per carrier. Handlers write to SSE streams and to
// channels of the caller, and running them on the listener goroutine would let
// one blocked writer stop delivery for every subject in the deployment. The drop
// is logged at error level and counted: to lose a broadcast loudly is
// recoverable, and to wedge the carrier silently is not.
const deliveryQueueDepth = 256

// ChannelFor maps a subject onto the LISTEN channel that carries it. All
// subjects of one root share one channel.
//
// Publish and Subscribe both call it, and neither may work out a channel name on
// its own: a publisher and a subscriber that disagree about a channel is a
// subject that is published and delivered to nobody, with no error anywhere.
//
// The channel is the root and not the subject because a channel name is limited
// to 63 bytes, and a channel for every subject would mean one LISTEN for every
// job, every gallery operation and every response, which has no bound. The set
// of roots is closed (see messaging.ValidateBroadcastSubject), so the set of
// channels is closed too.
func ChannelFor(subject string) (string, error) {
	if err := messaging.ValidateBroadcastSubject(subject); err != nil {
		return "", err
	}
	root, _, _ := strings.Cut(subject, ".")
	return channelPrefix + root, nil
}

// Config configures the carrier of a replica.
type Config struct {
	// DSN is the connection string of the LISTEN connection. It is separate from
	// DB because a LISTEN connection cannot come from a pool: a registration
	// belongs to one backend session, so a pooled handle would register on one
	// connection and lose it on the next. This connection is pinned for the life
	// of the Bus.
	//
	// It comes from one place: config.ApplicationConfig.Auth.DatabaseURL. It is
	// not a flag of its own and is not read from the environment. A second knob
	// would let a deployment point the LISTEN connection and the pool at
	// different databases, which is a carrier that publishes and never delivers.
	// New refuses that combination and does not rely on the convention.
	DSN string
	// DB is the pooled handle that NOTIFY and the spill table are written on.
	DB *gorm.DB
	// Queue bounds how many notifications may wait for the resolver before this
	// carrier drops them. Zero means DefaultQueueDepth.
	Queue int
	// Retention is how long a spilled row outlives its notification. Zero means
	// DefaultSpillRetention.
	Retention time.Duration
	// SweepInterval is how often this carrier deletes the spilled rows that aged
	// out. Zero means Retention / 2.
	SweepInterval time.Duration
	// MaxPublishers bounds how many Publish calls use the database at the same
	// time on this replica. Each one holds a connection of the pool until its
	// statement ends, so this bound is also the bound on the connections that
	// publishing opens. Zero means DefaultMaxPublishers.
	MaxPublishers int
	// FetchTimeout bounds the read of one spilled row. A read that takes longer
	// is dropped and counted, so that it does not hold back the delivery of the
	// broadcasts behind it. Zero means DefaultFetchTimeout.
	FetchTimeout time.Duration
	// Meter receives the counters of the carrier. Nil means the global meter.
	Meter metric.Meter
}

// notification is what travels in a pg_notify payload. The keys are one byte
// long because every byte of the envelope is a byte that the payload of the
// caller cannot use before it has to spill.
type notification struct {
	Subject string          `json:"s"`
	Data    json.RawMessage `json:"d,omitempty"`
	SpillID string          `json:"i,omitempty"`
}

// Bus is one replica's end of the carrier.
type Bus struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	// connected is a fact about the link of this process to the database and
	// about nothing else. Nothing may decide on it. "The carrier is down" is not
	// one of the conditions a node can be in, and to read it as absence would let
	// a database error evict models.
	connected atomic.Bool

	// dropped counts the notifications that the listener or the resolver lost.
	// Like connected, it may be reported, and nothing may act on it.
	dropped atomic.Uint64

	// appName identifies the LISTEN session of this carrier in pg_stat_activity,
	// and retention is Config.Retention after the default. New sets both once and
	// other goroutines read them, so neither may be assigned again.
	appName   string
	retention time.Duration
	metrics   *metrics

	// publishSlots is the semaphore that bounds the concurrent publishes. See
	// Config.MaxPublishers.
	publishSlots chan struct{}
	fetchTimeout time.Duration
	cmds         chan listenCmd
	inbound      chan inbound
	listenerDone chan struct{}
	resolverDone chan struct{}
	purgerDone   chan struct{}
	closeOnce    sync.Once

	// cbMu guards reconnectCbs. It is a lock of its own because a registration
	// comes from the goroutine of a caller while the listener dials again, and
	// neither should wait for delivery to finish.
	cbMu         sync.Mutex
	reconnectCbs []func()

	// listenMu orders the decision on the count of a channel with the LISTEN or
	// UNLISTEN that the decision needs, as one step.
	//
	// To decide under mu and to issue outside it is a real defect. The last
	// Unsubscribe may decide to UNLISTEN, a Subscribe may then decide to LISTEN,
	// and the two may reach the connection in that order: the channel ends up not
	// listened, with a live subscription on it. This does not heal itself,
	// because the next Subscribe sees that it is not the first and never listens
	// again, so the whole root is deaf on this replica until a connection loss
	// makes the carrier listen again.
	//
	// It is not mu because command waits for the listener goroutine and delivery
	// takes mu, so to hold mu across that wait would deadlock the carrier. The
	// listener and the resolver never take this lock.
	listenMu sync.Mutex

	// listenBarrier is a seam for tests and is nil in production. See barrier.
	listenBarrier func(stage, op string)

	mu     sync.Mutex
	nextID uint64
	subs   map[string]map[uint64]*subscription
}

// New opens the carrier: one pinned LISTEN connection, and the pooled handle
// that publishes travel on.
func New(ctx context.Context, cfg Config) (*Bus, error) {
	if cfg.DB == nil {
		return nil, errors.New("pgbus: no database handle")
	}
	// Everything below is PostgreSQL only (pg_notify, LISTEN, now(),
	// make_interval). Without this check, now() on SQLite reads as a missing
	// migration and not as a carrier that was never meant to run there.
	if name := cfg.DB.Dialector.Name(); name != "postgres" {
		return nil, fmt.Errorf("pgbus: the broadcast carrier requires PostgreSQL, got %q", name)
	}
	if cfg.DSN == "" {
		return nil, errors.New("pgbus: no DSN for the LISTEN connection")
	}

	// The name is per carrier and not per deployment, so two replicas on one
	// database are different in pg_stat_activity, and a spec can drop the session
	// of one without touching the peer that it asserts against.
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

	publishers := cfg.MaxPublishers
	if publishers <= 0 {
		publishers = DefaultMaxPublishers
	}

	fetchTimeout := cfg.FetchTimeout
	if fetchTimeout <= 0 {
		fetchTimeout = DefaultFetchTimeout
	}

	busCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	b := &Bus{
		fetchTimeout: fetchTimeout,
		publishSlots: make(chan struct{}, publishers),
		cfg:          cfg,
		ctx:          busCtx,
		cancel:       cancel,
		appName:      appName,
		retention:    retention,
		metrics:      newMetrics(cfg.Meter),
		cmds:         make(chan listenCmd),
		inbound:      make(chan inbound, queue),
		listenerDone: make(chan struct{}),
		resolverDone: make(chan struct{}),
		purgerDone:   make(chan struct{}),
		subs:         map[string]map[uint64]*subscription{},
	}
	b.connected.Store(true)
	go b.listen(conn)
	go b.resolve()
	go b.purge()
	return b, nil
}

// verifySameDatabase refuses a carrier whose LISTEN connection and whose pool are
// not on the same database.
//
// That combination has no other symptom: every publish succeeds, every subscribe
// succeeds, and nothing is ever delivered, on every replica, for ever. It is
// cheap to exclude here and expensive to diagnose anywhere else.
func verifySameDatabase(ctx context.Context, conn *pgx.Conn, db *gorm.DB) error {
	const identity = `SELECT current_database() || '@' || coalesce(host(inet_server_addr()), 'local') || ':' || coalesce(inet_server_port(), 0)`

	var listenSide string
	if err := conn.QueryRow(ctx, identity).Scan(&listenSide); err != nil {
		return fmt.Errorf("pgbus: identifying the database of the LISTEN connection: %w", err)
	}
	var poolSide string
	if err := db.WithContext(ctx).Raw(identity).Row().Scan(&poolSide); err != nil {
		return fmt.Errorf("pgbus: identifying the database of the pool: %w", err)
	}
	if listenSide != poolSide {
		return fmt.Errorf("pgbus: the LISTEN connection and the pool are on a different database (%s vs %s); every broadcast would be published and never delivered", listenSide, poolSide)
	}
	return nil
}

// IsConnected reports whether the pinned LISTEN connection is up. See the
// comment on Bus.connected: it is for reports and logs and not for decisions.
func (b *Bus) IsConnected() bool { return b.connected.Load() }

// Close stops the carrier. It is idempotent and does not wait for handlers that
// are still running: a handler that never returns must not hold up the shutdown
// of its process.
func (b *Bus) Close() {
	b.closeOnce.Do(func() {
		b.cancel()
		<-b.listenerDone
		<-b.resolverDone
		<-b.purgerDone
		b.connected.Store(false)
	})
}

// inlineNotification encodes what Publish puts on the wire when a message fits in
// the notification, and returns it with the encoded payload of the caller so
// that the spill path does not encode a second time.
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

// Publish fans a message out to every subscriber of the subject on every
// replica, this one included.
//
// It waits for a free publish slot when Config.MaxPublishers calls are already
// in progress. The wait ends when the carrier is closed.
func (b *Bus) Publish(subject string, data any) error {
	return b.PublishContext(b.ctx, subject, data)
}

// PublishContext is Publish with a context for the wait for a publish slot and
// for the database statements. When the context ends first, it returns the error
// of the context and publishes nothing.
func (b *Bus) PublishContext(ctx context.Context, subject string, data any) error {
	channel, err := ChannelFor(subject)
	if err != nil {
		return err
	}
	encoded, payload, err := inlineNotification(subject, data)
	if err != nil {
		return err
	}
	if err := messaging.CheckBroadcastSize(subject, len(payload)); err != nil {
		return err
	}

	// The slot is taken before the first statement, and it is held until the
	// last one ends. Without it, a burst of publishers opens a connection for
	// each of them, and a stock server refuses the ones above its limit.
	if err := b.acquirePublishSlot(ctx); err != nil {
		return err
	}
	defer func() { <-b.publishSlots }()

	// A context that also ends when the carrier is closed, so a statement does
	// not outlive Close.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(b.ctx, cancel)
	defer stop()

	// This is the one size decision in the package. Several subjects exceed the
	// limit in normal operation (the result of a job carries the whole output of
	// a model, and a gallery progress event carries one entry for each node), so
	// the spill is the usual path for them and not an error.
	path := pathInline
	if len(encoded) >= maxNotifyPayloadBytes {
		id, err := b.spill(ctx, subject, payload)
		if err != nil {
			return err
		}
		encoded, err = json.Marshal(notification{Subject: subject, SpillID: id})
		if err != nil {
			return fmt.Errorf("pgbus: encoding the spill notification for %q: %w", subject, err)
		}
		path = pathSpill
	}

	// pg_notify and not a NOTIFY statement, because the channel is a value here
	// and NOTIFY would need it as an identifier in the text.
	if err := b.cfg.DB.WithContext(ctx).Exec("SELECT pg_notify(?, ?)", channel, string(encoded)).Error; err != nil {
		return fmt.Errorf("pgbus: publishing on %q: %w", subject, err)
	}
	b.metrics.publish(path)
	return nil
}

// acquirePublishSlot takes one slot of the publish semaphore, or returns the
// error of the context that ended first.
func (b *Bus) acquirePublishSlot(ctx context.Context) error {
	select {
	case b.publishSlots <- struct{}{}:
		return nil
	default:
	}
	select {
	case b.publishSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.ctx.Done():
		return errors.New("pgbus: the carrier is closed")
	}
}

// Subscribe registers a handler for every subject that matches the filter. The
// grammar of the filter is the shared one: an exact subject, or `*` for a whole
// token.
func (b *Bus) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	channel, err := ChannelFor(subject)
	if err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, fmt.Errorf("pgbus: no handler for %q", subject)
	}

	// Before the lock, so a spec can see that this registration has started even
	// when the lock is what stops it going further.
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

	// The LISTEN comes before the registration and not after it.
	//
	// Both orders have listened by the time Subscribe returns, so the caller
	// cannot tell them apart. An observer can. With the registration first, the
	// count of subscribers includes a handler whose channel is not listened yet,
	// so whoever waits on that count as a sign that the carrier is ready goes on
	// into a window where a publish is lost. Registering last makes the count
	// mean what its name says, and it removes the rollback of a failed LISTEN:
	// a registration that was never made needs no undoing.
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

// subscribers reports how many handlers are registered, in every channel. A
// handler that is counted is live: its channel was listened before it was
// counted.
//
// The count exists to be asserted, because the leak that it shows has no other
// symptom. Two subscriptions in a deployment are opened and closed for every
// HTTP request (the progress stream of a job and the event stream of an agent),
// and only the first subscriber of a channel issues a LISTEN. The others only
// register a filter in the process. An Unsubscribe that did not remove its filter
// would leave a replica that served ten thousand requests running ten thousand
// closures for every notification, and nothing would fail. It would be slower.
func (b *Bus) subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, channel := range b.subs {
		n += len(channel)
	}
	return n
}

// barrier is a seam for tests and does nothing in production.
//
// It exists because the order that listenMu enforces cannot be seen from outside
// the package and cannot be provoked from outside either. The natural window is
// microseconds wide: it had no hit in forty attempts, and ten out of ten once it
// was widened. A spec that waits for that window is a spec that passes by luck.
func (b *Bus) barrier(stage, op string) {
	if b.listenBarrier != nil {
		b.listenBarrier(stage, op)
	}
}

// pending is one notification on its way from the listener to the handlers. The
// resolver decodes it in the order it arrived, and a spilled one is read back by
// a worker while the next ones are decoded. The dispatcher takes the pending
// notifications in the same order and waits for each one to be ready.
type pending struct {
	channel string
	n       notification
	data    []byte
	// ok is false when the notification must not be delivered.
	ok    bool
	ready chan struct{}
}

// decode turns a notification as it came off the connection into a pending one.
// A spilled one is not ready: the caller starts fetch for it.
func (b *Bus) decode(in inbound) *pending {
	p := &pending{channel: in.channel, ready: make(chan struct{})}
	if err := json.Unmarshal([]byte(in.payload), &p.n); err != nil {
		b.lose(stageResolve)
		xlog.Error("Broadcast carrier received an undecodable notification", "channel", in.channel, "error", err)
		close(p.ready)
		return p
	}
	p.data = []byte(p.n.Data)
	if p.n.SpillID == "" && len(p.data) == 0 {
		// Publish cannot produce this, because json.Marshal never returns an
		// empty encoding. It is possible only if something other than this
		// carrier notifies on one of its channels. A handler that received nil
		// would see a message that says nothing, and no message is better.
		b.lose(stageResolve)
		xlog.Error("Broadcast carrier received a notification with no payload", "channel", in.channel, "subject", p.n.Subject)
		close(p.ready)
		return p
	}
	p.ok = true
	if p.n.SpillID == "" {
		close(p.ready)
	}
	return p
}

// fetch reads the row of a spilled notification and marks it ready.
func (b *Bus) fetch(p *pending) {
	defer close(p.ready)
	resolved, err := b.resolveSpill(p.n.SpillID)
	if err != nil {
		// A dropped broadcast and nothing more. A handler that got an empty
		// message instead would read a carrier failure as a fact about the
		// deployment.
		p.ok = false
		b.lose(stageResolve)
		xlog.Error("Broadcast carrier could not resolve a spilled message", "subject", p.n.Subject, "id", p.n.SpillID, "error", err)
		return
	}
	p.data = resolved
}

// deliver hands a notification that is ready to the subscribers whose filters
// match.
func (b *Bus) deliver(p *pending) {
	b.barrier("enter", "DELIVER")
	if !p.ok {
		return
	}

	b.mu.Lock()
	targets := make([]*subscription, 0, len(b.subs[p.channel]))
	for _, sub := range b.subs[p.channel] {
		// The shared matcher decides here and nowhere else. A second spelling is
		// the drift that the shared function exists to prevent.
		if messaging.SubjectMatches(sub.filter, p.n.Subject) {
			targets = append(targets, sub)
		}
	}
	b.mu.Unlock()

	for _, sub := range targets {
		sub.enqueue(p.n.Subject, p.data)
	}
}

// lose counts a broadcast that this replica received and did not deliver.
func (b *Bus) lose(stage string) {
	b.dropped.Add(1)
	b.metrics.drop(stage)
}

// subscription is the registration of one handler.
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

// DropCounter is the optional interface of a Subscription that can report how
// many broadcasts it lost.
//
// It exists because the loss is otherwise invisible to the party that needs to
// know. The error log is on the replica that received the broadcast, and there is
// no sequence number and no signal of a gap, so a subscriber cannot act on "a
// broadcast that must survive a gap belongs in a table". A subscriber whose
// subject has no next message, such as the result of a job, can ask for this
// interface and go to read the row.
//
// It is not on messaging.Subscription. A subscription that only this carrier can
// answer for does not belong on the interface of every carrier, and a wider
// interface would make every consumer claim a guarantee that it does not have.
type DropCounter interface {
	Dropped() uint64
}

// Dropped reports how many broadcasts this subscription lost because its handler
// was too far behind. It only grows.
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
		s.bus.lose(stageSubscription)
		xlog.Error("Broadcast carrier dropped a message: subscriber is not keeping up",
			"filter", s.filter, "subject", subject, "depth", deliveryQueueDepth, "dropped", s.dropped.Load())
	}
}

// Unsubscribe stops delivery to this handler and, when it was the last
// subscriber of its channel, stops listening on the channel.
func (s *subscription) Unsubscribe() error {
	var err error
	s.once.Do(func() {
		b := s.bus
		b.barrier("enter", "UNLISTEN")
		// The whole decision and its issue, as one step. See listenMu.
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
// returns the id that the notification carries in its place.
func (b *Bus) spill(ctx context.Context, subject string, payload []byte) (string, error) {
	row := BusMessage{ID: uuid.New().String(), Subject: subject, Payload: payload}
	if err := b.cfg.DB.WithContext(ctx).Create(&row).Error; err != nil {
		return "", fmt.Errorf("pgbus: spilling a broadcast on %q: %w", subject, err)
	}
	return row.ID, nil
}

// resolveSpill reads the row of a spilled broadcast. The read ends after
// fetchTimeout, because the dispatcher waits for it in the order of arrival.
func (b *Bus) resolveSpill(id string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(b.ctx, b.fetchTimeout)
	defer cancel()
	var row BusMessage
	if err := b.cfg.DB.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return row.Payload, nil
}

var _ messaging.Broadcaster = (*Bus)(nil)
