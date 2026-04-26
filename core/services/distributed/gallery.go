package distributed

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// GalleryOperationRecord tracks model/backend download operations in PostgreSQL.
type GalleryOperationRecord struct {
	ID                 string    `gorm:"primaryKey;size:36" json:"id"`
	UserID             string    `gorm:"index;size:36" json:"user_id,omitempty"`
	GalleryElementName string    `gorm:"size:255" json:"gallery_element_name"`
	OpType             string    `gorm:"size:32" json:"op_type"`                // "model_install", "model_delete", "backend_install"
	Status             string    `gorm:"size:32;default:pending" json:"status"` // pending, downloading, processing, completed, failed, cancelled
	Progress           float64   `json:"progress"`                              // 0.0 to 1.0
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

// Create stores a new gallery operation.
func (s *GalleryStore) Create(op *GalleryOperationRecord) error {
	if op.ID == "" {
		op.ID = uuid.New().String()
	}
	op.CreatedAt = time.Now()
	op.UpdatedAt = op.CreatedAt
	return s.db.Create(op).Error
}

// UpdateProgress updates progress for an operation.
func (s *GalleryStore) UpdateProgress(id string, progress float64, message, downloadedSize string) error {
	return s.db.Model(&GalleryOperationRecord{}).Where("id = ?", id).Updates(map[string]any{
		"progress":             progress,
		"message":              message,
		"downloaded_file_size": downloadedSize,
		"updated_at":           time.Now(),
	}).Error
}

// UpdateStatus updates the status of an operation.
func (s *GalleryStore) UpdateStatus(id, status, errMsg string) error {
	updates := map[string]any{
		"status":     status,
		"updated_at": time.Now(),
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

// FindDuplicate checks if another instance is already downloading the same element.
// Only considers records updated within the last 30 minutes as active — older
// in-progress records are assumed to be stale (crashed instance).
func (s *GalleryStore) FindDuplicate(elementName string) (*GalleryOperationRecord, error) {
	var op GalleryOperationRecord
	staleCutoff := time.Now().Add(-30 * time.Minute)
	err := s.db.Where("gallery_element_name = ? AND status IN ? AND updated_at > ?", elementName,
		[]string{"pending", "downloading", "processing"}, staleCutoff).First(&op).Error
	if err != nil {
		return nil, err
	}
	return &op, nil
}

// Cancel marks an operation as cancelled.
func (s *GalleryStore) Cancel(id string) error {
	return s.UpdateStatus(id, "cancelled", "")
}

// CleanStale marks abandoned in-progress operations as failed.
// Should be called on startup to recover from crashed instances that
// left records in pending/downloading/processing state.
func (s *GalleryStore) CleanStale(age time.Duration) error {
	cutoff := time.Now().Add(-age)
	return s.db.Model(&GalleryOperationRecord{}).
		Where("updated_at < ? AND status IN ?", cutoff,
			[]string{"pending", "downloading", "processing"}).
		Updates(map[string]any{
			"status":     "failed",
			"error":      "stale operation cleaned up on startup",
			"updated_at": time.Now(),
		}).Error
}

// CleanOld removes operations older than the given duration.
func (s *GalleryStore) CleanOld(retention time.Duration) error {
	cutoff := time.Now().Add(-retention)
	return s.db.Where("created_at < ? AND status IN ?", cutoff,
		[]string{"completed", "failed", "cancelled"}).
		Delete(&GalleryOperationRecord{}).Error
}
