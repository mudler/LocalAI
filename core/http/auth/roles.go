package auth

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// AssignRole determines the role for a new user.
// First user in the database becomes admin. If adminEmail is set and matches,
// the user becomes admin. Otherwise, the user gets the "user" role.
func AssignRole(db *gorm.DB, email, adminEmail string) string {
	var count int64
	db.Model(&User{}).Count(&count)
	if count == 0 {
		return RoleAdmin
	}

	if adminEmail != "" && strings.EqualFold(email, adminEmail) {
		return RoleAdmin
	}

	return RoleUser
}

// MaybePromote promotes a user to admin on login if their email matches
// adminEmail. It does not demote existing admins. Returns true if the user
// was promoted.
func MaybePromote(db *gorm.DB, user *User, adminEmail string) bool {
	if user.Role == RoleAdmin {
		return false
	}

	if adminEmail != "" && strings.EqualFold(user.Email, adminEmail) {
		user.Role = RoleAdmin
		db.Model(user).Update("role", RoleAdmin)
		return true
	}

	return false
}

// ValidateInvite checks that an invite code exists, is unused, and has not expired.
func ValidateInvite(db *gorm.DB, code string) (*InviteCode, error) {
	var invite InviteCode
	if err := db.Where("code = ?", code).First(&invite).Error; err != nil {
		return nil, fmt.Errorf("invite code not found")
	}
	if invite.UsedBy != nil {
		return nil, fmt.Errorf("invite code already used")
	}
	if time.Now().After(invite.ExpiresAt) {
		return nil, fmt.Errorf("invite code expired")
	}
	return &invite, nil
}

// ConsumeInvite marks an invite code as used by the given user.
func ConsumeInvite(db *gorm.DB, invite *InviteCode, userID string) {
	now := time.Now()
	invite.UsedBy = &userID
	invite.UsedAt = &now
	db.Save(invite)
}

// NeedsInviteOrApproval returns true if registration gating applies for the given mode.
// Admins (first user or matching adminEmail) are never gated.
func NeedsInviteOrApproval(db *gorm.DB, email, adminEmail, registrationMode string) bool {
	if registrationMode != "approval" && registrationMode != "invite" {
		return false
	}
	// Admin email is never gated
	if adminEmail != "" && strings.EqualFold(email, adminEmail) {
		return false
	}
	// First user is never gated
	var count int64
	db.Model(&User{}).Count(&count)
	if count == 0 {
		return false
	}
	return true
}
