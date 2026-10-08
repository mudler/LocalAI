package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Names of the settings that every replica shares through the database.
//
// A runtime setting of the application lives in a file of one process, so two
// replicas can hold two values. These settings decide what the whole cluster
// does, so they live in a table. No credential is stored here: NATS keys and
// TLS files stay on each replica, as they always did.
const (
	// SettingNATSURL is the address of the NATS server that the frontends use.
	SettingNATSURL = "nats.url"
	// SettingNATSWorkerURL is the address that workers are told to use, when it
	// differs from the one the frontends use. It defaults to SettingNATSURL.
	SettingNATSWorkerURL = "nats.worker_url"
	// SettingPrepareTimeout is how long a change of carrier waits for every
	// replica to be ready.
	SettingPrepareTimeout = "switch.prepare_timeout"
	// SettingTransitionWindow is how long a change waits for every replica to
	// confirm that it uses the new carrier.
	SettingTransitionWindow = "switch.transition_window"
	// SettingMaxDrain is how long the previous carrier stays attached after a
	// change.
	SettingMaxDrain = "switch.max_drain"
)

// ErrUnknownSetting means a key is not one of the cluster settings.
var ErrUnknownSetting = errors.New("unknown cluster setting")

var knownSettings = map[string]func(string) error{
	SettingNATSURL:          CheckNATSURL,
	SettingNATSWorkerURL:    CheckNATSURL,
	SettingPrepareTimeout:   durationSetting,
	SettingTransitionWindow: durationSetting,
	SettingMaxDrain:         durationSetting,
}

// MinTiming is the shortest wait of a change that the cluster accepts. The
// replicas read the row every two seconds, so a shorter wait would abort every
// change before a replica could see it, and a value such as 1ns is a typo for
// a value that is meant to be long.
const MinTiming = 5 * time.Second

func durationSetting(v string) error {
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("not a duration: %w", err)
	}
	if d < MinTiming {
		return fmt.Errorf("a wait must be at least %s, not %s", MinTiming, d)
	}
	return nil
}

// CheckSetting says whether value may be stored for key. It stores nothing, so a
// caller with several settings can refuse all of them before it writes one.
func CheckSetting(key, value string) error { return checkSetting(key, value) }

// Timings are the waits of a change.
type Timings struct {
	// PrepareTimeout is how long prepare waits for every live replica.
	PrepareTimeout time.Duration `swaggertype:"integer"`
	// TransitionWindow is how long commit waits for every live replica to
	// confirm.
	TransitionWindow time.Duration `swaggertype:"integer"`
	// MaxDrain is how long the previous carrier stays attached after the commit.
	// Work that is still running on it then is failed by the reaper.
	MaxDrain time.Duration `swaggertype:"integer"`
}

// DefaultTimings are used for a timing that nothing sets.
var DefaultTimings = Timings{
	PrepareTimeout:   time.Minute,
	TransitionWindow: 2 * time.Minute,
	MaxDrain:         15 * time.Minute,
}

func (t Timings) withDefaults() Timings {
	if t.PrepareTimeout <= 0 {
		t.PrepareTimeout = DefaultTimings.PrepareTimeout
	}
	if t.TransitionWindow <= 0 {
		t.TransitionWindow = DefaultTimings.TransitionWindow
	}
	if t.MaxDrain <= 0 {
		t.MaxDrain = DefaultTimings.MaxDrain
	}
	return t
}

// Setting is one row of table cluster_settings.
type Setting struct {
	Key       string    `gorm:"primaryKey;size:64"`
	Value     string    `gorm:"type:text;not null"`
	UpdatedBy string    `gorm:"size:255"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (Setting) TableName() string { return "cluster_settings" }

// SettingsStore reads and writes the cluster settings.
type SettingsStore struct{ db *gorm.DB }

// NewSettingsStore creates the table when it is missing.
func NewSettingsStore(db *gorm.DB) (*SettingsStore, error) {
	if err := db.AutoMigrate(&Setting{}); err != nil {
		return nil, fmt.Errorf("migrating cluster_settings: %w", err)
	}
	return &SettingsStore{db: db}, nil
}

func checkSetting(key, value string) error {
	validate, ok := knownSettings[key]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownSetting, key)
	}
	if validate != nil {
		if err := validate(value); err != nil {
			return fmt.Errorf("setting %s: %w", key, err)
		}
	}
	return nil
}

// Get returns the value of a setting, and false when it was never stored.
func (s *SettingsStore) Get(ctx context.Context, key string) (string, bool, error) {
	var row Setting
	err := s.db.WithContext(ctx).Where("key = ?", key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading cluster setting %s: %w", key, err)
	}
	return row.Value, true, nil
}

// Set stores a value, replacing the one that was there.
func (s *SettingsStore) Set(ctx context.Context, key, value, by string) error {
	if err := checkSetting(key, value); err != nil {
		return err
	}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{"value": value, "updated_by": by, "updated_at": gorm.Expr("now()")}),
	}).Create(&Setting{Key: key, Value: value, UpdatedBy: by}).Error
	if err != nil {
		return fmt.Errorf("storing cluster setting %s: %w", key, err)
	}
	return nil
}

// SetIfAbsent stores a value only when the key has none, and reports whether it
// did. Replicas that start together can all call it: the first insert wins.
func (s *SettingsStore) SetIfAbsent(ctx context.Context, key, value, by string) (bool, error) {
	if err := checkSetting(key, value); err != nil {
		return false, err
	}
	res := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&Setting{Key: key, Value: value, UpdatedBy: by})
	if res.Error != nil {
		return false, fmt.Errorf("storing cluster setting %s: %w", key, res.Error)
	}
	return res.RowsAffected == 1, nil
}

// Clear removes a setting.
func (s *SettingsStore) Clear(ctx context.Context, key string) error {
	if _, ok := knownSettings[key]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownSetting, key)
	}
	if err := s.db.WithContext(ctx).Where("key = ?", key).Delete(&Setting{}).Error; err != nil {
		return fmt.Errorf("clearing cluster setting %s: %w", key, err)
	}
	return nil
}

// All returns every stored setting by key.
func (s *SettingsStore) All(ctx context.Context) (map[string]string, error) {
	var rows []Setting
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("reading cluster settings: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

// Timings returns the timings of a change. A timing that the cluster did not set
// comes from fallback, and one that fallback leaves at zero comes from
// DefaultTimings.
func (s *SettingsStore) Timings(ctx context.Context, fallback Timings) (Timings, error) {
	all, err := s.All(ctx)
	if err != nil {
		return Timings{}, err
	}
	t := fallback
	for key, dst := range map[string]*time.Duration{
		SettingPrepareTimeout:   &t.PrepareTimeout,
		SettingTransitionWindow: &t.TransitionWindow,
		SettingMaxDrain:         &t.MaxDrain,
	} {
		if v, ok := all[key]; ok {
			if d, err := time.ParseDuration(v); err == nil && d >= MinTiming {
				*dst = d
			}
		}
	}
	return t.withDefaults(), nil
}
