package distributed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// QuantJobRecord tracks quantization jobs in PostgreSQL. The columns mirror the
// API shape (schema.QuantizationJob); the structured Config and ExtraOptions are
// serialized into JSON text columns so a record fully reconstructs the job.
type QuantJobRecord struct {
	ID               string    `gorm:"primaryKey;size:36" json:"id"`
	UserID           string    `gorm:"index;size:36" json:"user_id,omitempty"`
	Model            string    `gorm:"size:255" json:"model"`
	Backend          string    `gorm:"size:64" json:"backend"`
	ModelID          string    `gorm:"size:255" json:"model_id,omitempty"`
	QuantizationType string    `gorm:"size:32" json:"quantization_type"`
	Status           string    `gorm:"index;size:32;default:queued" json:"status"` // queued, downloading, converting, quantizing, completed, failed, stopped
	Message          string    `gorm:"type:text" json:"message,omitempty"`
	OutputDir        string    `gorm:"size:512" json:"output_dir,omitempty"`
	OutputFile       string    `gorm:"size:512" json:"output_file,omitempty"`
	ConfigJSON       string    `gorm:"column:config;type:text" json:"-"`
	ExtraOptsJSON    string    `gorm:"column:extra_options;type:text" json:"-"`
	ImportStatus     string    `gorm:"size:32" json:"import_status,omitempty"`
	ImportMessage    string    `gorm:"type:text" json:"import_message,omitempty"`
	ImportModelName  string    `gorm:"size:255" json:"import_model_name,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (QuantJobRecord) TableName() string { return "quantization_jobs" }

// QuantStore manages quantization job state in PostgreSQL.
type QuantStore struct {
	db *gorm.DB
}

// NewQuantStore creates a new QuantStore and auto-migrates.
// Uses a PostgreSQL advisory lock to prevent concurrent migration races
// when multiple instances (frontend + workers) start at the same time.
func NewQuantStore(db *gorm.DB) (*QuantStore, error) {
	if err := advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		return db.AutoMigrate(&QuantJobRecord{})
	}); err != nil {
		return nil, fmt.Errorf("migrating quantization_jobs: %w", err)
	}
	return &QuantStore{db: db}, nil
}

// Create stores a new quantization job.
func (s *QuantStore) Create(job *QuantJobRecord) error {
	if job.ID == "" {
		job.ID = uuid.New().String()
	}
	job.CreatedAt = time.Now()
	job.UpdatedAt = job.CreatedAt
	return s.db.Create(job).Error
}

// Get retrieves a quantization job by ID.
func (s *QuantStore) Get(id string) (*QuantJobRecord, error) {
	var job QuantJobRecord
	if err := s.db.First(&job, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

// ListAll returns every quantization job across all users. The SyncedMap that
// backs QuantizationService is a single global map (the REST API filters by user
// at read time), so hydrate needs the full set.
func (s *QuantStore) ListAll() ([]QuantJobRecord, error) {
	var jobs []QuantJobRecord
	return jobs, s.db.Order("created_at DESC").Find(&jobs).Error
}

// Upsert idempotently inserts or fully replaces a job row by primary key. The
// SyncedMap write-through path issues a single Set per mutation regardless of
// whether the job already exists, so it needs one create-or-update primitive
// (Create alone fails on a duplicate key).
func (s *QuantStore) Upsert(job *QuantJobRecord) error {
	if job.ID == "" {
		job.ID = uuid.New().String()
	}
	now := time.Now()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		UpdateAll: true,
	}).Create(job).Error
}

// Delete removes a quantization job.
func (s *QuantStore) Delete(id string) error {
	return s.db.Where("id = ?", id).Delete(&QuantJobRecord{}).Error
}
