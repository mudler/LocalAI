package auth

import (
	"sync"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// LastActiveThrottle is the least time between two writes of one person's
// last_active_at. A busy API client sends many requests a second, and a write
// for each would turn a read-mostly table into a write storm.
const LastActiveThrottle = 5 * time.Minute

// lastActiveSeen remembers, per process, when a person's activity was last
// written, so a burst of requests costs one comparison and not one query.
// The conditional update below keeps several frontends on one database to one
// write per window as well.
var lastActiveSeen sync.Map // user id -> time.Time

// TouchLastActive records that the person made an authenticated request now.
// It writes at most once per LastActiveThrottle for a person, in this process
// and across processes that share the database. It never fails a request: a
// failed write is logged and skipped.
func TouchLastActive(db *gorm.DB, user *User) {
	TouchLastActiveAt(db, user, time.Now())
}

// TouchLastActiveAt is TouchLastActive with the clock given, for tests.
func TouchLastActiveAt(db *gorm.DB, user *User, now time.Time) {
	if db == nil || user == nil || user.ID == "" {
		return
	}
	// Compare and store in UTC. SQLite keeps timestamps as text, so the
	// window check in the WHERE below is a string comparison: a local time
	// with an offset against a value stored in UTC gives the wrong answer
	// whenever the server runs outside UTC (TZ=Europe/Berlin wrote inside the
	// window).
	now = now.UTC()
	if seen, ok := lastActiveSeen.Load(user.ID); ok && now.Sub(seen.(time.Time)) < LastActiveThrottle {
		return
	}
	if user.LastActiveAt != nil && now.Sub(*user.LastActiveAt) < LastActiveThrottle {
		lastActiveSeen.Store(user.ID, *user.LastActiveAt)
		return
	}
	lastActiveSeen.Store(user.ID, now)
	// UpdateColumn leaves updated_at alone: activity is not an edit of the
	// account. The WHERE makes the write a no-op when another request or
	// frontend wrote inside the window.
	err := db.Model(&User{}).
		Where("id = ? AND (last_active_at IS NULL OR last_active_at < ?)", user.ID, now.Add(-LastActiveThrottle)).
		UpdateColumn("last_active_at", now).Error
	if err != nil {
		xlog.Debug("could not record last activity", "error", err)
		return
	}
	user.LastActiveAt = &now
}

// ForgetLastActive drops the in-process throttle entry of a person. A deleted
// account does not leave a stale entry.
func ForgetLastActive(userID string) { lastActiveSeen.Delete(userID) }
