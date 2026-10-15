package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrPauseInPast is returned when a pause end time is not in the future.
var ErrPauseInPast = errors.New("paused_until must be in the future")

const (
	apiKeyPrefix    = "lai-"
	apiKeyRandBytes = 32 // 32 bytes = 64 hex chars
	keyPrefixLen    = 8  // display prefix length (from the random part)
)

// GenerateAPIKey generates a new API key. Returns the plaintext key,
// its HMAC-SHA256 hash, and a display prefix.
func GenerateAPIKey(hmacSecret string) (plaintext, hash, prefix string, err error) {
	b := make([]byte, apiKeyRandBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("failed to generate API key: %w", err)
	}

	randHex := hex.EncodeToString(b)
	plaintext = apiKeyPrefix + randHex
	hash = HashAPIKey(plaintext, hmacSecret)
	prefix = plaintext[:len(apiKeyPrefix)+keyPrefixLen]

	return plaintext, hash, prefix, nil
}

// HashAPIKey returns the HMAC-SHA256 hex digest of the given plaintext key.
// If hmacSecret is empty, falls back to plain SHA-256 for backward compatibility.
func HashAPIKey(plaintext, hmacSecret string) string {
	if hmacSecret == "" {
		h := sha256.Sum256([]byte(plaintext))
		return hex.EncodeToString(h[:])
	}
	mac := hmac.New(sha256.New, []byte(hmacSecret))
	mac.Write([]byte(plaintext))
	return hex.EncodeToString(mac.Sum(nil))
}

// CreateAPIKey generates and stores a new API key for the given user.
// Returns the plaintext key (shown once) and the database record.
func CreateAPIKey(db *gorm.DB, userID, name, role, hmacSecret string, expiresAt *time.Time) (string, *UserAPIKey, error) {
	plaintext, hash, prefix, err := GenerateAPIKey(hmacSecret)
	if err != nil {
		return "", nil, err
	}

	record := &UserAPIKey{
		ID:        uuid.New().String(),
		UserID:    userID,
		Name:      name,
		KeyHash:   hash,
		KeyPrefix: prefix,
		Role:      role,
		ExpiresAt: expiresAt,
	}

	if err := db.Create(record).Error; err != nil {
		return "", nil, fmt.Errorf("failed to store API key: %w", err)
	}

	return plaintext, record, nil
}

// ValidateAPIKey looks up an API key by hashing the plaintext and searching
// the database. Returns the key record if found, or an error.
// Updates LastUsed on successful validation.
func ValidateAPIKey(db *gorm.DB, plaintext, hmacSecret string) (*UserAPIKey, error) {
	hash := HashAPIKey(plaintext, hmacSecret)

	var key UserAPIKey
	if err := db.Preload("User").Where("key_hash = ?", hash).First(&key).Error; err != nil {
		return nil, fmt.Errorf("invalid API key")
	}

	if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
		return nil, fmt.Errorf("API key expired")
	}

	if key.User.Status != StatusActive {
		return nil, fmt.Errorf("user account is not active")
	}

	now := time.Now()
	if key.IsPaused(now) {
		return nil, fmt.Errorf("API key is paused")
	}

	// Update LastUsed
	db.Model(&key).Update("last_used", now)

	return &key, nil
}

// ListAPIKeys returns all API keys for the given user (without plaintext).
func ListAPIKeys(db *gorm.DB, userID string) ([]UserAPIKey, error) {
	var keys []UserAPIKey
	if err := db.Where("user_id = ?", userID).Order("created_at DESC").Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

// RevokeAPIKey deletes an API key. Only the owner can revoke their own key.
func RevokeAPIKey(db *gorm.DB, keyID, userID string) error {
	result := db.Where("id = ? AND user_id = ?", keyID, userID).Delete(&UserAPIKey{})
	if result.RowsAffected == 0 {
		return fmt.Errorf("API key not found or not owned by user")
	}
	return result.Error
}

// SetAPIKeyPause pauses or resumes an API key. Only the owner can change it.
// A paused key is rejected by ValidateAPIKey. Pass disabled=true to pause
// until resumed, or a pausedUntil in the future to pause until that time.
// Pass disabled=false and a nil pausedUntil to resume.
func SetAPIKeyPause(db *gorm.DB, keyID, userID string, disabled bool, pausedUntil *time.Time) error {
	if pausedUntil != nil && !pausedUntil.After(time.Now()) {
		return ErrPauseInPast
	}

	result := db.Model(&UserAPIKey{}).
		Where("id = ? AND user_id = ?", keyID, userID).
		Updates(map[string]any{"disabled": disabled, "paused_until": pausedUntil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("API key not found or not owned by user")
	}
	return nil
}

// CleanExpiredAPIKeys removes all API keys that have passed their expiry time.
func CleanExpiredAPIKeys(db *gorm.DB) error {
	return db.Where("expires_at IS NOT NULL AND expires_at < ?", time.Now()).Delete(&UserAPIKey{}).Error
}
