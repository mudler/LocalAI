package auth

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// QuotaRule defines a rate/token limit for a user, optionally scoped to a model.
type QuotaRule struct {
	ID             string `gorm:"primaryKey;size:36"`
	UserID         string `gorm:"size:36;uniqueIndex:idx_quota_user_model"`
	Model          string `gorm:"size:255;uniqueIndex:idx_quota_user_model"` // "" = all models
	MaxRequests    *int64 // nil = no request limit
	MaxTotalTokens *int64 // nil = no token limit
	WindowSeconds  int64  // e.g., 3600 = 1h
	CreatedAt      time.Time
	UpdatedAt      time.Time
	User           User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// QuotaStatus is returned to clients with current usage included.
type QuotaStatus struct {
	ID              string `json:"id"`
	Model           string `json:"model"`
	MaxRequests     *int64 `json:"max_requests"`
	MaxTotalTokens  *int64 `json:"max_total_tokens"`
	Window          string `json:"window"`
	CurrentRequests int64  `json:"current_requests"`
	CurrentTokens   int64  `json:"current_total_tokens"`
	ResetsAt        string `json:"resets_at,omitempty"`
}

// ── CRUD ──

// CreateOrUpdateQuotaRule upserts a quota rule for the given user+model.
func CreateOrUpdateQuotaRule(db *gorm.DB, userID, model string, maxReqs, maxTokens *int64, windowSecs int64) (*QuotaRule, error) {
	var existing QuotaRule
	err := db.Where("user_id = ? AND model = ?", userID, model).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		rule := QuotaRule{
			ID:             uuid.New().String(),
			UserID:         userID,
			Model:          model,
			MaxRequests:    maxReqs,
			MaxTotalTokens: maxTokens,
			WindowSeconds:  windowSecs,
		}
		if err := db.Create(&rule).Error; err != nil {
			return nil, err
		}
		quotaCache.invalidateUser(userID)
		return &rule, nil
	}
	if err != nil {
		return nil, err
	}
	existing.MaxRequests = maxReqs
	existing.MaxTotalTokens = maxTokens
	existing.WindowSeconds = windowSecs
	if err := db.Save(&existing).Error; err != nil {
		return nil, err
	}
	quotaCache.invalidateUser(userID)
	return &existing, nil
}

// ListQuotaRules returns all quota rules for a user.
func ListQuotaRules(db *gorm.DB, userID string) ([]QuotaRule, error) {
	var rules []QuotaRule
	if err := db.Where("user_id = ?", userID).Order("model ASC").Find(&rules).Error; err != nil {
		return nil, err
	}
	return rules, nil
}

// DeleteQuotaRule removes a quota rule by ID (scoped to user for safety).
func DeleteQuotaRule(db *gorm.DB, ruleID, userID string) error {
	result := db.Where("id = ? AND user_id = ?", ruleID, userID).Delete(&QuotaRule{})
	if result.RowsAffected == 0 {
		return fmt.Errorf("quota rule not found")
	}
	quotaCache.invalidateUser(userID)
	return nil
}

// ── Usage queries ──

type usageCounts struct {
	RequestCount int64
	TotalTokens  int64
}

// getUsageSince counts requests and tokens for a user since the given time.
func getUsageSince(db *gorm.DB, userID string, since time.Time, model string) (usageCounts, error) {
	var result usageCounts
	q := db.Model(&UsageRecord{}).
		Select("COUNT(*) as request_count, COALESCE(SUM(total_tokens), 0) as total_tokens").
		Where("user_id = ? AND created_at >= ?", userID, since)
	if model != "" {
		q = q.Where("model = ?", model)
	}
	if err := q.Row().Scan(&result.RequestCount, &result.TotalTokens); err != nil {
		return result, err
	}
	return result, nil
}

// GetQuotaStatuses returns all quota rules for a user with current usage.
func GetQuotaStatuses(db *gorm.DB, userID string) ([]QuotaStatus, error) {
	rules, err := ListQuotaRules(db, userID)
	if err != nil {
		return nil, err
	}
	statuses := make([]QuotaStatus, 0, len(rules))
	now := time.Now()
	for _, r := range rules {
		windowStart := now.Add(-time.Duration(r.WindowSeconds) * time.Second)
		counts, err := getUsageSince(db, userID, windowStart, r.Model)
		if err != nil {
			counts = usageCounts{}
		}
		statuses = append(statuses, QuotaStatus{
			ID:              r.ID,
			Model:           r.Model,
			MaxRequests:     r.MaxRequests,
			MaxTotalTokens:  r.MaxTotalTokens,
			Window:          formatWindowDuration(r.WindowSeconds),
			CurrentRequests: counts.RequestCount,
			CurrentTokens:   counts.TotalTokens,
			ResetsAt:        windowStart.Add(time.Duration(r.WindowSeconds) * time.Second).UTC().Format(time.RFC3339),
		})
	}
	return statuses, nil
}

// ── Quota check (used by middleware) ──

// QuotaExceeded checks whether the user has exceeded any applicable quota rule.
// Returns (exceeded bool, retryAfterSeconds int64, message string).
func QuotaExceeded(db *gorm.DB, userID, model string) (bool, int64, string) {
	rules := quotaCache.getRules(db, userID)
	if len(rules) == 0 {
		return false, 0, ""
	}

	now := time.Now()

	for _, r := range rules {
		// Check if rule applies: model-specific rules match that model, global (empty) applies to all.
		if r.Model != "" && r.Model != model {
			continue
		}

		windowStart := now.Add(-time.Duration(r.WindowSeconds) * time.Second)
		retryAfter := r.WindowSeconds // worst case: full window

		// Try cache first
		counts, ok := quotaCache.getUsage(userID, r.Model, windowStart)
		if !ok {
			var err error
			counts, err = getUsageSince(db, userID, windowStart, r.Model)
			if err != nil {
				continue // on error, don't block the request
			}
			quotaCache.setUsage(userID, r.Model, windowStart, counts)
		}

		if r.MaxRequests != nil && counts.RequestCount >= *r.MaxRequests {
			scope := "all models"
			if r.Model != "" {
				scope = "model " + r.Model
			}
			return true, retryAfter, fmt.Sprintf(
				"Request quota exceeded for %s: %d/%d requests in %s window",
				scope, counts.RequestCount, *r.MaxRequests, formatWindowDuration(r.WindowSeconds),
			)
		}
		if r.MaxTotalTokens != nil && counts.TotalTokens >= *r.MaxTotalTokens {
			scope := "all models"
			if r.Model != "" {
				scope = "model " + r.Model
			}
			return true, retryAfter, fmt.Sprintf(
				"Token quota exceeded for %s: %d/%d tokens in %s window",
				scope, counts.TotalTokens, *r.MaxTotalTokens, formatWindowDuration(r.WindowSeconds),
			)
		}
	}

	// Optimistic increment: bump cached counters so subsequent requests in the
	// same cache window see an updated count without re-querying the DB.
	for _, r := range rules {
		if r.Model != "" && r.Model != model {
			continue
		}
		windowStart := now.Add(-time.Duration(r.WindowSeconds) * time.Second)
		quotaCache.incrementUsage(userID, r.Model, windowStart)
	}

	return false, 0, ""
}

// ── In-memory cache ──

var quotaCache = newQuotaCacheStore()

type quotaCacheStore struct {
	mu    sync.RWMutex
	rules map[string]cachedRules // userID -> rules
	usage map[string]cachedUsage // "userID|model|windowStart" -> counts
}

type cachedRules struct {
	rules     []QuotaRule
	fetchedAt time.Time
}

type cachedUsage struct {
	counts    usageCounts
	fetchedAt time.Time
}

func newQuotaCacheStore() *quotaCacheStore {
	c := &quotaCacheStore{
		rules: make(map[string]cachedRules),
		usage: make(map[string]cachedUsage),
	}
	go c.cleanupLoop()
	return c
}

const (
	rulesCacheTTL = 30 * time.Second
	usageCacheTTL = 10 * time.Second
)

func (c *quotaCacheStore) getRules(db *gorm.DB, userID string) []QuotaRule {
	c.mu.RLock()
	cached, ok := c.rules[userID]
	c.mu.RUnlock()
	if ok && time.Since(cached.fetchedAt) < rulesCacheTTL {
		return cached.rules
	}

	rules, err := ListQuotaRules(db, userID)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	c.rules[userID] = cachedRules{rules: rules, fetchedAt: time.Now()}
	c.mu.Unlock()
	return rules
}

func (c *quotaCacheStore) invalidateUser(userID string) {
	c.mu.Lock()
	delete(c.rules, userID)
	c.mu.Unlock()
}

func usageKey(userID, model string, windowStart time.Time) string {
	return userID + "|" + model + "|" + windowStart.Truncate(time.Second).Format(time.RFC3339)
}

func (c *quotaCacheStore) getUsage(userID, model string, windowStart time.Time) (usageCounts, bool) {
	key := usageKey(userID, model, windowStart)
	c.mu.RLock()
	cached, ok := c.usage[key]
	c.mu.RUnlock()
	if ok && time.Since(cached.fetchedAt) < usageCacheTTL {
		return cached.counts, true
	}
	return usageCounts{}, false
}

func (c *quotaCacheStore) setUsage(userID, model string, windowStart time.Time, counts usageCounts) {
	key := usageKey(userID, model, windowStart)
	c.mu.Lock()
	c.usage[key] = cachedUsage{counts: counts, fetchedAt: time.Now()}
	c.mu.Unlock()
}

func (c *quotaCacheStore) incrementUsage(userID, model string, windowStart time.Time) {
	key := usageKey(userID, model, windowStart)
	c.mu.Lock()
	if cached, ok := c.usage[key]; ok {
		cached.counts.RequestCount++
		c.usage[key] = cached
	}
	c.mu.Unlock()
}

func (c *quotaCacheStore) cleanupLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.rules {
			if now.Sub(v.fetchedAt) > rulesCacheTTL*2 {
				delete(c.rules, k)
			}
		}
		for k, v := range c.usage {
			if now.Sub(v.fetchedAt) > usageCacheTTL*2 {
				delete(c.usage, k)
			}
		}
		c.mu.Unlock()
	}
}

// ── Helpers ──

// ParseWindowDuration converts a human-friendly window string to seconds.
func ParseWindowDuration(s string) (int64, error) {
	switch s {
	case "1m":
		return 60, nil
	case "5m":
		return 300, nil
	case "1h":
		return 3600, nil
	case "6h":
		return 21600, nil
	case "1d":
		return 86400, nil
	case "7d":
		return 604800, nil
	case "30d":
		return 2592000, nil
	}
	// Try Go duration parsing as fallback
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid window duration: %s", s)
	}
	return int64(d.Seconds()), nil
}

// formatWindowDuration converts seconds to a human-friendly string.
func formatWindowDuration(secs int64) string {
	switch secs {
	case 60:
		return "1m"
	case 300:
		return "5m"
	case 3600:
		return "1h"
	case 21600:
		return "6h"
	case 86400:
		return "1d"
	case 604800:
		return "7d"
	case 2592000:
		return "30d"
	default:
		d := time.Duration(secs) * time.Second
		return d.String()
	}
}
