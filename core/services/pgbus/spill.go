package pgbus

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// spillSweepSQL deletes the spilled broadcasts that are older than the
// retention it is given.
//
// The database computes the cutoff and Go does not. Replicas do not share a
// clock: a replica that runs minutes ahead would compute a cutoff in the future
// and delete rows that its peers have not read yet, and a replica that runs
// behind would never delete anything. now() is the one clock that every replica
// agrees on, because it is the clock that stamped the rows.
//
// It is PostgreSQL only, like the rest of this package. New refuses a handle on
// any other dialect.
const spillSweepSQL = "DELETE FROM bus_messages WHERE created_at < now() - make_interval(secs => ?)"

// BusMessage is one broadcast that is too large for a notification.
type BusMessage struct {
	ID        string `gorm:"primaryKey;size:36"`
	Subject   string `gorm:"size:255;index"`
	Payload   []byte
	CreatedAt time.Time `gorm:"index"`
}

// Migrate creates the spill table. It is separate from New because a deployment
// migrates once while every replica opens a carrier.
func Migrate(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).AutoMigrate(&BusMessage{})
}

// purgeBefore deletes the spilled rows older than olderThan and returns how many
// it deleted. The count is the point: a purge loop that retires nothing reports
// success as well, and a spill table that grows without bound fills the disk
// long after the change that caused it.
func (b *Bus) purgeBefore(ctx context.Context, olderThan time.Duration) (int64, error) {
	res := b.cfg.DB.WithContext(ctx).Exec(spillSweepSQL, olderThan.Seconds())
	if res.Error != nil {
		return 0, fmt.Errorf("pgbus: retiring spilled broadcasts: %w", res.Error)
	}
	b.metrics.purge(res.RowsAffected)
	return res.RowsAffected, nil
}
