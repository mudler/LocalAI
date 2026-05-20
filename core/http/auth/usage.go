package auth

import (
	"fmt"
	"strings"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// Source classification for a UsageRecord.
const (
	UsageSourceAPIKey = "apikey" // request authenticated with a named UserAPIKey
	UsageSourceWeb    = "web"    // request authenticated with a session cookie (web UI)
	UsageSourceLegacy = "legacy" // request authenticated with an env-configured legacy key
)

// UsageRecord represents a single API request's token usage.
type UsageRecord struct {
	ID       uint   `gorm:"primaryKey;autoIncrement"`
	UserID   string `gorm:"size:36;index:idx_usage_user_time"`
	UserName string `gorm:"size:255"`

	// Source classifies how the request authenticated. One of UsageSource* constants.
	// Empty for pre-feature rows until the InitDB backfill runs.
	Source string `gorm:"size:16;index:idx_usage_source"`
	// APIKeyID is the UserAPIKey.ID when Source == UsageSourceAPIKey. Nil otherwise.
	APIKeyID *string `gorm:"size:36;index:idx_usage_apikey"`
	// APIKeyName is a snapshot of UserAPIKey.Name at write time. Survives key deletion.
	APIKeyName string `gorm:"size:255"`

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
	Model            string `json:"model,omitempty"`
	UserID           string `json:"user_id,omitempty"`
	UserName         string `json:"user_name,omitempty"`
	Source           string `json:"source,omitempty"`
	APIKeyID         string `json:"api_key_id,omitempty"`
	APIKeyName       string `json:"api_key_name,omitempty"`
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

// BackfillUsageSource sets the Source column on pre-feature usage rows.
// Idempotent: only touches rows where source is NULL or empty.
//   - rows whose user_id == "legacy-api-key" -> UsageSourceLegacy
//   - everything else                        -> UsageSourceWeb
func BackfillUsageSource(db *gorm.DB) error {
	// Legacy first (more specific predicate)
	if err := db.Exec(
		`UPDATE usage_records SET source = ? WHERE (source IS NULL OR source = '') AND user_id = ?`,
		UsageSourceLegacy, "legacy-api-key",
	).Error; err != nil {
		return fmt.Errorf("backfill legacy usage source: %w", err)
	}
	// Everything else -> web
	if err := db.Exec(
		`UPDATE usage_records SET source = ? WHERE (source IS NULL OR source = '')`,
		UsageSourceWeb,
	).Error; err != nil {
		return fmt.Errorf("backfill web usage source: %w", err)
	}
	return nil
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

// TotalsEntry is a token+request roll-up.
type TotalsEntry struct {
	Tokens   int64 `json:"tokens"`
	Requests int64 `json:"requests"`
}

// KeyTotal is the per-key roll-up returned by sources endpoints.
type KeyTotal struct {
	APIKeyID   string    `json:"api_key_id"`
	APIKeyName string    `json:"api_key_name"`
	Tokens     int64     `json:"tokens"`
	Requests   int64     `json:"requests"`
	LastUsed   time.Time `json:"last_used"`
}

// SourceTotals summarises a per-source breakdown.
type SourceTotals struct {
	BySource   map[string]TotalsEntry `json:"by_source"`
	ByKey      []KeyTotal             `json:"by_key"` // server-sorted desc by tokens, capped
	GrandTotal TotalsEntry            `json:"grand_total"`
}

const maxKeyTotals = 200

// GetUserUsageBySource returns per-source aggregated usage for one user. Legacy
// is excluded by design (visible to admins only via the admin variant).
func GetUserUsageBySource(db *gorm.DB, userID, period string) ([]UsageBucket, SourceTotals, error) {
	sqlite := isSQLiteDB(db)
	since, dateFmt := periodToWindow(period, sqlite)
	bucketExpr := fmt.Sprintf("%s as bucket", dateFmt)

	query := db.Model(&UsageRecord{}).
		Select(bucketExpr+", source, COALESCE(api_key_id, '') as api_key_id, api_key_name, "+
			"SUM(prompt_tokens) as prompt_tokens, "+
			"SUM(completion_tokens) as completion_tokens, "+
			"SUM(total_tokens) as total_tokens, "+
			"COUNT(*) as request_count").
		Where("user_id = ?", userID).
		Where("source <> ?", UsageSourceLegacy).
		Group("bucket, source, api_key_id, api_key_name").
		Order("bucket ASC")

	if !since.IsZero() {
		query = query.Where("created_at >= ?", since)
	}

	var buckets []UsageBucket
	if err := query.Find(&buckets).Error; err != nil {
		return nil, SourceTotals{}, err
	}

	totals := computeSourceTotals(db, userID, "", since, false)
	return buckets, totals, nil
}

// computeSourceTotals rolls up by_source / by_key / grand_total.
// userID/apiKeyID are optional filters. includeLegacy controls whether the
// legacy bucket is exposed (admin-only).
func computeSourceTotals(db *gorm.DB, userID, apiKeyID string, since time.Time, includeLegacy bool) SourceTotals {
	totals := SourceTotals{BySource: map[string]TotalsEntry{}}

	bySourceQ := db.Model(&UsageRecord{}).
		Select("source, SUM(total_tokens) as tokens, COUNT(*) as requests").
		Group("source")
	bySourceQ = applyFilters(bySourceQ, userID, apiKeyID, since, includeLegacy)

	var bySourceRows []struct {
		Source   string
		Tokens   int64
		Requests int64
	}
	if err := bySourceQ.Scan(&bySourceRows).Error; err != nil {
		xlog.Warn("computeSourceTotals: by-source Scan failed", "error", err)
		return totals
	}
	for _, r := range bySourceRows {
		totals.BySource[r.Source] = TotalsEntry{Tokens: r.Tokens, Requests: r.Requests}
		totals.GrandTotal.Tokens += r.Tokens
		totals.GrandTotal.Requests += r.Requests
	}

	byKeyQ := db.Model(&UsageRecord{}).
		Select("COALESCE(api_key_id, '') as api_key_id, api_key_name, "+
			"SUM(total_tokens) as tokens, COUNT(*) as requests, MAX(created_at) as last_used").
		Where("api_key_id IS NOT NULL AND api_key_id <> ''").
		Group("api_key_id, api_key_name").
		Order("tokens DESC").
		Limit(maxKeyTotals)
	byKeyQ = applyFilters(byKeyQ, userID, apiKeyID, since, includeLegacy)

	// Iterate Rows() manually because MAX(created_at) is returned as a string by
	// the SQLite driver, and Go's database/sql refuses to scan that into
	// *time.Time. Postgres returns a proper timestamp. We accept both shapes
	// via a Rows.Scan into a string column, then parse uniformly.
	rows, err := byKeyQ.Rows()
	if err != nil {
		xlog.Warn("computeSourceTotals: by-key Rows() failed", "error", err)
	} else {
		defer rows.Close()
		out := make([]KeyTotal, 0)
		for rows.Next() {
			var (
				apiKeyID, apiKeyName, lastUsedRaw string
				tokens, requests                  int64
			)
			if scanErr := rows.Scan(&apiKeyID, &apiKeyName, &tokens, &requests, &lastUsedRaw); scanErr != nil {
				continue
			}
			out = append(out, KeyTotal{
				APIKeyID:   apiKeyID,
				APIKeyName: apiKeyName,
				Tokens:     tokens,
				Requests:   requests,
				LastUsed:   parseLastUsedString(lastUsedRaw),
			})
		}
		if rerr := rows.Err(); rerr != nil {
			xlog.Warn("computeSourceTotals: by-key rows iteration failed", "error", rerr)
		}
		totals.ByKey = out
	}

	return totals
}

// parseLastUsedString converts the textual MAX(created_at) value returned by
// SQLite (or any driver that surfaces the timestamp as a string) into a
// time.Time. Returns the zero time on parse failure.
func parseLastUsedString(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	// GORM's SQLite driver emits Go's default time formatting. Try the formats
	// it commonly produces, falling back to RFC3339Nano.
	layouts := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	xlog.Warn("parseLastUsedString: unrecognised format", "value", s)
	return time.Time{}
}

// GetAllUsageBySource is the admin variant of GetUserUsageBySource.
// Optional filters: userID and apiKeyID. Legacy is included.
// truncated == true iff the per-key roll-up was capped at maxKeyTotals.
func GetAllUsageBySource(db *gorm.DB, period, userID, apiKeyID string) ([]UsageBucket, SourceTotals, bool, error) {
	sqlite := isSQLiteDB(db)
	since, dateFmt := periodToWindow(period, sqlite)
	bucketExpr := fmt.Sprintf("%s as bucket", dateFmt)

	query := db.Model(&UsageRecord{}).
		Select(bucketExpr+", source, COALESCE(api_key_id, '') as api_key_id, api_key_name, "+
			"user_id, user_name, "+
			"SUM(prompt_tokens) as prompt_tokens, "+
			"SUM(completion_tokens) as completion_tokens, "+
			"SUM(total_tokens) as total_tokens, "+
			"COUNT(*) as request_count").
		Group("bucket, source, api_key_id, api_key_name, user_id, user_name").
		Order("bucket ASC")

	query = applyFilters(query, userID, apiKeyID, since, true)

	var buckets []UsageBucket
	if err := query.Find(&buckets).Error; err != nil {
		return nil, SourceTotals{}, false, err
	}

	totals := computeSourceTotals(db, userID, apiKeyID, since, true)

	// Count distinct api_key_ids matching the filters. If > maxKeyTotals,
	// the by_key slice was capped and we signal truncation to the caller.
	truncated := false
	var distinct int64
	countQ := applyFilters(
		db.Model(&UsageRecord{}).
			Distinct("api_key_id").
			Where("api_key_id IS NOT NULL AND api_key_id <> ''"),
		userID, apiKeyID, since, true,
	)
	if err := countQ.Count(&distinct).Error; err != nil {
		xlog.Warn("GetAllUsageBySource: distinct api_key_id count failed", "error", err)
	} else {
		truncated = distinct > maxKeyTotals
	}

	return buckets, totals, truncated, nil
}

func applyFilters(q *gorm.DB, userID, apiKeyID string, since time.Time, includeLegacy bool) *gorm.DB {
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	if apiKeyID != "" {
		q = q.Where("api_key_id = ?", apiKeyID)
	}
	if !since.IsZero() {
		q = q.Where("created_at >= ?", since)
	}
	if !includeLegacy {
		q = q.Where("source <> ?", UsageSourceLegacy)
	}
	return q
}
