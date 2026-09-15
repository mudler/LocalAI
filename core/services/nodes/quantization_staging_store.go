package nodes

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// QuantizationStagingRecord is the durable hand-off between the frontend that
// starts a remote job and whichever frontend later consumes its progress.
type QuantizationStagingRecord struct {
	NodeID          string   `gorm:"primaryKey;size:36"`
	JobID           string   `gorm:"primaryKey;size:255"`
	FrontendDir     string   `gorm:"type:text"`
	DataRelativeDir string   `gorm:"type:text"`
	RemoteDir       string   `gorm:"type:text"`
	KeyPrefix       string   `gorm:"type:text"`
	InputRequestID  string   `gorm:"size:36"`
	InputKeys       []string `gorm:"serializer:json"`
}

type quantizationStagingStore interface {
	Put(context.Context, *QuantizationStagingRecord) error
	Get(context.Context, string, string) (*QuantizationStagingRecord, bool, error)
	Delete(context.Context, string, string) error
}

type gormQuantizationStagingStore struct{ db *gorm.DB }

func (s gormQuantizationStagingStore) Put(ctx context.Context, record *QuantizationStagingRecord) error {
	return s.db.WithContext(ctx).Save(record).Error
}

func (s gormQuantizationStagingStore) Get(ctx context.Context, nodeID, jobID string) (*QuantizationStagingRecord, bool, error) {
	var record QuantizationStagingRecord
	err := s.db.WithContext(ctx).First(&record, "node_id = ? AND job_id = ?", nodeID, jobID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	return &record, err == nil, err
}

func (s gormQuantizationStagingStore) Delete(ctx context.Context, nodeID, jobID string) error {
	return s.db.WithContext(ctx).Delete(&QuantizationStagingRecord{}, "node_id = ? AND job_id = ?", nodeID, jobID).Error
}
