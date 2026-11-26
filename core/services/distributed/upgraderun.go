package distributed

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpgradeRunRecord is one upgrade run (core/services/upgraderun). The runner
// writes the state; any frontend writes the cancel and dismiss flags, so the
// two never write the same column.
type UpgradeRunRecord struct {
	ID     string `gorm:"primaryKey;size:36" json:"id"`
	Source string `gorm:"size:16" json:"source"`
	Actor  string `gorm:"size:255" json:"actor,omitempty"`
	// State is running, finished or cancelled.
	State string `gorm:"size:16;index" json:"state"`
	// CancelRequested is set by any frontend; the runner reads it.
	CancelRequested bool `json:"cancel_requested"`
	Dismissed       bool `json:"dismissed"`
	// Runner names the frontend that runs the run. It is informative: the
	// advisory lock of the run, not this name, tells whether it still runs.
	Runner     string     `gorm:"size:64" json:"runner,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (UpgradeRunRecord) TableName() string { return "upgrade_runs" }

// UpgradeRunItemRecord is one item of an upgrade run. The progress of a
// running item is not stored: it is read from the status of its operation
// (JobID), the same status /api/operations and gallery_operations hold.
type UpgradeRunItemRecord struct {
	RunID      string `gorm:"primaryKey;size:36" json:"run_id"`
	Key        string `gorm:"primaryKey;column:item_key;size:512" json:"key"`
	Position   int    `json:"position"`
	Kind       string `gorm:"size:16" json:"kind"`
	Name       string `gorm:"size:512" json:"name"`
	Title      string `gorm:"size:255" json:"title,omitempty"`
	From       string `gorm:"column:from_version;size:255" json:"from,omitempty"`
	To         string `gorm:"column:to_version;size:255" json:"to,omitempty"`
	ExpectedTo string `gorm:"size:255" json:"expected_to,omitempty"`
	Phase      string `gorm:"size:16" json:"phase"`
	Message    string `gorm:"type:text" json:"message,omitempty"`
	ErrorKey   string `gorm:"size:64" json:"error_key,omitempty"`
	Error      string `gorm:"type:text" json:"error,omitempty"`
	Retryable  bool   `json:"retryable"`
	JobID      string `gorm:"size:64;index" json:"job_id,omitempty"`
	// QueuedReason says why a queued item waits.
	QueuedReason string `gorm:"size:64" json:"queued_reason,omitempty"`
	Verifying    bool   `json:"verifying"`
	CancelWanted bool   `json:"cancel_wanted"`
	CancelSent   bool   `json:"cancel_sent"`
	// CancelRequested is set by any frontend for this item; the runner reads
	// it.
	CancelRequested bool `json:"cancel_requested"`
	// Nodes and Extra are JSON.
	Nodes     string    `gorm:"type:text" json:"nodes,omitempty"`
	Extra     string    `gorm:"type:text" json:"extra,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (UpgradeRunItemRecord) TableName() string { return "upgrade_run_items" }

// ErrUpgradeRunNotFound is returned for an unknown run or item.
var ErrUpgradeRunNotFound = errors.New("upgrade run not found")

// ErrUpgradeRunState is returned when a flag cannot be set in the current
// state of the run (a cancel of a finished run, a dismiss of a running one).
var ErrUpgradeRunState = errors.New("the upgrade run is not in a state that allows this")

// ErrUpgradeRunFenced is returned to a runner that saves a run another
// frontend has taken over (Claim): the runner lost its lock and must stop.
var ErrUpgradeRunFenced = errors.New("another frontend runs this upgrade run now")

// UpgradeRunStore keeps the upgrade runs, on SQLite (standalone) or
// PostgreSQL (distributed).
type UpgradeRunStore struct {
	db *gorm.DB
}

// NewUpgradeRunStore creates the store and migrates its tables.
func NewUpgradeRunStore(db *gorm.DB) (*UpgradeRunStore, error) {
	if err := db.AutoMigrate(&UpgradeRunRecord{}, &UpgradeRunItemRecord{}); err != nil {
		return nil, fmt.Errorf("migrating upgrade_runs: %w", err)
	}
	return &UpgradeRunStore{db: db}, nil
}

// DB returns the database of the store.
func (s *UpgradeRunStore) DB() *gorm.DB { return s.db }

// Create stores a new run with its items.
func (s *UpgradeRunStore) Create(run *UpgradeRunRecord, items []UpgradeRunItemRecord) error {
	now := time.Now()
	run.UpdatedAt = now
	for i := range items {
		items[i].RunID, items[i].Position, items[i].UpdatedAt = run.ID, i, now
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(run).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		return tx.Create(&items).Error
	})
}

// runnerItemColumns are the item columns the runner owns.
var runnerItemColumns = []string{
	"position", "kind", "name", "title", "from_version", "to_version", "expected_to", "phase", "message",
	"error_key", "error", "retryable", "job_id", "queued_reason", "verifying", "cancel_wanted",
	"cancel_sent", "nodes", "extra", "updated_at",
}

// Save writes what the runner owns: the state of the run and every column of
// its items except the cancel flag another frontend may set meanwhile.
//
// Save is fenced by the runner: a run that another frontend claimed (Claim)
// is not written, and the save answers ErrUpgradeRunFenced. A frontend that
// lost its lock and did not notice yet cannot overwrite the run it lost. A
// run with no runner recorded yet is written by anyone.
func (s *UpgradeRunStore) Save(run *UpgradeRunRecord, items []UpgradeRunItemRecord) error {
	now := time.Now()
	run.UpdatedAt = now
	for i := range items {
		items[i].RunID, items[i].Position, items[i].UpdatedAt = run.ID, i, now
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&UpgradeRunRecord{}).Where("id = ? AND (runner = ? OR runner = '' OR runner IS NULL)", run.ID, run.Runner).Updates(map[string]any{
			"state": run.State, "finished_at": run.FinishedAt, "runner": run.Runner, "updated_at": now,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			var n int64
			if err := tx.Model(&UpgradeRunRecord{}).Where("id = ?", run.ID).Count(&n).Error; err != nil {
				return err
			}
			if n == 0 {
				return ErrUpgradeRunNotFound
			}
			return ErrUpgradeRunFenced
		}
		if len(items) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "run_id"}, {Name: "item_key"}},
			DoUpdates: clause.AssignmentColumns(runnerItemColumns),
		}).Create(&items).Error
	})
}

// Claim records runner as the frontend that runs a running run, so the saves
// of the frontend that ran it before are refused (see Save). Call it while
// holding the lock of the run.
func (s *UpgradeRunStore) Claim(id, runner string) error {
	res := s.db.Model(&UpgradeRunRecord{}).Where("id = ? AND state = ?", id, "running").Updates(map[string]any{
		"runner": runner, "updated_at": time.Now(),
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return s.stateError(id)
	}
	return nil
}

// Get returns a run and its items in run order.
func (s *UpgradeRunStore) Get(id string) (*UpgradeRunRecord, []UpgradeRunItemRecord, error) {
	var run UpgradeRunRecord
	if err := s.db.First(&run, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrUpgradeRunNotFound
		}
		return nil, nil, err
	}
	var items []UpgradeRunItemRecord
	if err := s.db.Where("run_id = ?", id).Order("position").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	return &run, items, nil
}

// List returns runs, the running ones first, then the newest. limit <= 0
// returns every run. Dismissed runs are left out unless withDismissed.
func (s *UpgradeRunStore) List(limit int, withDismissed bool) ([]UpgradeRunRecord, map[string][]UpgradeRunItemRecord, error) {
	var runs []UpgradeRunRecord
	q := s.db.Clauses(clause.OrderBy{Expression: clause.Expr{
		SQL: "CASE WHEN state = ? THEN 0 ELSE 1 END, created_at DESC", Vars: []any{"running"}, WithoutParentheses: true,
	}})
	if !withDismissed {
		q = q.Where("dismissed = ?", false)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&runs).Error; err != nil {
		return nil, nil, err
	}
	items := map[string][]UpgradeRunItemRecord{}
	if len(runs) == 0 {
		return runs, items, nil
	}
	ids := make([]string, len(runs))
	for i, r := range runs {
		ids[i] = r.ID
	}
	var all []UpgradeRunItemRecord
	if err := s.db.Where("run_id IN ?", ids).Order("position").Find(&all).Error; err != nil {
		return nil, nil, err
	}
	for _, it := range all {
		items[it.RunID] = append(items[it.RunID], it)
	}
	return runs, items, nil
}

// ListActive returns the ids of the runs still running.
func (s *UpgradeRunStore) ListActive() ([]string, error) {
	var ids []string
	err := s.db.Model(&UpgradeRunRecord{}).Where("state = ?", "running").Order("created_at").Pluck("id", &ids).Error
	return ids, err
}

// RequestCancel sets the cancel flag of a running run.
func (s *UpgradeRunStore) RequestCancel(id string) error {
	res := s.db.Model(&UpgradeRunRecord{}).Where("id = ? AND state = ?", id, "running").
		Updates(map[string]any{"cancel_requested": true})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return s.stateError(id)
	}
	return nil
}

// RequestCancelItem sets the cancel flag of an item of a running run.
func (s *UpgradeRunStore) RequestCancelItem(runID, key string) error {
	var run UpgradeRunRecord
	if err := s.db.First(&run, "id = ?", runID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUpgradeRunNotFound
		}
		return err
	}
	if run.State != "running" {
		return ErrUpgradeRunState
	}
	res := s.db.Model(&UpgradeRunItemRecord{}).Where("run_id = ? AND item_key = ?", runID, key).
		Updates(map[string]any{"cancel_requested": true})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrUpgradeRunNotFound
	}
	return nil
}

// Dismiss hides a run that is over.
func (s *UpgradeRunStore) Dismiss(id string) error {
	res := s.db.Model(&UpgradeRunRecord{}).Where("id = ? AND state <> ?", id, "running").
		Updates(map[string]any{"dismissed": true})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return s.stateError(id)
	}
	return nil
}

func (s *UpgradeRunStore) stateError(id string) error {
	var n int64
	if err := s.db.Model(&UpgradeRunRecord{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return ErrUpgradeRunNotFound
	}
	return ErrUpgradeRunState
}

// pruneBatch bounds the ids of one delete statement.
const pruneBatch = 500

// Prune removes finished runs and their items: every run that ended longer
// than maxAge ago, and every finished run beyond the newest keep. A maxAge of
// zero or less keeps runs of any age; a negative keep keeps any number. A
// running run is never removed and does not count toward keep. Pruning is
// idempotent, so two frontends that prune at once remove each run once. It
// returns how many runs it removed.
func (s *UpgradeRunStore) Prune(maxAge time.Duration, keep int) (int64, error) {
	var total int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		doomed := map[string]bool{}
		if maxAge > 0 {
			var ids []string
			if err := tx.Model(&UpgradeRunRecord{}).Where("state <> ? AND finished_at < ?", "running", time.Now().Add(-maxAge)).Pluck("id", &ids).Error; err != nil {
				return err
			}
			for _, id := range ids {
				doomed[id] = true
			}
		}
		if keep >= 0 {
			// The ids of the finished runs, newest first; everything after
			// the first keep goes. (SQLite takes no OFFSET without a LIMIT,
			// so the offset is applied here.)
			var ids []string
			if err := tx.Model(&UpgradeRunRecord{}).Where("state <> ?", "running").
				// A finished run without an end time counts as the oldest
				// (PostgreSQL sorts NULL first in a descending order).
				Order("CASE WHEN finished_at IS NULL THEN 1 ELSE 0 END").Order("finished_at DESC").Order("created_at DESC").Order("id DESC").Pluck("id", &ids).Error; err != nil {
				return err
			}
			if len(ids) > keep {
				for _, id := range ids[keep:] {
					doomed[id] = true
				}
			}
		}
		ids := make([]string, 0, len(doomed))
		for id := range doomed {
			ids = append(ids, id)
		}
		for start := 0; start < len(ids); start += pruneBatch {
			batch := ids[start:min(start+pruneBatch, len(ids))]
			if err := tx.Where("run_id IN ?", batch).Delete(&UpgradeRunItemRecord{}).Error; err != nil {
				return err
			}
			res := tx.Where("id IN ? AND state <> ?", batch, "running").Delete(&UpgradeRunRecord{})
			if res.Error != nil {
				return res.Error
			}
			total += res.RowsAffected
		}
		return nil
	})
	return total, err
}
