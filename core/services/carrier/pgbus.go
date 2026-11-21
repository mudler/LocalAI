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

// Fanout is the PostgreSQL bus: the broadcaster, the reconnect hook the holder
// reads, the count of lost broadcasts and the close. Every frontend and chat
// worker holds one, because it is the floor of the broadcaster holder.
type Fanout struct {
	// Broadcaster delivers through LISTEN and NOTIFY.
	Broadcaster messaging.Broadcaster
	// OnReconnect registers a callback that runs after the LISTEN connection was
	// found again.
	OnReconnect func(func())
	// Dropped reports how many broadcasts this replica received and lost.
	Dropped func() uint64
	// Close ends the LISTEN connection.
	Close func()
}

// Bus returns the fan-out as the floor of the broadcaster holder.
func (f *Fanout) Bus() *Bus {
	return &Bus{Name: BusPostgres, Broadcaster: f.Broadcaster, OnReconnect: f.OnReconnect, Close: f.Close}
}

// NewPgbusFanout opens the LISTEN connection and returns the fan-out member.
//
// It is the one place that opens the connection. Every frontend and chat worker
// calls it once at start, whatever else the cluster has, and closes it when it
// stops. A LISTEN session needs a direct connection to the database, or a pooler
// in session mode.
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
		Dropped:     bus.Dropped,
		Close:       bus.Close,
	}, nil
}
