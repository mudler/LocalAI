package nodes

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrQuantizationStagingExists = errors.New("quantization staging state already exists")

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
	OutputFetched   bool
	OutputRelative  string `gorm:"type:text"`
	InputsReleased  bool
	OutputReleased  bool
}

type quantizationStagingStore interface {
	Create(context.Context, *QuantizationStagingRecord) error
	Update(context.Context, *QuantizationStagingRecord) error
	Get(context.Context, string, string) (*QuantizationStagingRecord, bool, error)
	Delete(context.Context, string, string) error
}

type gormQuantizationStagingStore struct{ db *gorm.DB }

func (s gormQuantizationStagingStore) Create(ctx context.Context, record *QuantizationStagingRecord) error {
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(record)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQuantizationStagingExists
	}
	return nil
}

func (s gormQuantizationStagingStore) Update(ctx context.Context, record *QuantizationStagingRecord) error {
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
