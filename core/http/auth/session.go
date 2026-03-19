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
	sessionDuration = 30 * 24 * time.Hour // 30 days
	sessionIDBytes  = 32                   // 32 bytes = 64 hex chars
	sessionCookie   = "session"
)

// CreateSession creates a new session for the given user, returning the
// session ID (64-char hex string).
func CreateSession(db *gorm.DB, userID string) (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate session ID: %w", err)
	}

	sessionID := hex.EncodeToString(b)

	session := Session{
		ID:        sessionID,
		UserID:    userID,
		ExpiresAt: time.Now().Add(sessionDuration),
	}

	if err := db.Create(&session).Error; err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	return sessionID, nil
}

// ValidateSession looks up a session by ID and returns the associated user.
// Returns nil if the session is not found or expired.
func ValidateSession(db *gorm.DB, sessionID string) *User {
	var session Session
	if err := db.Preload("User").Where("id = ? AND expires_at > ?", sessionID, time.Now()).First(&session).Error; err != nil {
		return nil
	}
	if session.User.Status != StatusActive {
		return nil
	}
	return &session.User
}

// DeleteSession removes a session from the database.
func DeleteSession(db *gorm.DB, sessionID string) error {
	return db.Where("id = ?", sessionID).Delete(&Session{}).Error
}

// CleanExpiredSessions removes all sessions that have passed their expiry time.
func CleanExpiredSessions(db *gorm.DB) error {
	return db.Where("expires_at < ?", time.Now()).Delete(&Session{}).Error
}

// isSecure returns true when the request arrived over HTTPS, either directly
// or via a reverse proxy that sets X-Forwarded-Proto.
func isSecure(c echo.Context) bool {
	return c.Scheme() == "https"
}

// DeleteUserSessions removes all sessions for the given user.
func DeleteUserSessions(db *gorm.DB, userID string) error {
	return db.Where("user_id = ?", userID).Delete(&Session{}).Error
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
