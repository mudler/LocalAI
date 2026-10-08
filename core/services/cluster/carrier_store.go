// Package cluster holds state that every frontend replica of a distributed
// deployment shares through the database.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Carrier names a transport that carries fan-out messages, work queues and
// control requests between frontends and workers. Exactly one carrier is
// active in a cluster at a time.
type Carrier string

const (
	CarrierNATS   Carrier = "nats"
	CarrierTunnel Carrier = "tunnel"
)

func (c Carrier) valid() bool { return c == CarrierNATS || c == CarrierTunnel }

// State is where the cluster is in a change of carrier.
type State string

const (
	// StateStable: one carrier is active and nothing is changing.
	StateStable State = "stable"
	// StatePrepare: replicas build the target carrier. The active carrier is
	// unchanged.
	StatePrepare State = "prepare"
	// StateCommit: the target is now the active carrier and the previous one
	// is still attached while it drains.
	StateCommit State = "commit"
)

func (s State) valid() bool { return s == StateStable || s == StatePrepare || s == StateCommit }

var (
	// ErrNotSeeded means no replica has written the carrier row yet.
	ErrNotSeeded = errors.New("cluster carrier row does not exist")
	// ErrStaleEpoch means another writer changed the row after the caller read it.
	ErrStaleEpoch = errors.New("cluster carrier epoch is stale")
	// ErrInvalidCarrier means a carrier name is not one the cluster knows.
	ErrInvalidCarrier = errors.New("unknown cluster carrier")
	// ErrInvalidChange means a change does not describe a valid state.
	ErrInvalidChange = errors.New("invalid cluster carrier change")
)

// carrierRowID is the primary key of the only row. A check constraint keeps
// the table at one row.
const carrierRowID = 1

// CarrierRow is the one row of table cluster_carrier. Active is the carrier
// that publishes, enqueues and dials. Epoch grows with every change, so a
// reader can tell a newer row from an older one without comparing clocks.
type CarrierRow struct {
	ID            int        `gorm:"primaryKey;check:cluster_carrier_single_row,id = 1" json:"-"`
	Active        Carrier    `gorm:"size:16;not null" json:"active"`
	Epoch         int64      `gorm:"not null" json:"epoch"`
	State         State      `gorm:"size:16;not null" json:"state"`
	Target        Carrier    `gorm:"size:16;not null;default:''" json:"target,omitempty"`
	Draining      Carrier    `gorm:"size:16;not null;default:''" json:"draining,omitempty"`
	DrainingUntil *time.Time `json:"draining_until,omitempty"`
	ChangedBy     string     `gorm:"size:255" json:"changed_by"`
	// ChangedAt is stamped by the database on every change, and the age of a
	// change is read on that same clock (CarrierStore.DBNow), so that the
	// timeouts of a change need no clock that the replicas agree on.
	ChangedAt time.Time `json:"changed_at"`
	PrevEpoch int64     `json:"prev_epoch"`
	// Force records that the admin who started the change accepted that a
	// replica which is not ready, or a worker that cannot follow, is left
	// behind. It belongs to one change and is cleared by the next one.
	Force bool `gorm:"not null;default:false" json:"force,omitempty"`
	// Note says in a sentence how the last change ended, or why it did. It is
	// for operators.
	Note string `gorm:"size:512;not null;default:''" json:"note,omitempty"`
}

func (CarrierRow) TableName() string { return "cluster_carrier" }

// Change is the next state of the row. Every field is written; a field left
// empty clears the column.
type Change struct {
	Active        Carrier
	State         State
	Target        Carrier
	Draining      Carrier
	DrainingUntil *time.Time
	By            string
	Force         bool
	Note          string
}

func (c Change) validate() error {
	if !c.Active.valid() {
		return fmt.Errorf("%w: active %q: %w", ErrInvalidChange, c.Active, ErrInvalidCarrier)
	}
	if !c.State.valid() {
		return fmt.Errorf("%w: state %q", ErrInvalidChange, c.State)
	}
	if c.Target != "" && !c.Target.valid() {
		return fmt.Errorf("%w: target %q: %w", ErrInvalidChange, c.Target, ErrInvalidCarrier)
	}
	if c.Draining != "" && !c.Draining.valid() {
		return fmt.Errorf("%w: draining %q: %w", ErrInvalidChange, c.Draining, ErrInvalidCarrier)
	}
	if c.State == StateStable && c.Target != "" {
		return fmt.Errorf("%w: a stable state has no target", ErrInvalidChange)
	}
	if c.State != StateStable && c.Target == "" {
		return fmt.Errorf("%w: state %q needs a target", ErrInvalidChange, c.State)
	}
	return nil
}

// CarrierStore reads and writes the carrier row.
type CarrierStore struct {
	db *gorm.DB
}

// NewCarrierStore creates the table when it is missing.
func NewCarrierStore(db *gorm.DB) (*CarrierStore, error) {
	if err := db.AutoMigrate(&CarrierRow{}); err != nil {
		return nil, fmt.Errorf("migrating cluster_carrier: %w", err)
	}
	return &CarrierStore{db: db}, nil
}

// Get returns the row, or ErrNotSeeded.
func (s *CarrierStore) Get(ctx context.Context) (CarrierRow, error) {
	var row CarrierRow
	err := s.db.WithContext(ctx).Where("id = ?", carrierRowID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CarrierRow{}, ErrNotSeeded
	}
	if err != nil {
		return CarrierRow{}, fmt.Errorf("reading cluster carrier: %w", err)
	}
	return row, nil
}

// Seed inserts a stable row at epoch 1 when none exists, and returns the row
// that is in the database after the call. created is true only for the caller
// whose insert won. Replicas that start together can all call it: the first
// insert wins and the others read that row.
func (s *CarrierStore) Seed(ctx context.Context, active Carrier, by string) (row CarrierRow, created bool, err error) {
	if !active.valid() {
		return CarrierRow{}, false, fmt.Errorf("%w: %q", ErrInvalidCarrier, active)
	}
	res := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&CarrierRow{
		ID:        carrierRowID,
		Active:    active,
		Epoch:     1,
		State:     StateStable,
		ChangedBy: by,
		ChangedAt: time.Now().UTC(),
	})
	if res.Error != nil {
		return CarrierRow{}, false, fmt.Errorf("seeding cluster carrier: %w", res.Error)
	}
	row, err = s.Get(ctx)
	return row, res.RowsAffected == 1, err
}

// Transition writes change if the row is still at epoch from, and bumps the
// epoch. It returns ErrStaleEpoch when another writer got there first, so two
// admins or two replicas cannot interleave a change.
func (s *CarrierStore) Transition(ctx context.Context, from int64, change Change) (CarrierRow, error) {
	if err := change.validate(); err != nil {
		return CarrierRow{}, err
	}
	// RETURNING hands back the row this update wrote, not a later one.
	var row CarrierRow
	res := s.db.WithContext(ctx).Model(&row).Clauses(clause.Returning{}).
		Where("id = ? AND epoch = ?", carrierRowID, from).
		Updates(map[string]any{
			"active":         change.Active,
			"state":          change.State,
			"target":         change.Target,
			"draining":       change.Draining,
			"draining_until": change.DrainingUntil,
			"changed_by":     change.By,
			"changed_at":     gorm.Expr("now()"),
			"force":          change.Force,
			"note":           change.Note,
			"prev_epoch":     gorm.Expr("epoch"),
			"epoch":          gorm.Expr("epoch + 1"),
		})
	if res.Error != nil {
		return CarrierRow{}, fmt.Errorf("changing cluster carrier: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		// Tell a missing row from a lost race.
		if _, err := s.Get(ctx); err != nil {
			return CarrierRow{}, err
		}
		return CarrierRow{}, ErrStaleEpoch
	}
	return row, nil
}

// DBNow returns the time on the clock of the database. The timeouts of a change
// (the prepare timeout, the transition window, the end of a drain) are compared
// with it and with the stamps that the database wrote, never with the clock of a
// replica.
func (s *CarrierStore) DBNow(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := s.db.WithContext(ctx).Raw("SELECT now()").Scan(&now).Error; err != nil {
		return time.Time{}, fmt.Errorf("reading the clock of the database: %w", err)
	}
	return now, nil
}
