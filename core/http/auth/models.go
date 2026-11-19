package auth

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Auth provider constants.
const (
	ProviderLocal       = "local"
	ProviderGitHub      = "github"
	ProviderOIDC        = "oidc"
	ProviderAgentWorker = "agent-worker"
)

// User represents an authenticated user.
type User struct {
	ID           string `gorm:"primaryKey;size:36"`
	Email        string `gorm:"size:255;index"`
	Name         string `gorm:"size:255"`
	AvatarURL    string `gorm:"size:512"`
	Provider     string `gorm:"size:50"`  // ProviderLocal, ProviderGitHub, ProviderOIDC
	Subject      string `gorm:"size:255"` // provider-specific user ID
	PasswordHash string `json:"-"`        // bcrypt hash, empty for OAuth-only users
	Role         string `gorm:"size:20;default:user"`
	Status       string `gorm:"size:20;default:active"` // "active", "pending"
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Session represents a user login session.
type Session struct {
	ID        string `gorm:"primaryKey;size:64"` // HMAC-SHA256 hash of session token
	UserID    string `gorm:"size:36;index"`
	ExpiresAt time.Time
	RotatedAt time.Time
	CreatedAt time.Time
	User      User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// UserAPIKey represents a user-generated API key for programmatic access.
type UserAPIKey struct {
	ID        string `gorm:"primaryKey;size:36"`
	UserID    string `gorm:"size:36;index"`
	Name      string `gorm:"size:255"` // user-provided label
	KeyHash   string `gorm:"size:64;uniqueIndex"`
	KeyPrefix string `gorm:"size:12"` // first 8 chars of key for display
	Role      string `gorm:"size:20"`
	CreatedAt time.Time
	ExpiresAt *time.Time `gorm:"index"`
	LastUsed  *time.Time
	// Disabled pauses the key until the owner resumes it.
	Disabled bool
	// PausedUntil pauses the key until the given time; the key becomes
	// active again by itself once that time has passed.
	PausedUntil *time.Time
	// Kind tells what the key is. Empty is a user key, which acts as its
	// owner. KeyKindClient is the key of a paired client: it holds only the
	// features of Features and nothing else.
	Kind string `gorm:"size:20;index"`
	// Features is the allow-list of a client key. Unused for a user key.
	Features PermissionMap `gorm:"type:text"`
	// Addons lists the add-ons a client key may reach, by id. Deny by default:
	// a key with the client_addons feature and no entry reaches no add-on.
	Addons AddonList `gorm:"type:text"`
	// Models lists the models a client key may use, by name. Deny by default:
	// a key with the client_inference feature and no entry uses no model.
	Models ModelList `gorm:"type:text"`
	// ReadFolders and WriteFolder are the folder grant of a client key
	// (client_folders): folders of the owner's workspace the key may read, and
	// the one it may write. Deny by default: empty means no access.
	ReadFolders FolderList `gorm:"type:text"`
	WriteFolder string     `gorm:"size:255"`
	User        User       `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// IsPaused reports whether the key is paused at the given time.
func (k *UserAPIKey) IsPaused(now time.Time) bool {
	return k.Disabled || (k.PausedUntil != nil && k.PausedUntil.After(now))
}

// Errors of CheckUsable. Their text is what a client of the API reads.
var (
	errKeyExpired   = errors.New("API key expired")
	errUserInactive = errors.New("user account is not active")
	errKeyPaused    = errors.New("API key is paused")
	// errOwnerNotAdmin is the refusal of a client key whose owner lost the
	// administrator role.
	errOwnerNotAdmin = errors.New("the owner of this key is no longer an administrator")
)

// CheckUsable is the one rule for a key that is used now: it is not expired,
// its owner is an active account, and it is not paused. A client key also
// needs an owner that is still an administrator: only an administrator can pair
// a client, and a client has no feature check, model allow-list or quota of the
// owner account, so a client of a demoted owner would go on past the limits that
// another administrator set. The key must be loaded
// with its User. Every place that accepts a key (request, add-on identity,
// host operation) asks this, so a new key state is added once.
func (k *UserAPIKey) CheckUsable(now time.Time) error {
	if k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
		return errKeyExpired
	}
	if k.User.Status != StatusActive {
		return errUserInactive
	}
	if k.IsPaused(now) {
		return errKeyPaused
	}
	if k.IsClientKey() && k.User.Role != RoleAdmin {
		return errOwnerNotAdmin
	}
	return nil
}

// PermissionMap is a flexible map of feature -> enabled, stored as JSON text.
// Known features: see AllFeatures. A stored key that is no longer known is ignored.
// New features can be added without schema changes.
type PermissionMap map[string]bool

// Value implements driver.Valuer for GORM JSON serialization.
func (p PermissionMap) Value() (driver.Value, error) {
	if p == nil {
		return "{}", nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal PermissionMap: %w", err)
	}
	return string(b), nil
}

// Scan implements sql.Scanner for GORM JSON deserialization.
func (p *PermissionMap) Scan(value any) error {
	if value == nil {
		*p = PermissionMap{}
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case string:
		bytes = []byte(v)
	case []byte:
		bytes = v
	default:
		return fmt.Errorf("cannot scan %T into PermissionMap", value)
	}
	return json.Unmarshal(bytes, p)
}

// InviteCode represents an admin-generated invitation for user registration.
type InviteCode struct {
	ID         string  `gorm:"primaryKey;size:36"`
	Code       string  `gorm:"uniqueIndex;not null;size:64"` // HMAC-SHA256 hash of invite code
	CodePrefix string  `gorm:"size:12"`                      // first 8 chars for admin display
	CreatedBy  string  `gorm:"size:36;not null"`
	UsedBy     *string `gorm:"size:36"`
	UsedAt     *time.Time
	ExpiresAt  time.Time `gorm:"not null;index"`
	CreatedAt  time.Time
	Creator    User  `gorm:"foreignKey:CreatedBy"`
	Consumer   *User `gorm:"foreignKey:UsedBy"`
}

// ModelAllowlist controls which models a user can access.
// When Enabled is false (default), all models are allowed.
type ModelAllowlist struct {
	Enabled bool     `json:"enabled"`
	Models  []string `json:"models,omitempty"`
}

// Value implements driver.Valuer for GORM JSON serialization.
func (m ModelAllowlist) Value() (driver.Value, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ModelAllowlist: %w", err)
	}
	return string(b), nil
}

// Scan implements sql.Scanner for GORM JSON deserialization.
func (m *ModelAllowlist) Scan(value any) error {
	if value == nil {
		*m = ModelAllowlist{}
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case string:
		bytes = []byte(v)
	case []byte:
		bytes = v
	default:
		return fmt.Errorf("cannot scan %T into ModelAllowlist", value)
	}
	return json.Unmarshal(bytes, m)
}

// UserPermission stores per-user feature permissions.
type UserPermission struct {
	ID            string         `gorm:"primaryKey;size:36"`
	UserID        string         `gorm:"size:36;uniqueIndex"`
	Permissions   PermissionMap  `gorm:"type:text"`
	AllowedModels ModelAllowlist `gorm:"type:text"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	User          User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}
