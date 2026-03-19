package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

const (
	sessionDuration         = 30 * 24 * time.Hour // 30 days
	sessionIDBytes          = 32                   // 32 bytes = 64 hex chars
	sessionCookie           = "session"
	sessionRotationInterval = 1 * time.Hour
)

// CreateSession creates a new session for the given user, returning the
// plaintext token (64-char hex string). The stored session ID is the
// HMAC-SHA256 hash of the token.
func CreateSession(db *gorm.DB, userID, hmacSecret string) (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate session ID: %w", err)
	}

	plaintext := hex.EncodeToString(b)
	hash := HashAPIKey(plaintext, hmacSecret)

	now := time.Now()
	session := Session{
		ID:        hash,
		UserID:    userID,
		ExpiresAt: now.Add(sessionDuration),
		RotatedAt: now,
	}

	if err := db.Create(&session).Error; err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	return plaintext, nil
}

// ValidateSession hashes the plaintext token and looks up the session.
// Returns the associated user and session, or (nil, nil) if not found/expired.
func ValidateSession(db *gorm.DB, token, hmacSecret string) (*User, *Session) {
	hash := HashAPIKey(token, hmacSecret)

	var session Session
	if err := db.Preload("User").Where("id = ? AND expires_at > ?", hash, time.Now()).First(&session).Error; err != nil {
		return nil, nil
	}
	if session.User.Status != StatusActive {
		return nil, nil
	}
	return &session.User, &session
}

// DeleteSession removes a session by hashing the plaintext token.
func DeleteSession(db *gorm.DB, token, hmacSecret string) error {
	hash := HashAPIKey(token, hmacSecret)
	return db.Where("id = ?", hash).Delete(&Session{}).Error
}

// CleanExpiredSessions removes all sessions that have passed their expiry time.
func CleanExpiredSessions(db *gorm.DB) error {
	return db.Where("expires_at < ?", time.Now()).Delete(&Session{}).Error
}

// DeleteUserSessions removes all sessions for the given user.
func DeleteUserSessions(db *gorm.DB, userID string) error {
	return db.Where("user_id = ?", userID).Delete(&Session{}).Error
}

// RotateSession creates a new session for the same user, deletes the old one,
// and returns the new plaintext token.
func RotateSession(db *gorm.DB, oldSession *Session, hmacSecret string) (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate session ID: %w", err)
	}

	plaintext := hex.EncodeToString(b)
	hash := HashAPIKey(plaintext, hmacSecret)

	now := time.Now()
	newSession := Session{
		ID:        hash,
		UserID:    oldSession.UserID,
		ExpiresAt: oldSession.ExpiresAt,
		RotatedAt: now,
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&newSession).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", oldSession.ID).Delete(&Session{}).Error
	})
	if err != nil {
		return "", fmt.Errorf("failed to rotate session: %w", err)
	}

	return plaintext, nil
}

// MaybeRotateSession checks if the session should be rotated and does so if needed.
// Called from the auth middleware after successful cookie-based authentication.
func MaybeRotateSession(c echo.Context, db *gorm.DB, session *Session, hmacSecret string) {
	if session == nil {
		return
	}

	rotatedAt := session.RotatedAt
	if rotatedAt.IsZero() {
		rotatedAt = session.CreatedAt
	}

	if time.Since(rotatedAt) < sessionRotationInterval {
		return
	}

	newToken, err := RotateSession(db, session, hmacSecret)
	if err != nil {
		// Rotation failure is non-fatal; the old session remains valid
		return
	}

	SetSessionCookie(c, newToken)
}

// isSecure returns true when the request arrived over HTTPS, either directly
// or via a reverse proxy that sets X-Forwarded-Proto.
func isSecure(c echo.Context) bool {
	return c.Scheme() == "https"
}

// SetSessionCookie sets the session cookie on the response.
func SetSessionCookie(c echo.Context, sessionID string) {
	cookie := &http.Cookie{
		Name:     sessionCookie,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure(c),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionDuration.Seconds()),
	}
	c.SetCookie(cookie)
}

// SetTokenCookie sets an httpOnly "token" cookie for legacy API key auth.
func SetTokenCookie(c echo.Context, token string) {
	cookie := &http.Cookie{
		Name:     "token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure(c),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionDuration.Seconds()),
	}
	c.SetCookie(cookie)
}

// ClearSessionCookie clears the session cookie.
func ClearSessionCookie(c echo.Context) {
	cookie := &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure(c),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
	c.SetCookie(cookie)
}
