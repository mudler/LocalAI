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
	OpType             string    `gorm:"size:32" json:"op_type"`                    // "model_install", "model_delete", "backend_install"
	Status             string    `gorm:"size:32;default:pending" json:"status"`     // pending, downloading, processing, completed, failed, cancelled
	Progress           float64   `json:"progress"`                                  // 0.0 to 1.0
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

func (GalleryOperationRecord) TableName() string { return "gallery_operations" }

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
func (s *GalleryStore) Create(op *GalleryOperationRecord) error {
	if op.ID == "" {
		op.ID = uuid.New().String()
	}
	op.CreatedAt = time.Now()
	op.UpdatedAt = op.CreatedAt
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"gallery_element_name", "op_type", "status",
			"frontend_id", "user_id", "cancellable", "updated_at",
		}),
	}).Create(op).Error
}

// UpdateProgress updates progress for an operation. The cancellable flag is
// persisted on every tick so a replica that restarts mid-install rehydrates the
// op as still cancellable — otherwise the column keeps its Create-time zero
// value (false), the UI hides the cancel button, and the orphaned op can only
// be dismissed by waiting for the 30-minute stale reaper.
func (s *GalleryStore) UpdateProgress(id string, progress float64, message, downloadedSize string, cancellable bool) error {
	return s.db.Model(&GalleryOperationRecord{}).Where("id = ?", id).Updates(map[string]any{
		"progress":             progress,
		"message":              message,
		"downloaded_file_size": downloadedSize,
		"cancellable":          cancellable,
		"updated_at":           time.Now(),
	}).Error
}

// UpdateStatus updates the status of an operation. A terminal status is never
// cancellable, so the flag is cleared here to keep the persisted row consistent
// with what the UI should offer.
func (s *GalleryStore) UpdateStatus(id, status, errMsg string) error {
	updates := map[string]any{
		"status":      status,
		"cancellable": false,
		"updated_at":  time.Now(),
	}
	if errMsg != "" {
		updates["error"] = errMsg
	}
	return s.db.Model(&GalleryOperationRecord{}).Where("id = ?", id).Updates(updates).Error
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
	return s.db.Where("created_at < ? AND status IN ?", cutoff,
		[]string{"completed", "failed", "cancelled"}).
		Delete(&GalleryOperationRecord{}).Error
}
