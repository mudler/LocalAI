package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

const (
	sessionDuration         = 30 * 24 * time.Hour // 30 days
	sessionIDBytes          = 32                  // 32 bytes = 64 hex chars
	sessionCookie           = "session"
	sessionRotationInterval = 1 * time.Hour
)

// CreateSession creates a new session for the given user, returning the
// plaintext token (64-char hex string). The stored session ID is the
// HMAC-SHA256 hash of the token.
func CreateSession(db *gorm.DB, userID, hmacSecret string) (string, error) {
	return createSession(db, userID, nil, hmacSecret)
}

// CreateDeviceSession creates a session for userID that a paired P2P device
// obtained with its credential. The session carries the device ID, so that
// DeleteDeviceSessions ends it when the device is revoked. A device keeps one
// live session: the new one replaces the earlier sessions of the device, in
// one transaction, so an app that signs in again and again does not fill the
// table.
func CreateDeviceSession(db *gorm.DB, userID, deviceID, hmacSecret string) (string, error) {
	if deviceID == "" {
		return "", errors.New("a device session needs a device ID")
	}
	var token string
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := DeleteDeviceSessions(tx, deviceID); err != nil {
			return fmt.Errorf("failed to end the earlier sessions of the device: %w", err)
		}
		var err error
		token, err = createSession(tx, userID, &deviceID, hmacSecret)
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// DeleteDeviceSessions removes the sessions that a device obtained with its
// credential. An empty device ID removes nothing.
func DeleteDeviceSessions(db *gorm.DB, deviceID string) error {
	if deviceID == "" {
		return nil
	}
	return db.Where("device_id = ?", deviceID).Delete(&Session{}).Error
}

func createSession(db *gorm.DB, userID string, deviceID *string, hmacSecret string) (string, error) {
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
		DeviceID:  deviceID,
	}

	if err := db.Create(&session).Error; err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	return plaintext, nil
}

// ValidateSession hashes the plaintext token and looks up the session.
// Returns the associated user and session, or (nil, nil) if not found/expired.
// It does not check the binding of a device session to its device: a request
// path must use ValidateRequestSession.
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

// errSessionGone is returned by RotateSession when the old session no longer
// exists.
var errSessionGone = errors.New("the session no longer exists")

// RotateSession deletes the old session, creates a new one for the same user
// in the same transaction, and returns the new plaintext token. When the old
// session is already gone it creates nothing and returns an error.
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
		// The new session keeps the device, so a revoke still finds it.
		DeviceID: oldSession.DeviceID,
	}

	// Delete first and create only when the old row was still there: a
	// session that a revoke or a logout deleted after the middleware loaded
	// it must not come back as a new row.
	err := db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ?", oldSession.ID).Delete(&Session{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errSessionGone
		}
		return tx.Create(&newSession).Error
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
