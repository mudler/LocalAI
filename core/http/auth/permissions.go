package auth

import (
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

const contextKeyPermissions = "auth_permissions"

// contextKeyPermissionsDB holds the database the cached permissions came from,
// so FeatureAllowed can read an administrator's default for a feature.
const contextKeyPermissionsDB = "auth_permissions_db"

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
	c.Set(contextKeyPermissionsDB, db)
	return perm, nil
}

// FeatureAllowed says whether the user of a request may use a feature, for a
// handler that must decide inside the route (for example when one field of a
// body needs a feature the route itself does not). It reads the permissions
// that RequireRouteFeature cached on the request, so it costs no query on a
// route in RouteFeatureRegistry. With no authenticated user (authentication is
// off) it allows; a user whose permissions were not loaded is refused.
func FeatureAllowed(c echo.Context, feature string) bool {
	user := GetUser(c)
	if user == nil || user.Role == RoleAdmin {
		return true
	}
	perm, ok := c.Get(contextKeyPermissions).(*UserPermission)
	if !ok || perm == nil {
		return false
	}
	if val, exists := perm.Permissions[feature]; exists {
		return val
	}
	db, _ := c.Get(contextKeyPermissionsDB).(*gorm.DB)
	return isDefaultOnFeature(db, feature)
}

// Feature name constants — all code must use these, never bare strings.
const (
	// Agent features (default OFF for new users)
	FeatureCollections      = "collections"
	FeatureLocalAIAssistant = "localai_assistant"
	// FeatureAgents gates saved agents (/api/chats/agents) and starting a chat
	// with one. An agent narrows what a chat may use, but a person must still
	// pick it.
	FeatureAgents = "agents"
	// FeatureTriggers gates scheduled and file triggers: unattended runs.
	FeatureTriggers = "triggers"
	// FeatureConnectors gates chat connectors (connectors to chat platforms): a public
	// door to a saved agent.
	FeatureConnectors = "connectors"
	// FeatureMCPJobs gates the agent tasks and jobs API (/api/agent/...), the
	// name master gave it, so a permission stored there still applies.
	FeatureMCPJobs = "mcp_jobs"
	// FeaturePages gates the pages of the workspace (/api/chats/pages...): HTML
	// pages in the user's home, shown in a sandboxed frame. Off for new users
	// until an administrator grants it; administrators always have it.
	FeaturePages = "pages"

	// General features (default OFF for new users)
	FeatureAddons       = "addons"
	FeatureFineTuning   = "fine_tuning"
	FeatureQuantization = "quantization"
	// FeatureP2PPair lets a user pair their own devices over P2P. Off by
	// default; an administrator can change the default for every user
	// (see feature_defaults.go).
	FeatureP2PPair = "p2p_pair"
	// FeaturePlugins gates the plugins in the Claude Code plugin format
	// (/api/plugins): browsing, personal installs and the per-user switch.
	// Unlike the other general features it is on by default; administrator
	// rights cover the rest.
	FeaturePlugins = "plugins"

	// API features (default ON for new users)
	FeatureChat                = "chat"
	FeatureImages              = "images"
	FeatureAudioSpeech         = "audio_speech"
	FeatureAudioTranscription  = "audio_transcription"
	FeatureAudioDiarization    = "audio_diarization"
	FeatureAudioClassification = "audio_classification"
	FeatureVAD                 = "vad"
	FeatureDetection           = "detection"
	FeatureVideo               = "video"
	Feature3D                  = "3d"
	FeatureMotion              = "motion"
	FeatureChats               = "chats"
	FeatureEmbeddings          = "embeddings"
	FeatureSound               = "sound"
	FeatureRealtime            = "realtime"
	FeatureModeration          = "moderation"
	FeatureRerank              = "rerank"
	FeatureTokenize            = "tokenize"
	FeatureMCP                 = "mcp"
	FeatureStores              = "stores"
	FeatureFaceRecognition     = "face_recognition"
	FeatureVoiceRecognition    = "voice_recognition"
	FeatureAudioTransform      = "audio_transform"
	FeatureDecisions           = "decisions"
	FeatureForecast            = "forecast"
	// FeaturePIIFilter gates the synchronous PII analyze/redact service
	// (POST /api/pii/{analyze,redact}). Default ON like the other API
	// features; the admin-only events log is gated separately in-handler.
	FeaturePIIFilter = "pii_filter"
)

// AgentFeatures lists agent-related features (default OFF).
var AgentFeatures = []string{FeatureCollections, FeatureLocalAIAssistant, FeatureAgents, FeatureTriggers, FeatureConnectors, FeatureMCPJobs, FeaturePages}

// GeneralFeatures lists general features (default OFF, except the ones in
// defaultOnGeneralFeatures).
var GeneralFeatures = []string{FeatureAddons, FeatureFineTuning, FeatureQuantization, FeatureP2PPair, FeaturePlugins}

// defaultOnGeneralFeatures are the general features that default to ON.
var defaultOnGeneralFeatures = []string{FeaturePlugins}

// APIFeatures lists API endpoint features (default ON).
var APIFeatures = []string{
	FeatureChat, FeatureImages, FeatureAudioSpeech, FeatureAudioTranscription,
	FeatureAudioDiarization, FeatureAudioClassification,
	FeatureVAD, FeatureDetection, FeatureVideo, Feature3D, FeatureMotion, FeatureEmbeddings, FeatureSound,
	FeatureRealtime, FeatureModeration, FeatureRerank, FeatureTokenize, FeatureMCP, FeatureStores,
	FeatureFaceRecognition, FeatureVoiceRecognition, FeatureAudioTransform,
	FeaturePIIFilter, FeatureDecisions, FeatureForecast,
	FeatureChats,
}

// AllFeatures lists all known features (used by UI and validation).
var AllFeatures = append(append(append([]string{}, AgentFeatures...), GeneralFeatures...), APIFeatures...)

// defaultOnFeatures is the built-in set of features that default to ON when
// absent from a user's permission map. An administrator can override the
// default of a configurable feature (see feature_defaults.go).
var defaultOnFeatures = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range APIFeatures {
		m[f] = true
	}
	for _, f := range defaultOnGeneralFeatures {
		m[f] = true
	}
	return m
}()

// isDefaultOnFeature returns true if the feature defaults to ON when not
// explicitly set. For a feature with a configurable default it reads the
// override that an administrator stored in db; with a nil db it returns the
// built-in default.
func isDefaultOnFeature(db *gorm.DB, feature string) bool {
	if db != nil && configurableDefaults[feature] {
		if on, ok := featureDefaultOverride(db, feature); ok {
			return on
		}
	}
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
		return isDefaultOnFeature(db, feature)
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
			effective[f] = isDefaultOnFeature(db, f)
		}
	}
	return effective
}

// GetExplicitPermissions returns the keys stored in a user's permission map:
// the features set to an explicit value. A feature it does not name follows
// its default, so an administrator's later change to a configurable default
// reaches the user. It only reads: a user without a record gets an empty map
// and no record is created.
func GetExplicitPermissions(db *gorm.DB, userID string) (PermissionMap, error) {
	var perm UserPermission
	err := db.Where("user_id = ?", userID).First(&perm).Error
	if err == gorm.ErrRecordNotFound {
		return PermissionMap{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(PermissionMap, len(perm.Permissions))
	for k, v := range perm.Permissions {
		out[k] = v
	}
	return out, nil
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
