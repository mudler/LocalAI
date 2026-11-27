package p2ppair

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Limits on how users pair their own devices that are fixed in code. The
// administrator edits the others in UserPairingPolicy.
const (
	// MaxUserMintsPerHour is how many invites one user may mint in an hour.
	MaxUserMintsPerHour = 10
	// MaxUserPendingInstance is how many sign-in invites may be pending on
	// the whole instance at once. Every pending invite keeps pairing open to
	// unknown peers at the connection level.
	MaxUserPendingInstance = 20
)

// UserPairingPolicy is the setting p2p_user_pairing: what an invite that a
// user mints for their own device carries, and the limits of each user. The
// user chooses only the label and the life of the invite.
type UserPairingPolicy struct {
	// Relays is RelayAuto or RelayNone.
	Relays         RelayMode `json:"relays"`
	AllowOpenRelay bool      `json:"allow_open_relay"`
	OmitLANAddrs   bool      `json:"omit_lan_addrs"`
	// MaxDevices is how many active sign-in devices one user may have.
	MaxDevices int `json:"max_devices"`
	// MaxPending is how many pending sign-in invites one user may have.
	MaxPending int `json:"max_pending"`
	// MaxTTLSeconds is the longest life of an invite a user mints.
	MaxTTLSeconds int `json:"max_ttl_seconds"`
}

// DefaultUserPairingPolicy is the policy until an administrator saves one.
func DefaultUserPairingPolicy() UserPairingPolicy {
	return UserPairingPolicy{Relays: RelayAuto, MaxDevices: 5, MaxPending: 2, MaxTTLSeconds: 3600}
}

// Validate checks the policy. Every error wraps ErrMalformed.
func (p UserPairingPolicy) Validate() error {
	switch {
	case p.Relays != RelayAuto && p.Relays != RelayNone:
		return fmt.Errorf("%w: relays must be \"auto\" or \"none\"", ErrMalformed)
	case p.MaxDevices < 1 || p.MaxDevices > 100:
		return fmt.Errorf("%w: max_devices must be between 1 and 100", ErrMalformed)
	case p.MaxPending < 1 || p.MaxPending > MaxUserPendingInstance:
		return fmt.Errorf("%w: max_pending must be between 1 and %d", ErrMalformed, MaxUserPendingInstance)
	case p.MaxTTLSeconds < 60 || p.MaxTTLSeconds > int(MaxTTL.Seconds()):
		return fmt.Errorf("%w: max_ttl_seconds must be between 60 and %d", ErrMalformed, int(MaxTTL.Seconds()))
	}
	return nil
}

// settingRow is one row of p2p_settings: a P2P setting that every frontend of
// a cluster shares, as JSON.
type settingRow struct {
	Key       string `gorm:"primaryKey;size:64"`
	Value     string `gorm:"type:text"`
	UpdatedAt time.Time
	UpdatedBy string `gorm:"size:64"`
}

// TableName is the table of the shared P2P settings.
func (settingRow) TableName() string { return "p2p_settings" }

const userPairingKey = "user_pairing"

// LoadUserPairingPolicy reads the policy from db, or the default when none
// was saved. A field the stored JSON lacks keeps its default, so a policy
// saved by an older version stays valid.
func LoadUserPairingPolicy(ctx context.Context, db *gorm.DB) (UserPairingPolicy, error) {
	p := DefaultUserPairingPolicy()
	var row settingRow
	err := db.WithContext(ctx).Where("key = ?", userPairingKey).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(row.Value), &p); err != nil {
		return DefaultUserPairingPolicy(), fmt.Errorf("p2ppair: stored user pairing policy: %w", err)
	}
	if p.Validate() != nil {
		return DefaultUserPairingPolicy(), nil
	}
	return p, nil
}

// SaveUserPairingPolicy validates p and stores it in db. by is the ID of the
// administrator who made the change.
func SaveUserPairingPolicy(ctx context.Context, db *gorm.DB, p UserPairingPolicy, by string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	row := settingRow{Key: userPairingKey, Value: string(b), UpdatedAt: time.Now().UTC(), UpdatedBy: by}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at", "updated_by"}),
	}).Create(&row).Error
}
