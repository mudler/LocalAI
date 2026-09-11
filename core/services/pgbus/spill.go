// SPDX-License-Identifier: MIT

package pgbus

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

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
//
// For a caller that has a handle but no carrier, which is why it takes a
// *gorm.DB. The carrier's own purge loop calls PurgeBefore instead, because it
// needs the count; both run the one statement in SpillSweepSQL.
func SweepSpill(ctx context.Context, db *gorm.DB, retention time.Duration) error {
	return db.WithContext(ctx).Exec(SpillSweepSQL, retention.Seconds()).Error
}

// PurgeBefore deletes this carrier's spilled rows older than olderThan and
// returns how many it deleted.
//
// The count is the point. SweepSpill answers "did the statement run", which a
// purge loop that is retiring nothing answers just as happily, and a spill table
// that grows without bound is a disk that fills long after the change that
// caused it. This is also what the purge loop calls, so the count an operator
// can ask for and the deletion the carrier performs are the same statement.
func (b *Bus) PurgeBefore(ctx context.Context, olderThan time.Duration) (int64, error) {
	res := b.cfg.DB.WithContext(ctx).Exec(SpillSweepSQL, olderThan.Seconds())
	if res.Error != nil {
		return 0, fmt.Errorf("pgbus: retiring spilled broadcasts: %w", res.Error)
	}
	return res.RowsAffected, nil
}
