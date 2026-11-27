package auth

import (
	"errors"
	"sync"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FeatureDefaultsRefresh is how long a frontend keeps the defaults it read.
// The auth database is the authority and every frontend reads it, so a change
// made on one frontend reaches the others within this interval; the frontend
// that made the change sees it at once.
const FeatureDefaultsRefresh = 30 * time.Second

var (
	// ErrUnknownFeature is returned for a feature name that LocalAI does not know.
	ErrUnknownFeature = errors.New("unknown feature")
	// ErrFeatureDefaultNotConfigurable is returned when an administrator tries
	// to change the default of a feature whose default is fixed in code.
	ErrFeatureDefaultNotConfigurable = errors.New("the default of this feature is not configurable")
)

// FeatureDefaultOverride is a default that an administrator set for a feature
// with a configurable default. It applies to every user whose permission map
// does not name the feature.
type FeatureDefaultOverride struct {
	Feature   string `gorm:"primaryKey;size:64"`
	Enabled   bool
	UpdatedAt time.Time
	UpdatedBy string `gorm:"size:255"`
}

// TableName sets the table name.
func (FeatureDefaultOverride) TableName() string { return "feature_defaults" }

// configurableDefaults is the set of features whose metadata marks the
// default as configurable.
var configurableDefaults = func() map[string]bool {
	m := map[string]bool{}
	for _, metas := range [][]FeatureMeta{AgentFeatureMetas(), GeneralFeatureMetas(), APIFeatureMetas()} {
		for _, meta := range metas {
			if meta.ConfigurableDefault {
				m[meta.Key] = true
			}
		}
	}
	return m
}()

// FeatureDefaultConfigurable reports whether an administrator may change the
// default of a feature.
func FeatureDefaultConfigurable(feature string) bool {
	return configurableDefaults[feature]
}

// FeatureDefault returns the default in force for a feature: the override an
// administrator stored, or the built-in default.
func FeatureDefault(db *gorm.DB, feature string) bool {
	return isDefaultOnFeature(db, feature)
}

// SetFeatureDefault stores the default of a feature for every user whose
// permission map does not name it. Only a feature marked ConfigurableDefault
// accepts one. by is the ID of the administrator who made the change.
func SetFeatureDefault(db *gorm.DB, feature string, enabled bool, by string) error {
	if !knownFeature(feature) {
		return ErrUnknownFeature
	}
	if !configurableDefaults[feature] {
		return ErrFeatureDefaultNotConfigurable
	}
	row := FeatureDefaultOverride{Feature: feature, Enabled: enabled, UpdatedAt: time.Now().UTC(), UpdatedBy: by}
	err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "feature"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at", "updated_by"}),
	}).Create(&row).Error
	if err != nil {
		return err
	}
	if c := defaultsCacheFor(db); c != nil {
		c.invalidate()
	}
	return nil
}

// EffectiveFeatureMetas returns a copy of metas with DefaultValue set to the
// default in force, so an administrator sees what a user without an explicit
// permission gets.
func EffectiveFeatureMetas(db *gorm.DB, metas []FeatureMeta) []FeatureMeta {
	out := make([]FeatureMeta, len(metas))
	for i, m := range metas {
		m.DefaultValue = isDefaultOnFeature(db, m.Key)
		out[i] = m
	}
	return out
}

func knownFeature(feature string) bool {
	for _, f := range AllFeatures {
		if f == feature {
			return true
		}
	}
	return false
}

// featureDefaultsClock is replaced in tests to step past the refresh interval.
var featureDefaultsClock = time.Now

type featureDefaultsCache struct {
	mu       sync.Mutex
	loadedAt time.Time
	values   map[string]bool
}

func (c *featureDefaultsCache) invalidate() {
	c.mu.Lock()
	c.loadedAt = time.Time{}
	c.mu.Unlock()
}

// lookup returns the stored override of a feature, reading the table again
// when the cached copy is older than FeatureDefaultsRefresh. A failed read
// keeps the last values and waits a full interval before it tries again, so a
// struggling database is not asked on every permission check.
func (c *featureDefaultsCache) lookup(db *gorm.DB, feature string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := featureDefaultsClock()
	if c.loadedAt.IsZero() || now.Sub(c.loadedAt) >= FeatureDefaultsRefresh {
		values, err := loadFeatureDefaults(db)
		if err != nil {
			xlog.Warn("Could not read the feature defaults, keeping the last ones", "error", err)
		} else {
			c.values = values
		}
		c.loadedAt = now
	}
	on, ok := c.values[feature]
	return on, ok
}

func loadFeatureDefaults(db *gorm.DB) (map[string]bool, error) {
	var rows []FeatureDefaultOverride
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	values := make(map[string]bool, len(rows))
	for _, r := range rows {
		values[r.Feature] = r.Enabled
	}
	return values, nil
}

// featureDefaultCaches holds one cache per database connection pool. Keying by
// the pool rather than by the *gorm.DB value makes every session derived from
// the same database share one cache.
var featureDefaultCaches sync.Map // *sql.DB -> *featureDefaultsCache

func defaultsCacheFor(db *gorm.DB) *featureDefaultsCache {
	pool, err := db.DB()
	if err != nil || pool == nil {
		// Inside a transaction there is no pool to key on; read directly.
		return nil
	}
	c, _ := featureDefaultCaches.LoadOrStore(pool, &featureDefaultsCache{})
	return c.(*featureDefaultsCache)
}

// featureDefaultOverride returns the override stored for a feature, if any.
func featureDefaultOverride(db *gorm.DB, feature string) (bool, bool) {
	if c := defaultsCacheFor(db); c != nil {
		return c.lookup(db, feature)
	}
	var row FeatureDefaultOverride
	if err := db.Where("feature = ?", feature).Take(&row).Error; err != nil {
		return false, false
	}
	return row.Enabled, true
}
