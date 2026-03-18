package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	apiKeyPrefix    = "lai-"
	apiKeyRandBytes = 32 // 32 bytes = 64 hex chars
	keyPrefixLen    = 8  // display prefix length (from the random part)
)

// GenerateAPIKey generates a new API key. Returns the plaintext key,
// its SHA-256 hash, and a display prefix.
func GenerateAPIKey() (plaintext, hash, prefix string, err error) {
	b := make([]byte, apiKeyRandBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("failed to generate API key: %w", err)
	}

	randHex := hex.EncodeToString(b)
	plaintext = apiKeyPrefix + randHex
	hash = HashAPIKey(plaintext)
	prefix = plaintext[:len(apiKeyPrefix)+keyPrefixLen]

	return plaintext, hash, prefix, nil
}

// HashAPIKey returns the SHA-256 hex digest of the given plaintext key.
func HashAPIKey(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}

// CreateAPIKey generates and stores a new API key for the given user.
// Returns the plaintext key (shown once) and the database record.
func CreateAPIKey(db *gorm.DB, userID, name, role string) (string, *UserAPIKey, error) {
	plaintext, hash, prefix, err := GenerateAPIKey()
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
	}

	if err := db.Create(record).Error; err != nil {
		return "", nil, fmt.Errorf("failed to store API key: %w", err)
	}

	return plaintext, record, nil
}

// ValidateAPIKey looks up an API key by hashing the plaintext and searching
// the database. Returns the key record if found, or an error.
// Updates LastUsed on successful validation.
func ValidateAPIKey(db *gorm.DB, plaintext string) (*UserAPIKey, error) {
	hash := HashAPIKey(plaintext)

	var key UserAPIKey
	if err := db.Preload("User").Where("key_hash = ?", hash).First(&key).Error; err != nil {
		return nil, fmt.Errorf("invalid API key")
	}

	// Update LastUsed
	now := time.Now()
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
