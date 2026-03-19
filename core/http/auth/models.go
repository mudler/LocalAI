package auth

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// Auth provider constants.
const (
	ProviderLocal  = "local"
	ProviderGitHub = "github"
	ProviderOIDC   = "oidc"
)

// User represents an authenticated user.
type User struct {
	ID        string `gorm:"primaryKey;size:36"`
	Email     string `gorm:"size:255;index"`
	Name      string `gorm:"size:255"`
	AvatarURL string `gorm:"size:512"`
	Provider  string `gorm:"size:50"`  // ProviderLocal, ProviderGitHub, ProviderOIDC
	Subject   string `gorm:"size:255"` // provider-specific user ID
	PasswordHash string `json:"-"`                       // bcrypt hash, empty for OAuth-only users
	Role         string `gorm:"size:20;default:user"`
	Status       string `gorm:"size:20;default:active"` // "active", "pending"
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Session represents a user login session.
type Session struct {
	ID        string `gorm:"primaryKey;size:64"` // 64-char hex token
	UserID    string `gorm:"size:36;index"`
	ExpiresAt time.Time
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
	LastUsed  *time.Time
	User      User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// PermissionMap is a flexible map of feature -> enabled, stored as JSON text.
// Known features: "agents", "skills", "collections", "mcp_jobs".
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
	ID        string     `gorm:"primaryKey;size:36"`
	Code      string     `gorm:"uniqueIndex;not null;size:64"`
	CreatedBy string     `gorm:"size:36;not null"`
	UsedBy    *string    `gorm:"size:36"`
	UsedAt    *time.Time
	ExpiresAt time.Time  `gorm:"not null;index"`
	CreatedAt time.Time
	Creator   User       `gorm:"foreignKey:CreatedBy"`
	Consumer  *User      `gorm:"foreignKey:UsedBy"`
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
