package pgbus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mudler/xlog"
)

// DefaultQueueDepth bounds how many notifications may wait for the resolver
// before this carrier drops them.
//
// It is a bound and not a blocking channel, on purpose. PostgreSQL keeps the
// notifications that were not delivered in one queue for the whole server, and
// it kills a session whose queue the client does not drain. A listener that
// blocked on a slow resolver would lose its connection, and with it every later
// broadcast. That is unbounded loss taken on to avoid bounded loss, and while it
// lasts it can block COMMIT for every other publisher on the server.
const DefaultQueueDepth = 1024

// DefaultSpillRetention is how long a spilled row outlives its notification
// before the purge loop deletes it.
//
// It is long compared with the delivery that it has to survive, which is one
// notification and one SELECT on the primary key, and short enough that a busy
// deployment does not collect the outputs of models in this table.
const DefaultSpillRetention = 10 * time.Minute

// listenPollInterval bounds how long a LISTEN or UNLISTEN waits for the listener
// goroutine to leave WaitForNotification and run it.
//
// It bounds registration and never delivery: a notification wakes
// WaitForNotification at once. The connection survives the deadline, because pgx
// implements it as a read deadline and not as a close, so this costs one system
// call for each interval.
const listenPollInterval = 25 * time.Millisecond

// subscribeTimeout bounds Subscribe when the listener is dialling a database
// that went away, so that a caller gets an error and not a goroutine that never
// returns.
const subscribeTimeout = 30 * time.Second

// applicationNamePrefix marks the LISTEN sessions of this carrier in
// pg_stat_activity. An operator who counts the listeners, and a spec that must
// drop the connection of one carrier and not of its peer, need a session that
// they can name.
const applicationNamePrefix = "localai_pgbus_"

// listenCmd is a LISTEN or UNLISTEN that is passed to the goroutine that owns the
// connection. The connection is not safe for concurrent use, and the listener
// goroutine waits in WaitForNotification most of the time, so registrations are
// queued to it and are not run against the connection.
type listenCmd struct {
	sql  string
	done chan error
}

// inbound is a notification as it came off the connection, before it is decoded,
// resolved and dispatched.
type inbound struct {
	channel string
	payload string
}

// Dropped reports how many broadcasts this replica received and lost: in the
// queue between the listener and the resolver, in the queue of a handler, or
// because a notification could not be decoded or resolved. It only grows. The
// same events are counted, by stage, in localai_pgbus_dropped_total.
//
// It is for reports. Nothing may decide on it. A lost broadcast is a fact about a
// frontend, while the conditions that a scheduler acts on are all facts about a
// worker. A reaper that read it would report a failure of the carrier as the
// absence of a worker.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }

// OnReconnect registers a callback that runs after the LISTEN connection was
// opened again and every channel was listened again.
//
// Consumers find it through an optional interface (interface{ OnReconnect(func()) })
// and not through messaging.Broadcaster, so a carrier that cannot reconnect does
// not have to say that it can. Nothing fails to compile if the call is removed
// from here: a map that is kept in step through broadcasts would stop converging
// without any sign, so the spec in listener_test.go is the only guard.
//
// A nil callback is ignored.
func (b *Bus) OnReconnect(cb func()) {
	if cb == nil {
		return
	}
	b.cbMu.Lock()
	b.reconnectCbs = append(b.reconnectCbs, cb)
	b.cbMu.Unlock()
}

// runReconnectCallbacks runs the registered callbacks, each on a goroutine of its
// own.
//
// They are detached on purpose. This runs on the goroutine that owns the LISTEN
// connection, and the callbacks read a whole durable source back from the
// database. To run one inline would put a query on the path whose only job is to
// keep the queue of PostgreSQL drained, and a callback that never returned would
// leave the carrier deaf for good.
//
// The slice is copied under the lock, so a callback that registers another does
// not deadlock.
func (b *Bus) runReconnectCallbacks() {
	b.cbMu.Lock()
	cbs := append([]func(){}, b.reconnectCbs...)
	b.cbMu.Unlock()
	for _, cb := range cbs {
		go cb()
	}
}

// dial opens a LISTEN connection with the application name of this carrier.
//
// The DSN is parsed again for each dial. pgx may record the state of a
// connection in the config that it receives, and to use one config for several
// dials is not part of its contract.
func (b *Bus) dial(ctx context.Context) (*pgx.Conn, error) {
	return dial(ctx, b.cfg.DSN, b.appName)
}

func dial(ctx context.Context, dsn, appName string) (*pgx.Conn, error) {
	connCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgbus: parsing the LISTEN connection string: %w", err)
	}
	if connCfg.RuntimeParams == nil {
		connCfg.RuntimeParams = map[string]string{}
	}
	connCfg.RuntimeParams["application_name"] = appName
	return pgx.ConnectConfig(ctx, connCfg)
}

// command passes a LISTEN or UNLISTEN to the goroutine that owns the connection
// and waits for it. To return before the server acknowledged it would lose every
// message published in the gap.
//
// A Subscribe that returned is therefore a registration that the server
// acknowledged. This holds when it issued nothing because the channel was
// already listened: because of listenMu, the Subscribe that did issue the LISTEN
// had its acknowledgement before this one could see its registration.
func (b *Bus) command(sql string) error {
	cmd := listenCmd{sql: sql, done: make(chan error, 1)}
	timeout := time.NewTimer(subscribeTimeout)
	defer timeout.Stop()

	select {
	case b.cmds <- cmd:
	case <-b.ctx.Done():
		return errors.New("pgbus: the carrier is closed")
	case <-timeout.C:
		return fmt.Errorf("pgbus: %q timed out waiting for the listen connection", sql)
	}
	select {
	case err := <-cmd.done:
		return err
	case <-b.ctx.Done():
		return errors.New("pgbus: the carrier is closed")
	case <-timeout.C:
		return fmt.Errorf("pgbus: %q timed out on the listen connection", sql)
	}
}

// listen owns the pinned connection for the life of the Bus. It is the only
// goroutine that uses it, which is what lets many callers share a connection that
// is not safe for concurrent use.
//
// A notification is copied off the connection here and is never processed here.
// Between the wait and offer, the loop must not do anything that can block on a
// database, on a handler or on the network, because whatever it waited for would
// hold the notifications of this session in a queue that it shares with every
// other publisher of the server.
func (b *Bus) listen(conn *pgx.Conn) {
	defer close(b.listenerDone)
	defer func() {
		b.connected.Store(false)
		// A fresh context, because b.ctx is already cancelled when this runs and
		// the close still has to reach the server.
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	for {
		if b.ctx.Err() != nil {
			return
		}
		// Registrations first: a subscriber that has waited must not be held up
		// behind a poll interval that has just started.
		select {
		case cmd := <-b.cmds:
			_, err := conn.Exec(b.ctx, cmd.sql)
			cmd.done <- err
			continue
		default:
		}

		waitCtx, cancel := context.WithTimeout(b.ctx, listenPollInterval)
		n, err := conn.WaitForNotification(waitCtx)
		deadline := waitCtx.Err() != nil
		cancel()

		switch {
		case b.ctx.Err() != nil:
			return
		case deadline:
			// The usual case: nothing arrived in the poll window. pgx keeps the
			// connection when its read deadline passes, so this is not a failure.
			continue
		case err != nil:
			xlog.Warn("Broadcast carrier lost its listen connection, dialling again", "error", err)
			replacement, ok := b.redial(conn)
			if !ok {
				return
			}
			conn = replacement
			continue
		}
		b.offer(n.Channel, n.Payload)
	}
}

// offer hands a notification to the resolver. It never blocks: the only job of
// the listener is to keep the queue of PostgreSQL drained.
//
// The drop is counted as well as logged. The log is on the replica that took the
// loss, which is not always the party that needs to know, and a counter that only
// grows is what a spec and an operator can both read.
func (b *Bus) offer(channel, payload string) {
	select {
	case b.inbound <- inbound{channel: channel, payload: payload}:
	default:
		b.lose(stageListener)
		xlog.Error("Broadcast carrier dropped a notification: the resolver is not keeping up",
			"channel", channel, "depth", cap(b.inbound), "dropped", b.dropped.Load())
	}
}

// spillFetchers is how many spilled rows one replica reads at the same time.
//
// A spilled broadcast costs one SELECT on every replica, and with one reader the
// SELECTs are in a line: a replica that reads a row in a millisecond cannot take
// more than a thousand spilled broadcasts in a second. Measured with 64 KiB
// payloads, a replica with one reader lost 10% of the broadcasts at 2,000 a
// second and 84% at 4,000. With several readers the waits overlap, and the order
// of delivery is unchanged because the dispatcher takes the notifications in the
// order they arrived.
//
// Each reader holds one connection of the pool of the replica while it reads.
const spillFetchers = 8

// pipelineDepth is how many notifications may be between the resolver and the
// dispatcher. It is at least spillFetchers, so that every reader has work.
const pipelineDepth = 4 * spillFetchers

// resolve decodes the notifications that the listener copied, in the order in
// which they arrived, and starts the read of every spilled row. The dispatcher
// delivers them in the same order.
//
// This half may block. A notification is processed here and never on the
// connection that it arrived on, so a slow SELECT here costs this replica a
// bounded loss through offer and costs the server nothing.
func (b *Bus) resolve() {
	defer close(b.resolverDone)
	ordered := make(chan *pending, pipelineDepth)
	dispatched := make(chan struct{})
	go b.dispatch(ordered, dispatched)
	defer func() {
		close(ordered)
		<-dispatched
	}()

	readers := make(chan struct{}, spillFetchers)
	for {
		select {
		case <-b.ctx.Done():
			return
		case in := <-b.inbound:
			p := b.decode(in)
			if p.ok && p.n.SpillID != "" {
				select {
				case readers <- struct{}{}:
				case <-b.ctx.Done():
					return
				}
				go func() {
					defer func() { <-readers }()
					b.fetch(p)
				}()
			}
			select {
			case ordered <- p:
			case <-b.ctx.Done():
				return
			}
		}
	}
}

// dispatch delivers the pending notifications in the order in which they
// arrived. A spilled one that is not ready holds the ones behind it: the order
// of the messages of one subject is kept, and the readers go on in the meantime.
func (b *Bus) dispatch(ordered <-chan *pending, done chan<- struct{}) {
	defer close(done)
	for p := range ordered {
		select {
		case <-p.ready:
			b.deliver(p)
		case <-b.ctx.Done():
			return
		}
	}
}

// redial replaces a dead LISTEN connection and restores every registration on it.
// Without the restore, the carrier comes back deaf, which looks like a deployment
// where nobody publishes any more.
func (b *Bus) redial(dead *pgx.Conn) (*pgx.Conn, bool) {
	b.connected.Store(false)
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = dead.Close(closeCtx)
	cancel()

	backoff := 100 * time.Millisecond
	for {
		if b.ctx.Err() != nil {
			return nil, false
		}
		conn, err := b.dial(b.ctx)
		if err == nil {
			if err = b.relisten(conn); err == nil {
				b.connected.Store(true)
				b.metrics.reconnect()
				xlog.Info("Broadcast carrier reconnected")
				// After the LISTEN and never before it. A callback reads from the
				// durable source, and one that ran while the channels were not
				// registered would converge against a carrier that cannot yet
				// tell it about the next change.
				b.runReconnectCallbacks()
				return conn, true
			}
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = conn.Close(closeCtx)
			cancel()
		}
		select {
		case <-b.ctx.Done():
			return nil, false
		case <-time.After(backoff):
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

// relisten registers again every channel that still has a subscriber.
//
// A carrier that reconnected without this is connected and deaf, and no error and
// no log line says so.
func (b *Bus) relisten(conn *pgx.Conn) error {
	b.mu.Lock()
	channels := make([]string, 0, len(b.subs))
	for channel, subs := range b.subs {
		if len(subs) > 0 {
			channels = append(channels, channel)
		}
	}
	b.mu.Unlock()

	for _, channel := range channels {
		if _, err := conn.Exec(b.ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
			return err
		}
	}
	return nil
}

// purge deletes the spilled rows that every replica has had time to read.
//
// Every replica purges. The DELETE is idempotent, and a deployment in which one
// replica purged would stop deleting when that replica went down.
func (b *Bus) purge() {
	defer close(b.purgerDone)
	interval := b.cfg.SweepInterval
	if interval <= 0 {
		// Half the retention, so a row is deleted within one retention after it
		// ages out and not within two.
		interval = b.retention / 2
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			if _, err := b.purgeBefore(b.ctx, b.retention); err != nil && b.ctx.Err() == nil {
				xlog.Warn("Broadcast carrier could not retire spilled messages", "error", err)
			}
		}
	}
}
