package auth

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Feature name constants — all code must use these, never bare strings.
const (
	FeatureAgents      = "agents"
	FeatureSkills      = "skills"
	FeatureCollections = "collections"
	FeatureMCPJobs     = "mcp_jobs"
)

// AllFeatures lists all known features (used by UI and validation).
var AllFeatures = []string{FeatureAgents, FeatureSkills, FeatureCollections, FeatureMCPJobs}

// GetUserPermissions returns the permission record for a user, creating a default
// (empty map = all disabled) if none exists.
func GetUserPermissions(db *gorm.DB, userID string) (*UserPermission, error) {
	var perm UserPermission
	err := db.Where("user_id = ?", userID).First(&perm).Error
	if err == gorm.ErrRecordNotFound {
		perm = UserPermission{
			ID:          uuid.New().String(),
			UserID:      userID,
			Permissions: PermissionMap{},
		}
		if err := db.Create(&perm).Error; err != nil {
			return nil, err
		}
		return &perm, nil
	}
	if err != nil {
		return nil, err
	}
	return &perm, nil
}

// UpdateUserPermissions upserts the permission map for a user.
func UpdateUserPermissions(db *gorm.DB, userID string, perms PermissionMap) error {
	var perm UserPermission
	err := db.Where("user_id = ?", userID).First(&perm).Error
	if err == gorm.ErrRecordNotFound {
		perm = UserPermission{
			ID:          uuid.New().String(),
			UserID:      userID,
			Permissions: perms,
		}
		return db.Create(&perm).Error
	}
	if err != nil {
		return err
	}
	perm.Permissions = perms
	return db.Save(&perm).Error
}

// HasFeatureAccess returns true if the user is an admin or has the given feature enabled.
func HasFeatureAccess(db *gorm.DB, user *User, feature string) bool {
	if user == nil {
		return false
	}
	if user.Role == RoleAdmin {
		return true
	}
	perm, err := GetUserPermissions(db, user.ID)
	if err != nil {
		return false
	}
	return perm.Permissions[feature]
}

// GetPermissionMapForUser returns the effective permission map for a user.
// Admins get all features as true (virtual).
func GetPermissionMapForUser(db *gorm.DB, user *User) PermissionMap {
	if user == nil {
		return PermissionMap{}
	}
	if user.Role == RoleAdmin {
		m := PermissionMap{}
		for _, f := range AllFeatures {
			m[f] = true
		}
		return m
	}
	perm, err := GetUserPermissions(db, user.ID)
	if err != nil {
		return PermissionMap{}
	}
	return perm.Permissions
}
