package routes

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"gorm.io/gorm"
)

// rateLimiter implements a simple per-IP rate limiter for auth endpoints.
type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	window   time.Duration
	max      int
}

func newRateLimiter(window time.Duration, max int) *rateLimiter {
	return &rateLimiter{
		attempts: make(map[string][]time.Time),
		window:   window,
		max:      max,
	}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Prune old entries
	recent := rl.attempts[key][:0]
	for _, t := range rl.attempts[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= rl.max {
		rl.attempts[key] = recent
		return false
	}

	rl.attempts[key] = append(recent, now)
	return true
}

// cleanup removes stale IP entries that have no recent attempts.
func (rl *rateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-rl.window)
	for ip, attempts := range rl.attempts {
		recent := attempts[:0]
		for _, t := range attempts {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(rl.attempts, ip)
		} else {
			rl.attempts[ip] = recent
		}
	}
}

func rateLimitMiddleware(rl *rateLimiter) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !rl.allow(c.RealIP()) {
				return c.JSON(http.StatusTooManyRequests, map[string]string{
					"error": "too many requests, please try again later",
				})
			}
			return next(c)
		}
	}
}

// parseDuration parses a duration string like "30d", "90d", "1y", "24h".
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		s = strings.TrimSuffix(s, "d")
		var days int
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, &time.ParseError{}
			}
			days = days*10 + int(c-'0')
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "y") {
		s = strings.TrimSuffix(s, "y")
		var years int
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, &time.ParseError{}
			}
			years = years*10 + int(c-'0')
		}
		return time.Duration(years) * 365 * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// RegisterAuthRoutes registers authentication-related API routes.
func RegisterAuthRoutes(e *echo.Echo, app *application.Application) {
	appConfig := app.ApplicationConfig()
	db := app.AuthDB()

	// GET /api/auth/status - public, returns auth state
	e.GET("/api/auth/status", func(c echo.Context) error {
		authEnabled := db != nil
		providers := []string{}
		hasUsers := false

		if authEnabled {
			var count int64
			db.Model(&auth.User{}).Count(&count)
			hasUsers = count > 0

			if !appConfig.Auth.DisableLocalAuth {
				providers = append(providers, auth.ProviderLocal)
			}
			if appConfig.Auth.GitHubClientID != "" {
				providers = append(providers, auth.ProviderGitHub)
			}
			if appConfig.Auth.OIDCClientID != "" {
				providers = append(providers, auth.ProviderOIDC)
			}
		}

		registrationMode := ""
		if authEnabled {
			registrationMode = appConfig.Auth.RegistrationMode
			if registrationMode == "" {
				registrationMode = "approval"
			}
		}

		resp := map[string]any{
			"authEnabled":          authEnabled,
			"staticApiKeyRequired": !authEnabled && len(appConfig.ApiKeys) > 0,
			"providers":            providers,
			"hasUsers":             hasUsers,
			"registrationMode":     registrationMode,
		}

		// Include current user if authenticated
		user := auth.GetUser(c)
		if user != nil {
			userResp := map[string]any{
				"id":        user.ID,
				"email":     user.Email,
				"name":      user.Name,
				"avatarUrl": user.AvatarURL,
				"role":      user.Role,
				"provider":  user.Provider,
			}
			if authEnabled {
				userResp["permissions"] = auth.GetPermissionMapForUser(db, user)
			}
			resp["user"] = userResp
		} else {
			resp["user"] = nil
		}

		return c.JSON(http.StatusOK, resp)
	})

	// Rate limiter for auth endpoints: 5 attempts per minute per IP
	authRL := newRateLimiter(1*time.Minute, 5)
	authRateLimitMw := rateLimitMiddleware(authRL)

	// Start background goroutine to periodically prune stale IP entries
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-appConfig.Context.Done():
				return
			case <-ticker.C:
				authRL.cleanup()
			}
		}
	}()

	// POST /api/auth/token-login - authenticate with API key/token.
	// Registered when auth DB or legacy API keys are configured.
	if db != nil || len(appConfig.ApiKeys) > 0 {
		e.POST("/api/auth/token-login", func(c echo.Context) error {
			var body struct {
				Token string `json:"token"`
			}
			if err := c.Bind(&body); err != nil || strings.TrimSpace(body.Token) == "" {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "token is required"})
			}

			token := strings.TrimSpace(body.Token)

			// Try as user API key (only when auth DB is available)
			if db != nil {
				if apiKey, err := auth.ValidateAPIKey(db, token, appConfig.Auth.APIKeyHMACSecret); err == nil {
					sessionID, err := auth.CreateSession(db, apiKey.User.ID, appConfig.Auth.APIKeyHMACSecret)
					if err != nil {
						return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
					}
					auth.SetSessionCookie(c, sessionID)
					return c.JSON(http.StatusOK, map[string]any{
						"user": map[string]any{
							"id":    apiKey.User.ID,
							"email": apiKey.User.Email,
							"name":  apiKey.User.Name,
							"role":  apiKey.User.Role,
						},
					})
				}
			}

			// Try as legacy API key
			if len(appConfig.ApiKeys) > 0 && isValidLegacyKey(token, appConfig) {
				auth.SetTokenCookie(c, token)
				return c.JSON(http.StatusOK, map[string]any{
					"user": map[string]any{
						"id":   "legacy-api-key",
						"name": "API Key User",
						"role": auth.RoleAdmin,
					},
				})
			}

			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid token"})
		}, authRateLimitMw)
	}

	// Remaining routes require auth DB
	if db == nil {
		return
	}

	// Set up OAuth manager when any OAuth/OIDC provider is configured
	if appConfig.Auth.GitHubClientID != "" || appConfig.Auth.OIDCClientID != "" {
		oauthMgr, err := auth.NewOAuthManager(
			appConfig.Auth.BaseURL,
			auth.OAuthParams{
				GitHubClientID:     appConfig.Auth.GitHubClientID,
				GitHubClientSecret: appConfig.Auth.GitHubClientSecret,
				OIDCIssuer:         appConfig.Auth.OIDCIssuer,
				OIDCClientID:       appConfig.Auth.OIDCClientID,
				OIDCClientSecret:   appConfig.Auth.OIDCClientSecret,
			},
		)
		if err == nil {
			if appConfig.Auth.GitHubClientID != "" {
				e.GET("/api/auth/github/login", oauthMgr.LoginHandler(auth.ProviderGitHub))
				e.GET("/api/auth/github/callback", oauthMgr.CallbackHandler(
					auth.ProviderGitHub, db, appConfig.Auth.AdminEmail, appConfig.Auth.RegistrationMode, appConfig.Auth.APIKeyHMACSecret,
				))
			}
			if appConfig.Auth.OIDCClientID != "" {
				e.GET("/api/auth/oidc/login", oauthMgr.LoginHandler(auth.ProviderOIDC))
				e.GET("/api/auth/oidc/callback", oauthMgr.CallbackHandler(
					auth.ProviderOIDC, db, appConfig.Auth.AdminEmail, appConfig.Auth.RegistrationMode, appConfig.Auth.APIKeyHMACSecret,
				))
			}
		}
	}

	// POST /api/auth/register - public, email/password registration
	e.POST("/api/auth/register", func(c echo.Context) error {
		if appConfig.Auth.DisableLocalAuth {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "local registration is disabled"})
		}

		var body struct {
			Email      string `json:"email"`
			Password   string `json:"password"`
			Name       string `json:"name"`
			InviteCode string `json:"inviteCode"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		}

		body.Email = strings.ToLower(strings.TrimSpace(body.Email))
		body.Name = strings.TrimSpace(body.Name)
		body.InviteCode = strings.TrimSpace(body.InviteCode)

		if body.Email == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "email is required"})
		}
		if _, err := mail.ParseAddress(body.Email); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid email address"})
		}
		if len(body.Password) < 8 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		}

		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
		}

		name := body.Name
		if name == "" {
			name = body.Email
		}

		// Wrap user creation in a transaction to prevent admin bootstrap race (#2)
		var user *auth.User
		var validInvite *auth.InviteCode
		var status string

		txErr := db.Transaction(func(tx *gorm.DB) error {
			// Check for duplicate email with local provider (#6: return generic response)
			var existing auth.User
			if err := tx.Where("email = ? AND provider = ?", body.Email, auth.ProviderLocal).First(&existing).Error; err == nil {
				// Account exists — return nil to signal generic success
				user = nil
				return nil
			}

			role := auth.AssignRole(tx, body.Email, appConfig.Auth.AdminEmail)

			// Determine status based on registration mode and invite code
			status = auth.StatusActive

			if auth.NeedsInviteOrApproval(tx, body.Email, appConfig.Auth.AdminEmail, appConfig.Auth.RegistrationMode) {
				if appConfig.Auth.RegistrationMode == "invite" && body.InviteCode == "" {
					return fmt.Errorf("invite_required")
				}

				if body.InviteCode != "" {
					invite, err := auth.ValidateInvite(tx, body.InviteCode, appConfig.Auth.APIKeyHMACSecret)
					if err != nil {
						return fmt.Errorf("invalid_invite")
					}
					validInvite = invite
					status = auth.StatusActive
				} else {
					status = auth.StatusPending
				}
			}

			user = &auth.User{
				ID:           uuid.New().String(),
				Email:        body.Email,
				Name:         name,
				Provider:     auth.ProviderLocal,
				Subject:      body.Email,
				PasswordHash: hash,
				Role:         role,
				Status:       status,
			}
			if err := tx.Create(user).Error; err != nil {
				return fmt.Errorf("failed to create user: %w", err)
			}

			if validInvite != nil {
				auth.ConsumeInvite(tx, validInvite, user.ID)
			}

			return nil
		})

		if txErr != nil {
			msg := txErr.Error()
			if msg == "invite_required" {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "an invite code is required to register"})
			}
			if msg == "invalid_invite" {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid or expired invite code"})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
		}

		// user == nil means duplicate email — return generic success (#6)
		if user == nil {
			return c.JSON(http.StatusCreated, map[string]any{
				"message": "registration processed",
			})
		}

		if status == auth.StatusPending {
			return c.JSON(http.StatusOK, map[string]any{
				"message": "registration successful, awaiting admin approval",
				"pending": true,
			})
		}

		sessionID, err := auth.CreateSession(db, user.ID, appConfig.Auth.APIKeyHMACSecret)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		}
		auth.SetSessionCookie(c, sessionID)

		return c.JSON(http.StatusCreated, map[string]any{
			"user": map[string]any{
				"id":    user.ID,
				"email": user.Email,
				"name":  user.Name,
				"role":  user.Role,
			},
		})
	}, authRateLimitMw)

	// POST /api/auth/login - public, email/password login
	e.POST("/api/auth/login", func(c echo.Context) error {
		if appConfig.Auth.DisableLocalAuth {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "local login is disabled, please use OAuth"})
		}

		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		}

		body.Email = strings.ToLower(strings.TrimSpace(body.Email))

		if body.Email == "" || body.Password == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "email and password are required"})
		}

		var user auth.User
		if err := db.Where("email = ? AND provider = ?", body.Email, auth.ProviderLocal).First(&user).Error; err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
		}

		if !auth.CheckPassword(user.PasswordHash, body.Password) {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
		}

		if user.Status == auth.StatusPending {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "account pending admin approval"})
		}

		// Maybe promote on login
		auth.MaybePromote(db, &user, appConfig.Auth.AdminEmail)

		sessionID, err := auth.CreateSession(db, user.ID, appConfig.Auth.APIKeyHMACSecret)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		}
		auth.SetSessionCookie(c, sessionID)

		return c.JSON(http.StatusOK, map[string]any{
			"user": map[string]any{
				"id":    user.ID,
				"email": user.Email,
				"name":  user.Name,
				"role":  user.Role,
			},
		})
	}, authRateLimitMw)

	// POST /api/auth/logout - requires auth
	e.POST("/api/auth/logout", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		// Delete session from cookie
		if cookie, err := c.Cookie("session"); err == nil && cookie.Value != "" {
			auth.DeleteSession(db, cookie.Value, appConfig.Auth.APIKeyHMACSecret)
		}
		auth.ClearSessionCookie(c)

		return c.JSON(http.StatusOK, map[string]string{"message": "logged out"})
	})

	// GET /api/auth/me - requires auth
	e.GET("/api/auth/me", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		resp := map[string]any{
			"id":          user.ID,
			"email":       user.Email,
			"name":        user.Name,
			"avatarUrl":   user.AvatarURL,
			"role":        user.Role,
			"provider":    user.Provider,
			"permissions": auth.GetPermissionMapForUser(db, user),
		}
		if quotas, err := auth.GetQuotaStatuses(db, user.ID); err == nil {
			resp["quotas"] = quotas
		}
		return c.JSON(http.StatusOK, resp)
	})

	// GET /api/auth/quota - view own quota status
	e.GET("/api/auth/quota", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}
		quotas, err := auth.GetQuotaStatuses(db, user.ID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to get quota status"})
		}
		return c.JSON(http.StatusOK, map[string]any{"quotas": quotas})
	})

	// PUT /api/auth/profile - update user profile (name, avatar_url)
	e.PUT("/api/auth/profile", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		var body struct {
			Name      string `json:"name"`
			AvatarURL string `json:"avatar_url"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		}

		name := strings.TrimSpace(body.Name)
		if name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
		}

		avatarURL := strings.TrimSpace(body.AvatarURL)
		if len(avatarURL) > 512 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "avatar URL must be at most 512 characters"})
		}

		updates := map[string]any{
			"name":       name,
			"avatar_url": avatarURL,
		}

		if err := db.Model(&auth.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update profile"})
		}

		return c.JSON(http.StatusOK, map[string]any{
			"message":   "profile updated",
			"name":      name,
			"avatarUrl": avatarURL,
		})
	})

	// PUT /api/auth/password - change password (local users only) (#4: add rate limiting)
	e.PUT("/api/auth/password", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		if user.Provider != auth.ProviderLocal {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "password change is only available for local accounts"})
		}

		var body struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		}

		if body.CurrentPassword == "" || body.NewPassword == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "current and new passwords are required"})
		}

		if len(body.NewPassword) < 8 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "new password must be at least 8 characters"})
		}

		// Verify current password
		if !auth.CheckPassword(user.PasswordHash, body.CurrentPassword) {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "current password is incorrect"})
		}

		hash, err := auth.HashPassword(body.NewPassword)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
		}

		if err := db.Model(&auth.User{}).Where("id = ?", user.ID).Update("password_hash", hash).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update password"})
		}

		// Invalidate all existing sessions for this user
		auth.DeleteUserSessions(db, user.ID)

		// Create a fresh session for the current request
		newSessionID, err := auth.CreateSession(db, user.ID, appConfig.Auth.APIKeyHMACSecret)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		}
		auth.SetSessionCookie(c, newSessionID)

		return c.JSON(http.StatusOK, map[string]string{"message": "password updated"})
	}, authRateLimitMw)

	// DELETE /api/auth/sessions - revoke all sessions for the current user
	e.DELETE("/api/auth/sessions", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		// Delete all sessions
		auth.DeleteUserSessions(db, user.ID)

		// Create a fresh session for the current request
		newSessionID, err := auth.CreateSession(db, user.ID, appConfig.Auth.APIKeyHMACSecret)
		if err != nil {
			auth.ClearSessionCookie(c)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		}
		auth.SetSessionCookie(c, newSessionID)

		return c.JSON(http.StatusOK, map[string]string{"message": "all other sessions revoked"})
	})

	// POST /api/auth/api-keys - create API key (#8: expiration support)
	e.POST("/api/auth/api-keys", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		var body struct {
			Name      string `json:"name"`
			ExpiresIn string `json:"expiresIn"` // duration like "30d", "90d", "1y"
			ExpiresAt string `json:"expiresAt"` // ISO timestamp
		}
		if err := c.Bind(&body); err != nil || body.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
		}

		// Determine expiration
		var expiresAt *time.Time
		if body.ExpiresAt != "" {
			t, err := time.Parse(time.RFC3339, body.ExpiresAt)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid expiresAt format, use RFC3339"})
			}
			expiresAt = &t
		} else if body.ExpiresIn != "" {
			dur, err := parseDuration(body.ExpiresIn)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid expiresIn format"})
			}
			t := time.Now().Add(dur)
			expiresAt = &t
		} else if appConfig.Auth.DefaultAPIKeyExpiry != "" {
			dur, err := parseDuration(appConfig.Auth.DefaultAPIKeyExpiry)
			if err == nil {
				t := time.Now().Add(dur)
				expiresAt = &t
			}
		}

		plaintext, record, err := auth.CreateAPIKey(db, user.ID, body.Name, user.Role, appConfig.Auth.APIKeyHMACSecret, expiresAt)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create API key"})
		}

		resp := map[string]any{
			"key":       plaintext, // shown once
			"id":        record.ID,
			"name":      record.Name,
			"keyPrefix": record.KeyPrefix,
			"role":      record.Role,
			"createdAt": record.CreatedAt,
		}
		if record.ExpiresAt != nil {
			resp["expiresAt"] = record.ExpiresAt
		}

		return c.JSON(http.StatusCreated, resp)
	})

	// GET /api/auth/api-keys - list user's API keys
	e.GET("/api/auth/api-keys", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		keys, err := auth.ListAPIKeys(db, user.ID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list API keys"})
		}

		result := make([]map[string]any, 0, len(keys))
		for _, k := range keys {
			entry := map[string]any{
				"id":        k.ID,
				"name":      k.Name,
				"keyPrefix": k.KeyPrefix,
				"role":      k.Role,
				"createdAt": k.CreatedAt,
				"lastUsed":  k.LastUsed,
			}
			if k.ExpiresAt != nil {
				entry["expiresAt"] = k.ExpiresAt
			}
			result = append(result, entry)
		}

		return c.JSON(http.StatusOK, map[string]any{"keys": result})
	})

	// DELETE /api/auth/api-keys/:id - revoke API key
	e.DELETE("/api/auth/api-keys/:id", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		keyID := c.Param("id")
		if err := auth.RevokeAPIKey(db, keyID, user.ID); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "API key not found"})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "API key revoked"})
	})

	// Usage endpoints
	// GET /api/auth/usage - user's own usage
	e.GET("/api/auth/usage", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		period := c.QueryParam("period")
		if period == "" {
			period = "month"
		}

		buckets, err := auth.GetUserUsage(db, user.ID, period)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to get usage"})
		}

		totals := auth.UsageTotals{}
		for _, b := range buckets {
			totals.PromptTokens += b.PromptTokens
			totals.CompletionTokens += b.CompletionTokens
			totals.TotalTokens += b.TotalTokens
			totals.RequestCount += b.RequestCount
		}

		return c.JSON(http.StatusOK, map[string]any{
			"usage":  buckets,
			"totals": totals,
		})
	})

	// Admin endpoints
	adminMw := auth.RequireAdmin()

	// GET /api/auth/admin/features - returns feature metadata and available models
	e.GET("/api/auth/admin/features", func(c echo.Context) error {
		// Get available models
		modelNames := []string{}
		if app.ModelConfigLoader() != nil && app.ModelLoader() != nil {
			names, err := galleryop.ListModels(
				app.ModelConfigLoader(), app.ModelLoader(), nil, galleryop.SKIP_IF_CONFIGURED,
			)
			if err == nil {
				modelNames = names
			}
		}

		return c.JSON(http.StatusOK, map[string]any{
			"agent_features":   auth.AgentFeatureMetas(),
			"general_features": auth.GeneralFeatureMetas(),
			"api_features":     auth.APIFeatureMetas(),
			"models":           modelNames,
		})
	}, adminMw)

	// GET /api/auth/admin/users - list all users
	e.GET("/api/auth/admin/users", func(c echo.Context) error {
		var users []auth.User
		if err := db.Order("created_at ASC").Find(&users).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list users"})
		}

		result := make([]map[string]any, 0, len(users))
		for _, u := range users {
			entry := map[string]any{
				"id":        u.ID,
				"email":     u.Email,
				"name":      u.Name,
				"avatarUrl": u.AvatarURL,
				"role":      u.Role,
				"status":    u.Status,
				"provider":  u.Provider,
				"createdAt": u.CreatedAt,
			}
			entry["permissions"] = auth.GetPermissionMapForUser(db, &u)
			entry["allowed_models"] = auth.GetModelAllowlist(db, u.ID)
			if quotas, err := auth.GetQuotaStatuses(db, u.ID); err == nil && len(quotas) > 0 {
				entry["quotas"] = quotas
			}
			result = append(result, entry)
		}

		return c.JSON(http.StatusOK, map[string]any{"users": result})
	}, adminMw)

	// PUT /api/auth/admin/users/:id/role - change user role
	e.PUT("/api/auth/admin/users/:id/role", func(c echo.Context) error {
		currentUser := auth.GetUser(c)
		targetID := c.Param("id")

		if currentUser.ID == targetID {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot change your own role"})
		}

		var body struct {
			Role string `json:"role"`
		}
		if err := c.Bind(&body); err != nil || (body.Role != auth.RoleAdmin && body.Role != auth.RoleUser) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "role must be 'admin' or 'user'"})
		}

		result := db.Model(&auth.User{}).Where("id = ?", targetID).Update("role", body.Role)
		if result.RowsAffected == 0 {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "role updated"})
	}, adminMw)

	// PUT /api/auth/admin/users/:id/status - change user status (approve/disable)
	e.PUT("/api/auth/admin/users/:id/status", func(c echo.Context) error {
		currentUser := auth.GetUser(c)
		targetID := c.Param("id")

		if currentUser.ID == targetID {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot change your own status"})
		}

		var body struct {
			Status string `json:"status"`
		}
		if err := c.Bind(&body); err != nil || (body.Status != auth.StatusActive && body.Status != auth.StatusDisabled) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "status must be 'active' or 'disabled'"})
		}

		result := db.Model(&auth.User{}).Where("id = ?", targetID).Update("status", body.Status)
		if result.RowsAffected == 0 {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "status updated"})
	}, adminMw)

	// PUT /api/auth/admin/users/:id/password - admin reset user password
	e.PUT("/api/auth/admin/users/:id/password", func(c echo.Context) error {
		currentUser := auth.GetUser(c)
		targetID := c.Param("id")

		if currentUser.ID == targetID {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot reset your own password via this endpoint, use self-service password change"})
		}

		var target auth.User
		if err := db.First(&target, "id = ?", targetID).Error; err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}

		if target.Provider != auth.ProviderLocal {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "password reset is only available for local accounts"})
		}

		var body struct {
			Password string `json:"password"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		}

		if len(body.Password) < 8 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		}

		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
		}

		if err := db.Model(&auth.User{}).Where("id = ?", targetID).Update("password_hash", hash).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update password"})
		}

		auth.DeleteUserSessions(db, targetID)

		return c.JSON(http.StatusOK, map[string]string{"message": "password reset successfully"})
	}, adminMw)

	// DELETE /api/auth/admin/users/:id - delete user
	e.DELETE("/api/auth/admin/users/:id", func(c echo.Context) error {
		currentUser := auth.GetUser(c)
		targetID := c.Param("id")

		if currentUser.ID == targetID {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot delete yourself"})
		}

		// Cascade: delete sessions and API keys
		db.Where("user_id = ?", targetID).Delete(&auth.Session{})
		db.Where("user_id = ?", targetID).Delete(&auth.UserAPIKey{})

		result := db.Where("id = ?", targetID).Delete(&auth.User{})
		if result.RowsAffected == 0 {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "user deleted"})
	}, adminMw)

	// GET /api/auth/admin/users/:id/permissions - get user permissions
	e.GET("/api/auth/admin/users/:id/permissions", func(c echo.Context) error {
		targetID := c.Param("id")
		var target auth.User
		if err := db.First(&target, "id = ?", targetID).Error; err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}
		perms := auth.GetPermissionMapForUser(db, &target)
		return c.JSON(http.StatusOK, map[string]any{
			"user_id":     targetID,
			"permissions": perms,
		})
	}, adminMw)

	// PUT /api/auth/admin/users/:id/permissions - update user permissions
	e.PUT("/api/auth/admin/users/:id/permissions", func(c echo.Context) error {
		targetID := c.Param("id")
		var target auth.User
		if err := db.First(&target, "id = ?", targetID).Error; err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}

		var perms auth.PermissionMap
		if err := c.Bind(&perms); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}

		if err := auth.UpdateUserPermissions(db, targetID, perms); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update permissions"})
		}

		return c.JSON(http.StatusOK, map[string]any{
			"message":     "permissions updated",
			"user_id":     targetID,
			"permissions": perms,
		})
	}, adminMw)

	// PUT /api/auth/admin/users/:id/models - update user model allowlist
	e.PUT("/api/auth/admin/users/:id/models", func(c echo.Context) error {
		targetID := c.Param("id")
		var target auth.User
		if err := db.First(&target, "id = ?", targetID).Error; err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}

		var allowlist auth.ModelAllowlist
		if err := c.Bind(&allowlist); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}

		if err := auth.UpdateModelAllowlist(db, targetID, allowlist); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update model allowlist"})
		}

		return c.JSON(http.StatusOK, map[string]any{
			"message":        "model allowlist updated",
			"user_id":        targetID,
			"allowed_models": allowlist,
		})
	}, adminMw)

	// GET /api/auth/admin/users/:id/quotas - list user's quota rules
	e.GET("/api/auth/admin/users/:id/quotas", func(c echo.Context) error {
		targetID := c.Param("id")
		var target auth.User
		if err := db.First(&target, "id = ?", targetID).Error; err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}
		quotas, err := auth.GetQuotaStatuses(db, targetID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to get quotas"})
		}
		return c.JSON(http.StatusOK, map[string]any{"quotas": quotas})
	}, adminMw)

	// PUT /api/auth/admin/users/:id/quotas - upsert quota rule (by user+model)
	e.PUT("/api/auth/admin/users/:id/quotas", func(c echo.Context) error {
		targetID := c.Param("id")
		var target auth.User
		if err := db.First(&target, "id = ?", targetID).Error; err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}

		var body struct {
			Model          string `json:"model"`
			MaxRequests    *int64 `json:"max_requests"`
			MaxTotalTokens *int64 `json:"max_total_tokens"`
			Window         string `json:"window"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
		if body.Window == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "window is required"})
		}

		windowSecs, err := auth.ParseWindowDuration(body.Window)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		rule, err := auth.CreateOrUpdateQuotaRule(db, targetID, body.Model, body.MaxRequests, body.MaxTotalTokens, windowSecs)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save quota rule"})
		}

		return c.JSON(http.StatusOK, map[string]any{
			"message": "quota rule saved",
			"quota":   rule,
		})
	}, adminMw)

	// DELETE /api/auth/admin/users/:id/quotas/:quota_id - delete a quota rule
	e.DELETE("/api/auth/admin/users/:id/quotas/:quota_id", func(c echo.Context) error {
		targetID := c.Param("id")
		quotaID := c.Param("quota_id")
		if err := auth.DeleteQuotaRule(db, quotaID, targetID); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "quota rule not found"})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "quota rule deleted"})
	}, adminMw)

	// GET /api/auth/admin/usage - all users' usage (admin only)
	e.GET("/api/auth/admin/usage", func(c echo.Context) error {
		period := c.QueryParam("period")
		if period == "" {
			period = "month"
		}
		userID := c.QueryParam("user_id")

		buckets, err := auth.GetAllUsage(db, period, userID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to get usage"})
		}

		totals := auth.UsageTotals{}
		for _, b := range buckets {
			totals.PromptTokens += b.PromptTokens
			totals.CompletionTokens += b.CompletionTokens
			totals.TotalTokens += b.TotalTokens
			totals.RequestCount += b.RequestCount
		}

		return c.JSON(http.StatusOK, map[string]any{
			"usage":  buckets,
			"totals": totals,
		})
	}, adminMw)

	// --- Invite management endpoints ---

	// POST /api/auth/admin/invites - create invite (admin only)
	e.POST("/api/auth/admin/invites", func(c echo.Context) error {
		admin := auth.GetUser(c)
		if admin == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		var body struct {
			ExpiresInHours int `json:"expiresInHours"`
		}
		_ = c.Bind(&body)
		if body.ExpiresInHours <= 0 {
			body.ExpiresInHours = 168 // 7 days default
		}

		codeBytes := make([]byte, 32)
		if _, err := rand.Read(codeBytes); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to generate invite code"})
		}
		plaintext := hex.EncodeToString(codeBytes)
		codeHash := auth.HashAPIKey(plaintext, appConfig.Auth.APIKeyHMACSecret)

		invite := &auth.InviteCode{
			ID:         uuid.New().String(),
			Code:       codeHash,
			CodePrefix: plaintext[:8],
			CreatedBy:  admin.ID,
			ExpiresAt:  time.Now().Add(time.Duration(body.ExpiresInHours) * time.Hour),
		}
		if err := db.Create(invite).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create invite"})
		}

		return c.JSON(http.StatusCreated, map[string]any{
			"id":        invite.ID,
			"code":      plaintext,
			"expiresAt": invite.ExpiresAt,
			"createdAt": invite.CreatedAt,
		})
	}, adminMw)

	// GET /api/auth/admin/invites - list all invites (admin only)
	e.GET("/api/auth/admin/invites", func(c echo.Context) error {
		var invites []auth.InviteCode
		if err := db.Preload("Creator").Preload("Consumer").Order("created_at DESC").Find(&invites).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list invites"})
		}

		result := make([]map[string]any, 0, len(invites))
		for _, inv := range invites {
			entry := map[string]any{
				"id":         inv.ID,
				"codePrefix": inv.CodePrefix,
				"expiresAt":  inv.ExpiresAt,
				"createdAt":  inv.CreatedAt,
				"usedAt":     inv.UsedAt,
				"createdBy": map[string]any{
					"id":   inv.Creator.ID,
					"name": inv.Creator.Name,
				},
			}
			if inv.UsedBy != nil && inv.Consumer != nil {
				entry["usedBy"] = map[string]any{
					"id":   inv.Consumer.ID,
					"name": inv.Consumer.Name,
				}
			} else {
				entry["usedBy"] = nil
			}
			result = append(result, entry)
		}

		return c.JSON(http.StatusOK, map[string]any{"invites": result})
	}, adminMw)

	// DELETE /api/auth/admin/invites/:id - revoke unused invite (admin only)
	e.DELETE("/api/auth/admin/invites/:id", func(c echo.Context) error {
		inviteID := c.Param("id")

		var invite auth.InviteCode
		if err := db.First(&invite, "id = ?", inviteID).Error; err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "invite not found"})
		}
		if invite.UsedBy != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot revoke a used invite"})
		}

		db.Delete(&invite)
		return c.JSON(http.StatusOK, map[string]string{"message": "invite revoked"})
	}, adminMw)

	// Note: GET /api/auth/invite/:code/check endpoint removed (#5) —
	// invite codes are validated only during registration.
}

// isValidLegacyKey checks if the key matches any configured API key
// using constant-time comparison.
func isValidLegacyKey(token string, appConfig *config.ApplicationConfig) bool {
	for _, validKey := range appConfig.ApiKeys {
		if subtle.ConstantTimeCompare([]byte(token), []byte(validKey)) == 1 {
			return true
		}
	}
	return false
}
