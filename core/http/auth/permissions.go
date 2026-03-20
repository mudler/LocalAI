package auth

import (
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

const contextKeyPermissions = "auth_permissions"

// GetCachedUserPermissions returns the user's permission record, using a
// request-scoped cache stored in the echo context. This avoids duplicate
// DB lookups when multiple middlewares (RequireRouteFeature, RequireModelAccess)
// both need permissions in the same request.
func GetCachedUserPermissions(c echo.Context, db *gorm.DB, userID string) (*UserPermission, error) {
	if perm, ok := c.Get(contextKeyPermissions).(*UserPermission); ok && perm != nil {
		return perm, nil
	}
	perm, err := GetUserPermissions(db, userID)
	if err != nil {
		return nil, err
	}
	c.Set(contextKeyPermissions, perm)
	return perm, nil
}

// Feature name constants — all code must use these, never bare strings.
const (
	// Agent features (default OFF for new users)
	FeatureAgents      = "agents"
	FeatureSkills      = "skills"
	FeatureCollections = "collections"
	FeatureMCPJobs     = "mcp_jobs"
	FeatureFineTuning  = "fine_tuning"

	// API features (default ON for new users)
	FeatureChat              = "chat"
	FeatureImages            = "images"
	FeatureAudioSpeech       = "audio_speech"
	FeatureAudioTranscription = "audio_transcription"
	FeatureVAD               = "vad"
	FeatureDetection         = "detection"
	FeatureVideo             = "video"
	FeatureEmbeddings        = "embeddings"
	FeatureSound             = "sound"
	FeatureRealtime          = "realtime"
	FeatureRerank            = "rerank"
	FeatureTokenize          = "tokenize"
	FeatureMCP               = "mcp"
	FeatureStores            = "stores"
)

// AgentFeatures lists agent-related features (default OFF).
var AgentFeatures = []string{FeatureAgents, FeatureSkills, FeatureCollections, FeatureMCPJobs, FeatureFineTuning}

// APIFeatures lists API endpoint features (default ON).
var APIFeatures = []string{
	FeatureChat, FeatureImages, FeatureAudioSpeech, FeatureAudioTranscription,
	FeatureVAD, FeatureDetection, FeatureVideo, FeatureEmbeddings, FeatureSound,
	FeatureRealtime, FeatureRerank, FeatureTokenize, FeatureMCP, FeatureStores,
}

// AllFeatures lists all known features (used by UI and validation).
var AllFeatures = append(append([]string{}, AgentFeatures...), APIFeatures...)

// defaultOnFeatures is the set of features that default to ON when absent from a user's permission map.
var defaultOnFeatures = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range APIFeatures {
		m[f] = true
	}
	return m
}()

// isDefaultOnFeature returns true if the feature defaults to ON when not explicitly set.
func isDefaultOnFeature(feature string) bool {
	return defaultOnFeatures[feature]
}

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
// When a feature key is absent from the user's permission map, it checks whether the
// feature defaults to ON (API features) or OFF (agent features) for backward compatibility.
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
	val, exists := perm.Permissions[feature]
	if !exists {
		return isDefaultOnFeature(feature)
	}
	return val
}

// GetPermissionMapForUser returns the effective permission map for a user.
// Admins get all features as true (virtual).
// For regular users, absent keys are filled with their defaults so the
// UI/API always returns a complete picture.
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
	// Fill in defaults for absent keys
	effective := PermissionMap{}
	for _, f := range AllFeatures {
		val, exists := perm.Permissions[f]
		if exists {
			effective[f] = val
		} else {
			effective[f] = isDefaultOnFeature(f)
		}
	}
	return effective
}

// GetModelAllowlist returns the model allowlist for a user.
func GetModelAllowlist(db *gorm.DB, userID string) ModelAllowlist {
	perm, err := GetUserPermissions(db, userID)
	if err != nil {
		return ModelAllowlist{}
	}
	return perm.AllowedModels
}

// UpdateModelAllowlist updates the model allowlist for a user.
func UpdateModelAllowlist(db *gorm.DB, userID string, allowlist ModelAllowlist) error {
	perm, err := GetUserPermissions(db, userID)
	if err != nil {
		return err
	}
	perm.AllowedModels = allowlist
	return db.Save(perm).Error
}

// IsModelAllowed returns true if the user is allowed to use the given model.
// Admins always have access. If the allowlist is not enabled, all models are allowed.
func IsModelAllowed(db *gorm.DB, user *User, modelName string) bool {
	if user == nil {
		return false
	}
	if user.Role == RoleAdmin {
		return true
	}
	allowlist := GetModelAllowlist(db, user.ID)
	if !allowlist.Enabled {
		return true
	}
	for _, m := range allowlist.Models {
		if m == modelName {
			return true
		}
	}
	return false
}
