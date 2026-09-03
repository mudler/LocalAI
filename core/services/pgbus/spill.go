// SPDX-License-Identifier: MIT

package pgbus

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// spillRetention is how long a spilled broadcast stays readable after it was
// published. It is generous against the delivery it has to survive, which is
// one notification and one primary-key SELECT, and short enough that a busy
// deployment does not accumulate LLM outputs in this table.
const spillRetention = 5 * time.Minute

// spillSweepInterval is how often each replica retires what has aged out. Every
// replica sweeps; the DELETE is idempotent and a replica that is down must not
// leave the table growing.
const spillSweepInterval = time.Minute

// SpillSweepSQL retires spilled broadcasts older than the retention it is given.
//
// The cutoff is computed by the DATABASE and never in Go, and that is the whole
// point of holding the statement here where a spec can read it. Replicas do not
// share a clock: a replica running minutes ahead would compute a cutoff in the
// future and delete rows its peers have not read yet, and a replica running
// behind would never delete anything. now() is the one clock every replica
// agrees on, because it is the clock the rows were written by.
//
// PostgreSQL-only, like everything else in this package. New refuses a handle on
// any other dialect, which is where that is guarded.
const SpillSweepSQL = "DELETE FROM bus_messages WHERE created_at < now() - make_interval(secs => ?)"

// BusMessage is one broadcast too large to travel in a notification.
type BusMessage struct {
	ID        string `gorm:"primaryKey;size:36"`
	Subject   string `gorm:"size:255;index"`
	Payload   []byte
	CreatedAt time.Time `gorm:"index"`
}

// Migrate creates the spill table. It is separate from New because a deployment
// migrates once at boot while every replica opens a carrier.
func Migrate(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).AutoMigrate(&BusMessage{})
}

// SweepSpill deletes spilled broadcasts older than retention.
func SweepSpill(ctx context.Context, db *gorm.DB, retention time.Duration) error {
	return db.WithContext(ctx).Exec(SpillSweepSQL, retention.Seconds()).Error
}
