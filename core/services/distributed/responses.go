package distributed

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ResponseMetadataRecord is the durable row behind the responses.metadata
// SyncedMap.
//
// PayloadJSON carries the whole projection as JSON rather than one column per
// field. A column-per-field schema would be a SECOND definition of what a peer
// may act on, and the two would drift the first time the projection gains a
// field: the map would broadcast the new field and hydrate without it, so a
// replica that had reconnected would serve a different response body from one
// that had not, with nothing failing anywhere.
//
// OwnerReplica and Owner are duplicated out of the payload as indexed columns
// because they are what an operator filters on when reading this table by hand
// ("which responses did the replica that just crashed own"). No query in this
// package reads them, so they cannot drift into a second source of truth for
// what a peer acts on: only PayloadJSON is ever decoded.
type ResponseMetadataRecord struct {
	ID           string     `gorm:"primaryKey;size:64"`
	OwnerReplica string     `gorm:"size:64;index"`
	Owner        string     `gorm:"size:64;index"`
	PayloadJSON  []byte     `gorm:"type:bytea"`
	ExpiresAt    *time.Time `gorm:"index"`
	CreatedAt    time.Time  `gorm:"index"`
}

func (ResponseMetadataRecord) TableName() string { return "response_metadata" }

// ResponseMetadataStore is the durable half of cross-replica response metadata.
//
// It exists because a broadcast carrier is at most once to CONNECTED listeners.
// A replica whose listener was down while a response was created never sees the
// delta, and without a table to re-hydrate from it serves 404 for that response
// forever while its peers serve 200. Every method here reports a database
// failure as an error and never as an empty result, because "no such response"
// and "the database could not be reached" are different facts and a hydrate that
// confused them would blank the map on a transient outage.
type ResponseMetadataStore struct {
	db *gorm.DB

	// retention bounds how long a row that carries NO expiry of its own stays
	// in this table. See DefaultResponseMetadataRetention.
	retention time.Duration
}

// DefaultResponseMetadataRetention is how long a response row with no expiry of
// its own is kept.
//
// It is INDEPENDENT of the Open Responses store TTL, and that is the whole
// point. That TTL defaults to 0, documented as "no expiration", and 0 there is
// defensible: the in-memory map it governs dies with the process, so unbounded
// means "bounded by this process's lifetime and by memory an operator can see".
// A TABLE has neither bound. Inheriting that 0 here made every replicated
// response immortal on disk, so the table grew for the life of the deployment
// and a restarting replica re-hydrated its map with every response the cluster
// had ever created. Neither is a condition anything reports.
//
// So the durable projection gets a retention of its own. It is a floor on
// CROSS-REPLICA visibility and never on the response: the replica that owns a
// response keeps it in memory for exactly as long as the configured TTL says,
// and an operator who sets a TTL longer than this gets that TTL honoured,
// because a row with an expires_at is judged on that column alone.
//
// Twenty-four hours is chosen against what the row is for. It exists so a peer
// replica, or a restarted one, can answer a GET or resolve a
// previous_response_id it did not create. That is a lookup a client makes
// within minutes of the response, not a day later, and the row is metadata
// rather than the generation itself.
const DefaultResponseMetadataRetention = 24 * time.Hour

// deadResponseMetadata is the ONE definition of "this row is no longer live",
// evaluated on the DATABASE clock so every replica agrees.
//
// PurgeExpired deletes exactly the rows it matches and ListUnexpired returns
// exactly the rows it does not, spelled as NOT of this same string. Two
// separate spellings would let a hydrate resurrect a row a sweep had already
// decided was dead, or leave a row that is invisible to every reader sitting in
// the table forever, and neither reports anything.
//
// The bind parameter is the retention in seconds, and it is the only one:
// make_interval(secs => ?) turns it into an interval the SERVER subtracts from
// its own now(), so no process's clock enters the comparison.
const deadResponseMetadata = `((expires_at IS NOT NULL AND expires_at <= now()) OR ` +
	`(expires_at IS NULL AND created_at <= now() - make_interval(secs => ?)))`

// NewResponseMetadataStore creates a ResponseMetadataStore and migrates its
// table.
//
// The dialect is checked rather than assumed: ListUnexpired and PurgeExpired are
// spelled with now(), and on the SQLite single-binary path an unguarded now()
// fails at query time in a way that reads as a missing migration rather than as
// a store that was never meant to run there.
//
// The migration runs under the same advisory lock NewFineTuneStore uses, because
// several replicas start at once and concurrent AutoMigrate races.
func NewResponseMetadataStore(db *gorm.DB) (*ResponseMetadataStore, error) {
	return NewResponseMetadataStoreWithRetention(db, DefaultResponseMetadataRetention)
}

// NewResponseMetadataStoreWithRetention is NewResponseMetadataStore with an
// explicit bound on rows that carry no expiry of their own.
//
// A non-positive retention is refused rather than taken to mean "keep
// everything": unbounded is the shape this store had, it grows a table nothing
// ever sweeps, and a deployment that wants a different bound should say which
// one rather than switch the bound off.
func NewResponseMetadataStoreWithRetention(db *gorm.DB, retention time.Duration) (*ResponseMetadataStore, error) {
	if db == nil {
		return nil, fmt.Errorf("response metadata store: no database handle")
	}
	if retention <= 0 {
		return nil, fmt.Errorf("response metadata store: retention must be positive, got %s; a row with no expiry of its own would never be swept and the table would grow for the life of the deployment", retention)
	}
	if name := db.Dialector.Name(); name != "postgres" {
		return nil, fmt.Errorf("response metadata store requires PostgreSQL, this deployment runs on %q", name)
	}
	if err := advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		return db.AutoMigrate(&ResponseMetadataRecord{})
	}); err != nil {
		return nil, fmt.Errorf("migrating response_metadata: %w", err)
	}
	return &ResponseMetadataStore{db: db, retention: retention}, nil
}

// Retention is the effective bound on rows with no expiry of their own.
// Reported so a deployment can log what it actually got.
func (s *ResponseMetadataStore) Retention() time.Duration { return s.retention }

// Upsert idempotently inserts or replaces one row by primary key.
//
// created_at is deliberately NOT in the update set: the row is rewritten on
// every response state change (created, stored for background execution, status
// changed, cancelled), and updating it would make the column mean "last
// touched", which is not what an operator reading the table would take it for.
func (s *ResponseMetadataStore) Upsert(ctx context.Context, rec *ResponseMetadataRecord) error {
	if rec == nil || rec.ID == "" {
		return fmt.Errorf("response metadata upsert: record has no id")
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"owner_replica", "owner", "payload_json", "expires_at"}),
	}).Create(rec).Error
}

// Delete removes one row. Deleting a row that is not there is not an error: the
// map's Delete is broadcast to every replica and any of them may reap an expired
// entry, so a second delete for the same id is expected traffic.
func (s *ResponseMetadataStore) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&ResponseMetadataRecord{}).Error
}

// ListUnexpired returns every row that is still live: its ExpiresAt is in the
// future, or it has none and is younger than the retention. Both legs are
// measured on the DATABASE clock.
//
// The clock is the database's because every replica hydrating from this table
// must agree on which rows are live, and a Go-side cutoff makes that a property
// of whichever process asked. The predicate is deadResponseMetadata negated, so
// this and PurgeExpired cannot disagree about a row; it is dialect-guarded in
// the constructor, because now() and make_interval on the SQLite single-binary
// path read as a missing migration rather than as an error.
func (s *ResponseMetadataStore) ListUnexpired(ctx context.Context) ([]ResponseMetadataRecord, error) {
	var out []ResponseMetadataRecord
	if err := s.db.WithContext(ctx).
		Where("NOT "+deadResponseMetadata, s.retention.Seconds()).
		Order("created_at").
		Find(&out).Error; err != nil {
		return nil, fmt.Errorf("listing unexpired response metadata: %w", err)
	}
	return out, nil
}

// PurgeExpired deletes every row ListUnexpired would refuse to return and
// reports how many. It is the reason a table of ephemeral state does not grow
// forever, and it runs on the same DATABASE clock and the same predicate.
func (s *ResponseMetadataStore) PurgeExpired(ctx context.Context) (int64, error) {
	res := s.db.WithContext(ctx).
		Where(deadResponseMetadata, s.retention.Seconds()).
		Delete(&ResponseMetadataRecord{})
	if res.Error != nil {
		return 0, fmt.Errorf("purging expired response metadata: %w", res.Error)
	}
	return res.RowsAffected, nil
}
