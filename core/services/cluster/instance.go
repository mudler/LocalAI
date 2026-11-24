package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrInstanceNotFound means no row exists for the requested instance ID. A
// caller uses it to decide between registering again and retrying.
var ErrInstanceNotFound = errors.New("cluster: instance not found")

// Instance is one live frontend replica, keyed by the ID that the replica chose
// for itself.
type Instance struct {
	ID string `gorm:"primaryKey;size:36" json:"id"`
	// Development builds include git-describe output and a full commit hash,
	// which is longer than 64 bytes after enough commits.
	Version  string    `gorm:"size:255" json:"version"`
	LastSeen time.Time `gorm:"index" json:"last_seen"`
	// AdvertisedAddr is the host and port at which the other replicas dial this
	// one. It is empty for a replica that publishes none, which no peer can
	// reach.
	AdvertisedAddr string `gorm:"size:255" json:"advertised_addr,omitempty"`
	// PeerTokenHash is the SHA-256 of the peer credential of this replica, as
	// hex. A peer that dials this replica proves who it is by presenting the
	// secret behind this hash. It is empty for a replica that has not published
	// one, and an empty hash authorises nobody.
	PeerTokenHash string `gorm:"size:64" json:"-"`
}

// LiveInstance is an Instance as ListLive reads it.
type LiveInstance struct {
	Instance
}

// Registry reads and writes the instances table and the table of worker
// connections.
type Registry struct {
	db *gorm.DB
}

// NewRegistry returns a Registry over db. The caller migrates the tables with
// Migrate.
func NewRegistry(db *gorm.DB) *Registry {
	return &Registry{db: db}
}

// Register records this replica and refreshes its LastSeen. It upserts on the
// primary key and does not delete and insert, so a concurrent reader of the
// live set never sees a live replica as missing.
func (r *Registry) Register(ctx context.Context, id, version string) error {
	return r.RegisterPeer(ctx, id, version, "", "")
}

// RegisterPeer is Register for a replica that other replicas dial. The address
// and the hash of the peer credential go in the same statement as the rest of the
// row. A replica that had an address and no identity, even for a moment, would be
// one that every peer refuses.
//
// An empty address or hash leaves the stored value alone on an update, so a
// replica that publishes none cannot wipe what an earlier registration wrote.
func (r *Registry) RegisterPeer(ctx context.Context, id, version, advertisedAddr, peerTokenHash string) error {
	// The database stamps last_seen and not this process. Liveness is compared
	// across replicas, so it must be measured on the one clock they share. With
	// one clock per replica, the real window becomes the window minus the clock
	// error of the writer and the reader, and a healthy peer can look dead.
	row := map[string]any{
		"id":        id,
		"version":   version,
		"last_seen": gorm.Expr("now()"),
	}
	update := map[string]any{
		"version":   version,
		"last_seen": gorm.Expr("now()"),
	}
	if advertisedAddr != "" {
		row["advertised_addr"] = advertisedAddr
		update["advertised_addr"] = advertisedAddr
	}
	if peerTokenHash != "" {
		row["peer_token_hash"] = peerTokenHash
		update["peer_token_hash"] = peerTokenHash
	}
	if err := r.db.WithContext(ctx).Model(&Instance{}).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(update),
	}).Create(row).Error; err != nil {
		return fmt.Errorf("registering instance %q: %w", id, err)
	}
	return nil
}

// Get returns one instance, or ErrInstanceNotFound if it is not registered. It
// does not ask if the replica is alive.
func (r *Registry) Get(ctx context.Context, id string) (*Instance, error) {
	var inst Instance
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&inst).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("getting instance %q: %w", id, ErrInstanceNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting instance %q: %w", id, err)
	}
	return &inst, nil
}

// Heartbeat refreshes LastSeen for a registered instance. An unknown ID is an
// error and not an insert, because the caller must then register again with
// the full row.
func (r *Registry) Heartbeat(ctx context.Context, id string) error {
	// gorm reports no error when the Where clause matches nothing, so the miss
	// is read from RowsAffected.
	res := r.db.WithContext(ctx).Model(&Instance{}).
		Where("id = ?", id).
		Update("last_seen", gorm.Expr("now()"))
	if res.Error != nil {
		return fmt.Errorf("heartbeating instance %q: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("heartbeating instance %q: %w", id, ErrInstanceNotFound)
	}
	return nil
}

// instanceIsLive is the one predicate that decides whether a replica is alive.
// Its single bind parameter is the window in seconds. Every reader of that fact
// is written with it: ListLive lists the replicas it selects, Owner refuses an
// owner that it rejects, and ReapStale deletes its negation. Two spellings of one
// fact drift apart, and the drift looks like a relay to a replica that one
// query calls dead and another calls alive.
//
// The column has a table name because Owner reads it across a join, where a
// bare last_seen is ambiguous.
//
// The database computes the cutoff for the same reason it stamps last_seen: a
// reader's own clock must not decide whether another replica is alive.
const instanceIsLive = `instances.last_seen > now() - make_interval(secs => ?)`

// ListLive returns the replicas that the deployment considers alive, ordered by
// id. It uses instanceIsLive, the one definition of alive.
func (r *Registry) ListLive(ctx context.Context, within time.Duration) ([]LiveInstance, error) {
	var rows []Instance
	err := r.db.WithContext(ctx).Raw(
		`SELECT instances.* FROM instances WHERE `+instanceIsLive+` ORDER BY instances.id`, within.Seconds()).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listing the live instances: %w", err)
	}
	out := make([]LiveInstance, len(rows))
	for i, rw := range rows {
		out[i] = LiveInstance{Instance: rw}
	}
	return out, nil
}
