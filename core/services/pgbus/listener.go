// SPDX-License-Identifier: MIT

package pgbus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mudler/xlog"
)

// DefaultQueueDepth bounds how many notifications may be waiting for the
// resolver before this carrier starts dropping them.
//
// A bound and not a blocking channel, deliberately. PostgreSQL holds undelivered
// notifications in a shared, server-wide async queue, and it kills a session
// whose queue the client is not draining. A listener that blocked on a slow
// resolver would therefore lose the connection and with it EVERY subsequent
// broadcast, which is unbounded loss taken on to avoid bounded loss, and it can
// block COMMIT for every other publisher on that server while it lasts.
const DefaultQueueDepth = 1024

// DefaultSpillRetention is how long a spilled row outlives its notification
// before the purge loop deletes it.
//
// It is generous against the delivery it has to survive, which is one
// notification and one primary-key SELECT, and short enough that a busy
// deployment does not accumulate LLM outputs in this table.
const DefaultSpillRetention = 10 * time.Minute

// listenPollInterval bounds how long a LISTEN or UNLISTEN waits for the
// listener goroutine to come off WaitForNotification and run it.
//
// It is a bound on REGISTRATION, never on delivery: a notification wakes
// WaitForNotification immediately. The connection survives the deadline, which
// pgx implements as a read deadline rather than a close (pgconn's peekMessage
// closes on anything except a net timeout), so this costs one syscall per
// interval and nothing else.
const listenPollInterval = 25 * time.Millisecond

// subscribeTimeout bounds Subscribe when the listener is busy redialling a
// database that has gone away, so a caller gets an error instead of a
// goroutine that never returns.
const subscribeTimeout = 30 * time.Second

// applicationNamePrefix marks this carrier's LISTEN sessions in
// pg_stat_activity. An operator counting listeners, and a spec that has to drop
// ONE carrier's connection without touching its peer's, both need a session they
// can name rather than guess at.
const applicationNamePrefix = "localai_pgbus_"

// listenCmd is a LISTEN or UNLISTEN handed to the goroutine that owns the
// connection. The connection is not concurrency safe and the listener goroutine
// is parked in WaitForNotification most of the time, so registrations are
// queued to it rather than run against it.
type listenCmd struct {
	sql  string
	done chan error
}

// inbound is one notification as it came off the connection, before it is
// decoded, resolved and dispatched.
type inbound struct {
	channel string
	payload string
}

// ApplicationName is the application_name this carrier's LISTEN session reports
// in pg_stat_activity. It is unique per carrier so two Buses on one database are
// distinguishable, which is what lets an operator see how many replicas are
// listening and lets a spec terminate exactly one of them.
func (b *Bus) ApplicationName() string { return b.appName }

// QueueDepth is the effective notification queue depth, after Config.Queue has
// been defaulted. Reported so a deployment can log what it actually got rather
// than what it thinks it configured.
func (b *Bus) QueueDepth() int { return cap(b.inbound) }

// SpillRetention is the effective spill retention, after Config.Retention has
// been defaulted.
func (b *Bus) SpillRetention() time.Duration { return b.retention }

// Dropped reports how many notifications this carrier discarded because the
// resolver was too far behind. It only ever grows.
//
// Dropped and IsConnected are consumed by SPECS AND LOGGING ONLY, and neither is
// on messaging.Broadcaster. No production branch may read either: "this
// replica's listener is down" and "this replica is behind" are facts about a
// FRONTEND, while the four conditions a scheduler acts on are all facts about a
// WORKER. A reaper that consulted them would be reporting a carrier failure as a
// worker's absence, which is the collapse this programme exists to prevent.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }

// OnReconnect registers a callback invoked after the LISTEN connection has been
// re-established AND every channel has been re-listened.
//
// It is consumed through an optional interface assertion
// (interface{ OnReconnect(func()) }) in syncstate.SyncedMap.Start rather than
// through messaging.Broadcaster, so a carrier that cannot reconnect is not
// forced to claim it can. That assertion is also why nothing here fails to
// compile if the invocation is deleted: a SyncedMap whose only convergence
// mechanism is this callback would simply stop converging, in silence, so the
// spec in listener_test.go is the only guard there is.
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

// runReconnectCallbacks invokes the registered callbacks, each on a goroutine of
// its own.
//
// Detached on purpose. This runs on the goroutine that owns the LISTEN
// connection, and the callbacks are re-hydrations: a SyncedMap reads its whole
// durable source back. Running one of those inline would put a database query on
// the path whose only job is to keep PostgreSQL's async queue draining, which is
// the failure this task exists to remove, and a callback that never returned
// would leave the carrier deaf for good.
//
// The slice is copied under the lock so a callback that registers another cannot
// deadlock.
func (b *Bus) runReconnectCallbacks() {
	b.cbMu.Lock()
	cbs := append([]func(){}, b.reconnectCbs...)
	b.cbMu.Unlock()
	for _, cb := range cbs {
		// Pinned by "does not run a reconnect callback on the goroutine that
		// owns the connection": deleting the `go` compiles and stays green
		// everywhere else.
		go cb()
	}
}

// dial opens a LISTEN connection carrying this carrier's application name.
//
// The DSN is re-parsed per dial rather than a parsed config being kept, because
// pgx may record connection state on the config it is handed and reusing one
// across redials is not part of its contract.
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

// command hands a LISTEN or UNLISTEN to the goroutine that owns the connection
// and waits for it. Returning before the server had acknowledged it would lose
// every message published in the gap.
//
// A Subscribe that has returned is therefore always a registration the server
// has acknowledged, including the case where this Subscribe issued nothing
// because the channel was already listened: listenMu means the Subscribe that
// DID issue the LISTEN had already been acknowledged before this one could see
// its registration.
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

// listen owns the pinned connection for the life of the Bus: it is the only
// goroutine that touches it, which is what makes a connection that is not
// concurrency safe usable from many callers.
//
// A notification is copied off the connection here and NEVER processed here. The
// loop below must reach offer with nothing in between that can block on a
// database, on a handler or on the network, because whatever it waited for
// PostgreSQL would be holding notifications for this session in a queue it
// shares with every other publisher on the server.
func (b *Bus) listen(conn *pgx.Conn) {
	defer close(b.listenerDone)
	defer func() {
		b.connected.Store(false)
		// A background context, because b.ctx is already cancelled by the time
		// this runs and the close still has to reach the server.
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	for {
		if b.ctx.Err() != nil {
			return
		}
		// Registrations first: a subscriber that has been waiting must not be
		// held up behind a poll interval that has just restarted.
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
			// The ordinary case: nothing arrived inside the poll window. pgx
			// implements the deadline as a read deadline and keeps the
			// connection, so this is not a failure.
			continue
		case err != nil:
			xlog.Warn("Broadcast carrier lost its listen connection, redialling", "error", err)
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

// offer hands a notification to the resolver. It never blocks: the listener's
// only job is to keep PostgreSQL's async queue draining.
//
// The drop is counted as well as logged. The log lands on the replica that took
// the loss, which is not always the party that needs to know, and a counter that
// only ever grows is the one thing a spec and an operator can both read.
func (b *Bus) offer(channel, payload string) {
	select {
	case b.inbound <- inbound{channel: channel, payload: payload}:
	default:
		b.dropped.Add(1)
		xlog.Error("Broadcast carrier dropped a notification: the resolver is not keeping up",
			"channel", channel, "depth", cap(b.inbound), "dropped", b.dropped.Load())
	}
}

// resolve is where a spilled broadcast is read back and where every notification
// is dispatched. Both are off the listener on purpose, and both are on ONE
// goroutine, so a spilled message and an inline one on the same subject keep the
// order they were published in.
//
// This is the half that is allowed to block. A notification is processed here
// and never on the connection it arrived on, so a slow SELECT here costs this
// replica bounded loss through offer, and costs the SERVER nothing.
func (b *Bus) resolve() {
	defer close(b.resolverDone)
	for {
		select {
		case <-b.ctx.Done():
			return
		case in := <-b.inbound:
			b.deliver(in.channel, in.payload)
		}
	}
}

// redial replaces a dead LISTEN connection and restores every registration on
// it. Without the restore the carrier comes back deaf, which looks exactly like
// a deployment where nobody is publishing any more.
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
				xlog.Info("Broadcast carrier reconnected")
				// After the re-LISTEN and never before it. A callback
				// re-hydrates from the durable source, and one that ran while
				// the channels were still unregistered would converge against a
				// carrier that cannot yet tell it about the next change.
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

// relisten re-registers every channel that still has a subscriber.
//
// A carrier that reconnected without this is CONNECTED AND DEAF, and there is no
// error and no log line anywhere that says so.
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

// purge retires spilled rows that every replica has had time to read.
//
// Every replica purges. The DELETE is idempotent, and a deployment where only
// one replica swept would stop retiring anything the moment that replica went
// down.
func (b *Bus) purge() {
	interval := b.cfg.SweepInterval
	if interval <= 0 {
		// Half the retention, so a row is retired within one retention of
		// ageing out rather than within two.
		interval = b.retention / 2
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			if _, err := b.PurgeBefore(b.ctx, b.retention); err != nil && b.ctx.Err() == nil {
				xlog.Warn("Broadcast carrier could not retire spilled messages", "error", err)
			}
		}
	}
}
