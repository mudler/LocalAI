package auth

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// UsageRecord represents a single API request's token usage.
type UsageRecord struct {
	ID               uint   `gorm:"primaryKey;autoIncrement"`
	UserID           string `gorm:"size:36;index:idx_usage_user_time"`
	UserName         string `gorm:"size:255"`
	Model            string `gorm:"size:255;index"`
	Endpoint         string `gorm:"size:255"`
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Duration         int64     // milliseconds
	CreatedAt        time.Time `gorm:"index:idx_usage_user_time"`
}

// RecordUsage inserts a usage record.
func RecordUsage(db *gorm.DB, record *UsageRecord) error {
	return db.Create(record).Error
}

// UsageBucket is an aggregated time bucket for the dashboard.
type UsageBucket struct {
	Bucket           string `json:"bucket"`
	Model            string `json:"model"`
	UserID           string `json:"user_id,omitempty"`
	UserName         string `json:"user_name,omitempty"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	RequestCount     int64  `json:"request_count"`
}

// UsageTotals is a summary of all usage.
type UsageTotals struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	RequestCount     int64 `json:"request_count"`
}

// periodToWindow returns the time window and SQL date format for a period.
func periodToWindow(period string, isSQLite bool) (time.Time, string) {
	now := time.Now()
	var since time.Time
	var dateFmt string

	switch period {
	case "day":
		since = now.Add(-24 * time.Hour)
		if isSQLite {
			dateFmt = "strftime('%Y-%m-%d %H:00', created_at)"
		} else {
			dateFmt = "to_char(date_trunc('hour', created_at), 'YYYY-MM-DD HH24:00')"
		}
	case "week":
		since = now.Add(-7 * 24 * time.Hour)
		if isSQLite {
			dateFmt = "strftime('%Y-%m-%d', created_at)"
		} else {
			dateFmt = "to_char(date_trunc('day', created_at), 'YYYY-MM-DD')"
		}
	case "all":
		since = time.Time{} // zero time = no filter
		if isSQLite {
			dateFmt = "strftime('%Y-%m', created_at)"
		} else {
			dateFmt = "to_char(date_trunc('month', created_at), 'YYYY-MM')"
		}
	default: // "month"
		since = now.Add(-30 * 24 * time.Hour)
		if isSQLite {
			dateFmt = "strftime('%Y-%m-%d', created_at)"
		} else {
			dateFmt = "to_char(date_trunc('day', created_at), 'YYYY-MM-DD')"
		}
	}

	return since, dateFmt
}

func isSQLiteDB(db *gorm.DB) bool {
	return strings.Contains(db.Dialector.Name(), "sqlite")
}

// GetUserUsage returns aggregated usage for a single user.
func GetUserUsage(db *gorm.DB, userID, period string) ([]UsageBucket, error) {
	sqlite := isSQLiteDB(db)
	since, dateFmt := periodToWindow(period, sqlite)

	bucketExpr := fmt.Sprintf("%s as bucket", dateFmt)

	query := db.Model(&UsageRecord{}).
		Select(bucketExpr+", model, "+
			"SUM(prompt_tokens) as prompt_tokens, "+
			"SUM(completion_tokens) as completion_tokens, "+
			"SUM(total_tokens) as total_tokens, "+
			"COUNT(*) as request_count").
		Where("user_id = ?", userID).
		Group("bucket, model").
		Order("bucket ASC")

	if !since.IsZero() {
		query = query.Where("created_at >= ?", since)
	}

	var buckets []UsageBucket
	if err := query.Find(&buckets).Error; err != nil {
		return nil, err
	}
	return buckets, nil
}

// GetAllUsage returns aggregated usage for all users (admin). Optional userID filter.
func GetAllUsage(db *gorm.DB, period, userID string) ([]UsageBucket, error) {
	sqlite := isSQLiteDB(db)
	since, dateFmt := periodToWindow(period, sqlite)

	bucketExpr := fmt.Sprintf("%s as bucket", dateFmt)

	query := db.Model(&UsageRecord{}).
		Select(bucketExpr + ", model, user_id, user_name, " +
			"SUM(prompt_tokens) as prompt_tokens, " +
			"SUM(completion_tokens) as completion_tokens, " +
			"SUM(total_tokens) as total_tokens, " +
			"COUNT(*) as request_count").
		Group("bucket, model, user_id, user_name").
		Order("bucket ASC")

	if !since.IsZero() {
		query = query.Where("created_at >= ?", since)
	}

	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}

	var buckets []UsageBucket
	if err := query.Find(&buckets).Error; err != nil {
		return nil, err
	}
	return buckets, nil
}
