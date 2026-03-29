package distributed

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"gorm.io/gorm"
)

// FineTuneJobRecord tracks fine-tune jobs in PostgreSQL.
type FineTuneJobRecord struct {
	ID              string    `gorm:"primaryKey;size:36" json:"id"`
	UserID          string    `gorm:"index;size:36" json:"user_id,omitempty"`
	Model           string    `gorm:"size:255" json:"model"`
	Backend         string    `gorm:"size:64" json:"backend"`
	ModelID         string    `gorm:"size:255" json:"model_id,omitempty"`
	TrainingType    string    `gorm:"size:32" json:"training_type"`   // lora, loha, lokr, full
	TrainingMethod  string    `gorm:"size:32" json:"training_method"` // sft, dpo, grpo, etc.
	Status          string    `gorm:"index;size:32;default:queued" json:"status"` // queued, loading_model, training, saving, completed, failed, stopped
	Message         string    `gorm:"type:text" json:"message,omitempty"`
	OutputDir       string    `gorm:"size:512" json:"output_dir,omitempty"`
	ConfigJSON      string    `gorm:"column:config;type:text" json:"-"`
	ExtraOptsJSON   string    `gorm:"column:extra_options;type:text" json:"-"`
	BackendNodeID   string    `gorm:"size:36" json:"backend_node_id,omitempty"` // which backend node runs it
	ExportStatus    string    `gorm:"size:32" json:"export_status,omitempty"`
	ExportMessage   string    `gorm:"type:text" json:"export_message,omitempty"`
	ExportModelName string    `gorm:"size:255" json:"export_model_name,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (FineTuneJobRecord) TableName() string { return "finetune_jobs" }

// FineTuneStore manages fine-tune job state in PostgreSQL.
type FineTuneStore struct {
	db *gorm.DB
}

// NewFineTuneStore creates a new FineTuneStore and auto-migrates.
// Uses a PostgreSQL advisory lock to prevent concurrent migration races
// when multiple instances (frontend + workers) start at the same time.
func NewFineTuneStore(db *gorm.DB) (*FineTuneStore, error) {
	if err := advisorylock.WithLock(db, advisorylock.KeySchemaMigrate, func() error {
		return db.AutoMigrate(&FineTuneJobRecord{})
	}); err != nil {
		return nil, fmt.Errorf("migrating finetune_jobs: %w", err)
	}
	return &FineTuneStore{db: db}, nil
}

// Create stores a new fine-tune job.
func (s *FineTuneStore) Create(job *FineTuneJobRecord) error {
	if job.ID == "" {
		job.ID = uuid.New().String()
	}
	job.CreatedAt = time.Now()
	job.UpdatedAt = job.CreatedAt
	return s.db.Create(job).Error
}

// Get retrieves a fine-tune job by ID.
func (s *FineTuneStore) Get(id string) (*FineTuneJobRecord, error) {
	var job FineTuneJobRecord
	if err := s.db.First(&job, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

// List returns fine-tune jobs, optionally filtered by user.
func (s *FineTuneStore) List(userID string) ([]FineTuneJobRecord, error) {
	var jobs []FineTuneJobRecord
	q := s.db.Order("created_at DESC")
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	return jobs, q.Find(&jobs).Error
}

// UpdateStatus updates the status and message of a fine-tune job.
func (s *FineTuneStore) UpdateStatus(id, status, message string) error {
	return s.db.Model(&FineTuneJobRecord{}).Where("id = ?", id).Updates(map[string]any{
		"status":     status,
		"message":    message,
		"updated_at": time.Now(),
	}).Error
}

// UpdateExportStatus updates the export state of a fine-tune job.
func (s *FineTuneStore) UpdateExportStatus(id, status, message, modelName string) error {
	return s.db.Model(&FineTuneJobRecord{}).Where("id = ?", id).Updates(map[string]any{
		"export_status":     status,
		"export_message":    message,
		"export_model_name": modelName,
		"updated_at":        time.Now(),
	}).Error
}

// Delete removes a fine-tune job.
func (s *FineTuneStore) Delete(id string) error {
	return s.db.Where("id = ?", id).Delete(&FineTuneJobRecord{}).Error
}
