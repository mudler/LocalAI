package distributed

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SkillMetadataRecord tracks skill metadata in PostgreSQL.
type SkillMetadataRecord struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	UserID     string    `gorm:"index;size:36" json:"user_id,omitempty"`
	Name       string    `gorm:"index;size:255" json:"name"`
	Definition string    `gorm:"type:text" json:"definition,omitempty"` // SKILL.md content or YAML
	SourceType string    `gorm:"size:32" json:"source_type"`            // "inline", "git"
	SourceURL  string    `gorm:"size:512" json:"source_url,omitempty"`
	Version    string    `gorm:"size:64" json:"version,omitempty"`
	Enabled    bool      `gorm:"default:true" json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (SkillMetadataRecord) TableName() string { return "skills_metadata" }

// SkillStore manages skill metadata in PostgreSQL.
type SkillStore struct {
	db *gorm.DB
}

// NewSkillStore creates a new SkillStore and auto-migrates.
func NewSkillStore(db *gorm.DB) (*SkillStore, error) {
	if err := db.AutoMigrate(&SkillMetadataRecord{}); err != nil {
		return nil, fmt.Errorf("migrating skills_metadata: %w", err)
	}
	return &SkillStore{db: db}, nil
}

// Save creates or updates a skill metadata record.
func (s *SkillStore) Save(rec *SkillMetadataRecord) error {
	if rec.ID == "" {
		rec.ID = uuid.New().String()
	}
	rec.UpdatedAt = time.Now()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = rec.UpdatedAt
	}

	var existing SkillMetadataRecord
	err := s.db.Where("user_id = ? AND name = ?", rec.UserID, rec.Name).First(&existing).Error
	if err == nil {
		rec.ID = existing.ID
		rec.CreatedAt = existing.CreatedAt
		return s.db.Model(&existing).Updates(rec).Error
	}
	return s.db.Create(rec).Error
}

// Get retrieves a skill by user and name.
func (s *SkillStore) Get(userID, name string) (*SkillMetadataRecord, error) {
	var rec SkillMetadataRecord
	q := s.db.Where("name = ?", name)
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	if err := q.First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

// List returns skills for a user.
func (s *SkillStore) List(userID string) ([]SkillMetadataRecord, error) {
	var recs []SkillMetadataRecord
	q := s.db.Order("name")
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	return recs, q.Find(&recs).Error
}

// Delete removes a skill metadata record.
func (s *SkillStore) Delete(userID, name string) error {
	q := s.db.Where("name = ?", name)
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	return q.Delete(&SkillMetadataRecord{}).Error
}

// ListGitSkills returns skills sourced from git repos (for sync jobs).
func (s *SkillStore) ListGitSkills() ([]SkillMetadataRecord, error) {
	var recs []SkillMetadataRecord
	return recs, s.db.Where("source_type = ? AND enabled = true", "git").Find(&recs).Error
}
