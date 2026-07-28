package distributed

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GalleryOperationRecord tracks model/backend download operations in PostgreSQL.
//
// CacheKey and IsBackendOp mirror the in-memory OpCache held by each frontend
// replica. They are written when a request first lands so a freshly-started
// (or freshly-routed-to) replica can rebuild its OpCache from this table
// instead of returning an empty `/api/operations` payload while the real
// operation is still in flight on a peer.
type GalleryOperationRecord struct {
	ID                 string    `gorm:"primaryKey;size:36" json:"id"`
	UserID             string    `gorm:"index;size:36" json:"user_id,omitempty"`
	GalleryElementName string    `gorm:"size:255" json:"gallery_element_name"`
	CacheKey           string    `gorm:"index;size:512" json:"cache_key,omitempty"` // OpCache key (galleryID or node:<id>:<backend>)
	IsBackendOp        bool      `json:"is_backend_op"`                             // true if installed via SetBackend
	OpType             string    `gorm:"size:32" json:"op_type"`                    // "model_install", "model_delete", "backend_install", "backend_delete"
	Status             string    `gorm:"size:32;default:pending" json:"status"`     // pending, downloading, processing, completed, failed, cancelled
	Progress           float64   `json:"progress"`                                  // 0.0 to 1.0
	Phase              string    `gorm:"size:32" json:"phase,omitempty"`
	CurrentBytes       int64     `json:"current_bytes,omitempty"`
	TotalBytes         int64     `json:"total_bytes,omitempty"`
	Message            string    `gorm:"type:text" json:"message,omitempty"`
	Error              string    `gorm:"type:text" json:"error,omitempty"`
	FileName           string    `gorm:"size:512" json:"file_name,omitempty"`
	TotalFileSize      string    `gorm:"size:32" json:"total_file_size,omitempty"`
	DownloadedFileSize string    `gorm:"size:32" json:"downloaded_file_size,omitempty"`
	FrontendID         string    `gorm:"size:36" json:"frontend_id,omitempty"` // which instance is processing
	Cancellable        bool      `json:"cancellable"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// activeStatuses lists the gallery_operations.status values that represent an
// operation a replica should still surface via /api/operations. Hydration and
// the dedup lookup share this set so the two paths never disagree about what
// "still active" means.
var activeStatuses = []string{"pending", "downloading", "processing"}

// terminalStatuses lists the gallery_operations.status values that represent a
// finished operation. The Activity record and the retention reaper share this
// set so the two never disagree about what "finished" means.
var terminalStatuses = []string{"completed", "failed", "cancelled"}

// settledStatuses is the subset of terminalStatuses that may never be
// rewritten. It deliberately excludes "failed", which the other two do not:
// CleanStale writes a failure onto any operation that has sat in an active
// status for 30 minutes, and the gallery worker consumes both channels
// serially, so an operation queued behind a large download is reaped while it
// is still going to run. That failure has to stay correctable by the real
// outcome. A completion or a cancellation is what actually happened, and
// nothing that arrives afterwards knows better.
var settledStatuses = []string{"completed", "cancelled"}

const galleryOperationsTable = "gallery_operations"

func (GalleryOperationRecord) TableName() string { return galleryOperationsTable }

// GalleryStore manages gallery operation state in PostgreSQL.
type GalleryStore struct {
	db *gorm.DB
}

// NewGalleryStore creates a new GalleryStore and auto-migrates.
func NewGalleryStore(db *gorm.DB) (*GalleryStore, error) {
	if err := db.AutoMigrate(&GalleryOperationRecord{}); err != nil {
		return nil, fmt.Errorf("migrating gallery_operations: %w", err)
	}
	return &GalleryStore{db: db}, nil
}

// Create stores a new gallery operation. Tolerates a row already existing
// for this ID — OpCache.Set may have written a placeholder row via
// UpsertCacheKey before the galleryop service goroutine called Create, and
// in that case we want to fill in the descriptive columns (gallery element
// name, op type, status) rather than fail with a primary-key conflict.
// CacheKey and IsBackendOp are intentionally not in DoUpdates so the
// placeholder's values win.
//
// The status, cancellable and updated_at columns are frozen once the row has
// settled. An admin can cancel an operation while it is still queued, and the
// worker then dequeues it and calls Create with status "pending" — without the
// freeze that reopens a cancelled operation, which reads as live forever. The
// descriptive columns still update, so the row keeps gaining its name and
// op_type, and a reaped-but-still-running operation is deliberately still
// allowed to reopen (see settledStatuses).
func (s *GalleryStore) Create(op *GalleryOperationRecord) error {
	if op.ID == "" {
		op.ID = uuid.New().String()
	}
	op.CreatedAt = time.Now()
	op.UpdatedAt = op.CreatedAt
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"gallery_element_name": gorm.Expr("excluded.gallery_element_name"),
			"op_type":              gorm.Expr("excluded.op_type"),
			"frontend_id":          gorm.Expr("excluded.frontend_id"),
			"user_id":              gorm.Expr("excluded.user_id"),
			"status":               keepWhenSettled("status"),
			"cancellable":          keepWhenSettled("cancellable"),
			"updated_at":           keepWhenSettled("updated_at"),
		}),
	}).Create(op).Error
}

// keepWhenSettled builds the upsert assignment for a column that must not be
// rewritten once the operation has settled: it keeps the stored value for a
// settled row and takes the incoming one otherwise.
func keepWhenSettled(column string) clause.Expr {
	stored := galleryOperationsTable + "."
	return gorm.Expr(
		"CASE WHEN "+stored+"status IN ? THEN "+stored+column+" ELSE excluded."+column+" END",
		settledStatuses)
}

// UpdateProgress updates progress for an operation. The cancellable flag is
// persisted on every tick so a replica that restarts mid-install rehydrates the
// op as still cancellable — otherwise the column keeps its Create-time zero
// value (false), the UI hides the cancel button, and the orphaned op can only
// be dismissed by waiting for the 30-minute stale reaper.
type OperationProgressDetails struct {
	Phase        string
	CurrentBytes int64
	TotalBytes   int64
}

func (s *GalleryStore) UpdateProgress(id string, progress float64, message, downloadedSize string, cancellable bool, details ...OperationProgressDetails) error {
	updates := map[string]any{
		"progress":             progress,
		"message":              message,
		"downloaded_file_size": downloadedSize,
		"cancellable":          cancellable,
		"updated_at":           time.Now(),
	}
	if len(details) > 0 {
		updates["phase"] = details[0].Phase
		updates["current_bytes"] = details[0].CurrentBytes
		updates["total_bytes"] = details[0].TotalBytes
	}
	return s.db.Model(&GalleryOperationRecord{}).Where("id = ?", id).Updates(updates).Error
}

// UpdateStatus updates the status of an operation. A terminal status is never
// cancellable, so the flag is cleared here to keep the persisted row consistent
// with what the UI should offer.
//
// A row that has already settled is left alone. An operation settles once, and
// the paths that retire one are not mutually exclusive: cancelling writes
// "cancelled" synchronously, and the handler goroutine then unwinds with the
// context error, which without this guard would overwrite the row with
// "failed: context canceled" and make the Activity page render a cancelled
// install as a red failure offering Retry. The guard also keeps updated_at
// pinned to when the operation actually finished, which is the key ListTerminal
// orders the record by. A failure is not settled and stays correctable — see
// settledStatuses for why.
//
// The error is written unconditionally so a corrected outcome drops the
// previous attempt's reason: without that, an operation the reaper gave up on
// and that then succeeded would be recorded as completed while still carrying
// "stale operation reaped" as its error.
func (s *GalleryStore) UpdateStatus(id, status, errMsg string) error {
	updates := map[string]any{
		"status":      status,
		"cancellable": false,
		"updated_at":  time.Now(),
		"error":       errMsg,
	}
	return s.db.Model(&GalleryOperationRecord{}).
		Where("id = ? AND status NOT IN ?", id, settledStatuses).
		Updates(updates).Error
}

// Get retrieves an operation by ID.
func (s *GalleryStore) Get(id string) (*GalleryOperationRecord, error) {
	var op GalleryOperationRecord
	if err := s.db.First(&op, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &op, nil
}

// List returns all operations, optionally filtered by status.
func (s *GalleryStore) List(status string) ([]GalleryOperationRecord, error) {
	var ops []GalleryOperationRecord
	q := s.db.Order("created_at DESC")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	return ops, q.Find(&ops).Error
}

// ListActive returns operations still considered in-flight — used by replicas
// to rehydrate their in-memory OpCache + statuses on startup. Stale records
// (older than 30 minutes without an update) are excluded so a crashed peer's
// orphaned rows never resurrect on a healthy replica; the existing CleanStale
// reaper eventually marks them failed.
func (s *GalleryStore) ListActive() ([]GalleryOperationRecord, error) {
	var ops []GalleryOperationRecord
	staleCutoff := time.Now().Add(-30 * time.Minute)
	err := s.db.Where("status IN ? AND updated_at > ?", activeStatuses, staleCutoff).
		Order("created_at DESC").Find(&ops).Error
	return ops, err
}

// UpsertCacheKey records the in-memory OpCache key + IsBackendOp flag on the
// gallery_operations row, creating the row if it does not exist yet.
//
// Why upsert: OpCache.Set is called by the HTTP admission handler before the
// galleryop service goroutine processes the operation and calls Create. If
// OpCache wrote with a plain Updates() those columns would silently be lost
// in the window between the two, so peer replicas hydrating in that window
// would still rebuild an empty OpCache. Upsert closes that window.
func (s *GalleryStore) UpsertCacheKey(id, cacheKey string, isBackend bool) error {
	now := time.Now()
	rec := GalleryOperationRecord{
		ID:          id,
		CacheKey:    cacheKey,
		IsBackendOp: isBackend,
		Status:      "pending",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"cache_key":     cacheKey,
			"is_backend_op": isBackend,
			"updated_at":    now,
		}),
	}).Create(&rec).Error
}

// FindDuplicate checks if another instance is already downloading the same element.
// Only considers records updated within the last 30 minutes as active — older
// in-progress records are assumed to be stale (crashed instance).
func (s *GalleryStore) FindDuplicate(elementName string) (*GalleryOperationRecord, error) {
	var op GalleryOperationRecord
	staleCutoff := time.Now().Add(-30 * time.Minute)
	err := s.db.Where("gallery_element_name = ? AND status IN ? AND updated_at > ?", elementName,
		activeStatuses, staleCutoff).First(&op).Error
	if err != nil {
		return nil, err
	}
	return &op, nil
}

// Cancel marks an operation as cancelled.
func (s *GalleryStore) Cancel(id string) error {
	return s.UpdateStatus(id, "cancelled", "")
}

// ListStale returns the IDs of in-progress operations that CleanStale would
// reap. Callers need the IDs, not just a count: the reaper corrects the
// database, but each replica also holds an in-memory copy of the operation
// status that the API actually serves, and that copy has to be corrected too
// or the reaped op keeps reporting "downloading" forever.
func (s *GalleryStore) ListStale(age time.Duration) ([]string, error) {
	cutoff := time.Now().Add(-age)
	var ids []string
	err := s.db.Model(&GalleryOperationRecord{}).
		Where("updated_at < ? AND status IN ?", cutoff, activeStatuses).
		Pluck("id", &ids).Error
	return ids, err
}

// CleanStale marks abandoned in-progress operations as failed and returns the
// number of rows reaped. Called on startup AND periodically to recover from
// crashed/restarted instances that left records in pending/downloading/
// processing state — an op orphaned after startup would otherwise linger
// "processing" until the next restart.
func (s *GalleryStore) CleanStale(age time.Duration) (int64, error) {
	cutoff := time.Now().Add(-age)
	res := s.db.Model(&GalleryOperationRecord{}).
		Where("updated_at < ? AND status IN ?", cutoff, activeStatuses).
		Updates(map[string]any{
			"status":     "failed",
			"error":      "stale operation reaped (abandoned by a crashed or restarted instance)",
			"updated_at": time.Now(),
		})
	return res.RowsAffected, res.Error
}

// CleanOld removes operations older than the given duration.
func (s *GalleryStore) CleanOld(retention time.Duration) error {
	cutoff := time.Now().Add(-retention)
	return s.db.Where("created_at < ? AND status IN ?", cutoff, terminalStatuses).
		Delete(&GalleryOperationRecord{}).Error
}

// ListTerminal returns finished operations, newest-finished first. It backs the
// Activity page's record: in distributed mode the per-replica in-memory ring
// shows a different history depending on which replica served the request, and
// a replica added by a scale-out has none at all.
//
// The order is by updated_at, which is when the operation reached its terminal
// status, rather than created_at, which is when it was queued: the record
// reports what finished and when.
//
// limit <= 0 returns every row.
func (s *GalleryStore) ListTerminal(limit int) ([]GalleryOperationRecord, error) {
	var ops []GalleryOperationRecord
	q := s.db.Where("status IN ?", terminalStatuses).Order("updated_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	return ops, q.Find(&ops).Error
}

// ClearTerminal deletes every finished operation, cluster-wide. Operations
// still in flight survive, so clearing the record cannot lose an install that
// has not reported its outcome yet.
func (s *GalleryStore) ClearTerminal() error {
	return s.db.Where("status IN ?", terminalStatuses).Delete(&GalleryOperationRecord{}).Error
}
