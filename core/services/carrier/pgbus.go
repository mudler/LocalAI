package carrier

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/metric"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/pgbus"
)

// PgbusOptions is what NewPgbusFanout needs.
type PgbusOptions struct {
	// DB is the pooled handle of the cluster database.
	DB *gorm.DB
	// DSN is the connection string of the LISTEN connection. It is the string of
	// the database that DB points to, because a LISTEN connection cannot come
	// from a pool.
	DSN string
	// Meter receives the counters of the carrier. Nil means the global meter.
	Meter metric.Meter
}

// Fanout is the fan-out member of a Set for a carrier that has no NATS server:
// the broadcaster, the reconnect hook the holder reads, a ready check and the
// close. The other members of the set come from the other parts of the carrier.
type Fanout struct {
	// Broadcaster delivers through LISTEN and NOTIFY.
	Broadcaster messaging.Broadcaster
	// OnReconnect registers a callback that runs after the LISTEN connection was
	// found again. It is the OnReconnect of the Set.
	OnReconnect func(func())
	// Ready reports an error when the LISTEN connection is down. The report that
	// a replica sends for a change of carrier reads it. A caller must not decide
	// on it anything about a worker.
	Ready func() error
	// Dropped reports how many broadcasts this replica received and lost.
	Dropped func() uint64
	// Close ends the LISTEN connection. It is the Close of the Set.
	Close func()
}

// ProbeListen reports why a LISTEN connection to the database cannot be opened
// from this process, or nil when it can. It opens and closes one session.
func ProbeListen(ctx context.Context, dsn string) error {
	if dsn == "" {
		return errors.New("the connection string of the database is not known, so no LISTEN connection can be opened")
	}
	return pgbus.ProbeListen(ctx, dsn)
}

// NewPgbusFanout opens the LISTEN connection and returns the fan-out member.
//
// It is the one place that opens the connection. A deployment whose active
// carrier is NATS does not call it until a change of carrier asks for it, so it
// holds no LISTEN session, and it closes the member after the drain, so it holds
// none again. A LISTEN session needs a direct connection to the database, or a
// pooler in session mode.
func NewPgbusFanout(ctx context.Context, o PgbusOptions) (*Fanout, error) {
	if o.DB == nil {
		return nil, errors.New("pgbus fan-out needs a database handle")
	}
	if o.DSN == "" {
		return nil, errors.New("pgbus fan-out needs the connection string of the database")
	}
	if err := advisorylock.WithLockCtx(ctx, o.DB, advisorylock.KeySchemaMigrate, func() error {
		return pgbus.Migrate(ctx, o.DB)
	}); err != nil {
		return nil, fmt.Errorf("migrating the spill table: %w", err)
	}
	bus, err := pgbus.New(ctx, pgbus.Config{DSN: o.DSN, DB: o.DB, Meter: o.Meter})
	if err != nil {
		return nil, err
	}
	return &Fanout{
		Broadcaster: bus,
		OnReconnect: bus.OnReconnect,
		Ready: func() error {
			if !bus.IsConnected() {
				return errors.New("the LISTEN connection to the database is down")
			}
			return nil
		},
		Dropped: bus.Dropped,
		Close:   bus.Close,
	}, nil
}
