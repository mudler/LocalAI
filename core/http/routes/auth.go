package routes

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services"
)

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

			providers = append(providers, "local") // always available
			if appConfig.Auth.GitHubClientID != "" {
				providers = append(providers, "github")
			}
			if appConfig.Auth.OIDCClientID != "" {
				providers = append(providers, "oidc")
			}
		}

		registrationMode := ""
		if authEnabled {
			registrationMode = appConfig.Auth.RegistrationMode
			if registrationMode == "" {
				registrationMode = "open"
			}
		}

		resp := map[string]interface{}{
			"authEnabled":      authEnabled,
			"providers":        providers,
			"hasUsers":         hasUsers,
			"registrationMode": registrationMode,
		}

		// Include current user if authenticated
		user := auth.GetUser(c)
		if user != nil {
			userResp := map[string]interface{}{
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

	// OAuth routes - only registered when auth is enabled
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
				e.GET("/api/auth/github/login", oauthMgr.LoginHandler("github"))
				e.GET("/api/auth/github/callback", oauthMgr.CallbackHandler(
					"github", db, appConfig.Auth.AdminEmail, appConfig.Auth.RegistrationMode,
				))
			}
			if appConfig.Auth.OIDCClientID != "" {
				e.GET("/api/auth/oidc/login", oauthMgr.LoginHandler("oidc"))
				e.GET("/api/auth/oidc/callback", oauthMgr.CallbackHandler(
					"oidc", db, appConfig.Auth.AdminEmail, appConfig.Auth.RegistrationMode,
				))
			}
		}
	}

	// POST /api/auth/register - public, email/password registration
	e.POST("/api/auth/register", func(c echo.Context) error {
		var body struct {
			Email      string `json:"email"`
			Password   string `json:"password"`
			Name       string `json:"name"`
			InviteCode string `json:"inviteCode"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		}

		body.Email = strings.TrimSpace(body.Email)
		body.Name = strings.TrimSpace(body.Name)
		body.InviteCode = strings.TrimSpace(body.InviteCode)

		if body.Email == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "email is required"})
		}
		if len(body.Password) < 8 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		}

		// Check for duplicate email with provider "local"
		var existing auth.User
		if err := db.Where("email = ? AND provider = ?", body.Email, "local").First(&existing).Error; err == nil {
			return c.JSON(http.StatusConflict, map[string]string{"error": "an account with this email already exists"})
		}

		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
		}

		role := auth.AssignRole(db, body.Email, appConfig.Auth.AdminEmail)

		// Determine status based on registration mode and invite code
		status := auth.StatusActive
		var validInvite *auth.InviteCode

		if auth.NeedsInviteOrApproval(db, body.Email, appConfig.Auth.AdminEmail, appConfig.Auth.RegistrationMode) {
			if appConfig.Auth.RegistrationMode == "invite" && body.InviteCode == "" {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "an invite code is required to register"})
			}

			if body.InviteCode != "" {
				invite, err := auth.ValidateInvite(db, body.InviteCode)
				if err != nil {
					return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid or expired invite code"})
				}
				validInvite = invite
				status = auth.StatusActive // valid invite = immediate activation
			} else {
				// approval mode without invite code
				status = auth.StatusPending
			}
		}

		name := body.Name
		if name == "" {
			name = body.Email
		}

		user := &auth.User{
			ID:           uuid.New().String(),
			Email:        body.Email,
			Name:         name,
			Provider:     "local",
			Subject:      body.Email,
			PasswordHash: hash,
			Role:         role,
			Status:       status,
		}
		if err := db.Create(user).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
		}

		if validInvite != nil {
			auth.ConsumeInvite(db, validInvite, user.ID)
		}

		if status == auth.StatusPending {
			return c.JSON(http.StatusOK, map[string]interface{}{
				"message": "registration successful, awaiting admin approval",
				"pending": true,
			})
		}

		sessionID, err := auth.CreateSession(db, user.ID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		}
		auth.SetSessionCookie(c, sessionID)

		return c.JSON(http.StatusCreated, map[string]interface{}{
			"user": map[string]interface{}{
				"id":    user.ID,
				"email": user.Email,
				"name":  user.Name,
				"role":  user.Role,
			},
		})
	})

	// POST /api/auth/login - public, email/password login
	e.POST("/api/auth/login", func(c echo.Context) error {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		}

		body.Email = strings.TrimSpace(body.Email)

		if body.Email == "" || body.Password == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "email and password are required"})
		}

		var user auth.User
		if err := db.Where("email = ? AND provider = ?", body.Email, "local").First(&user).Error; err != nil {
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

		sessionID, err := auth.CreateSession(db, user.ID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		}
		auth.SetSessionCookie(c, sessionID)

		return c.JSON(http.StatusOK, map[string]interface{}{
			"user": map[string]interface{}{
				"id":    user.ID,
				"email": user.Email,
				"name":  user.Name,
				"role":  user.Role,
			},
		})
	})

	// POST /api/auth/logout - requires auth
	e.POST("/api/auth/logout", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		// Delete session from cookie
		if cookie, err := c.Cookie("session"); err == nil && cookie.Value != "" {
			auth.DeleteSession(db, cookie.Value)
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

		return c.JSON(http.StatusOK, map[string]interface{}{
			"id":          user.ID,
			"email":       user.Email,
			"name":        user.Name,
			"avatarUrl":   user.AvatarURL,
			"role":        user.Role,
			"provider":    user.Provider,
			"permissions": auth.GetPermissionMapForUser(db, user),
		})
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

		updates := map[string]interface{}{
			"name":       name,
			"avatar_url": avatarURL,
		}

		if err := db.Model(&auth.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update profile"})
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"message":   "profile updated",
			"name":      name,
			"avatarUrl": avatarURL,
		})
	})

	// PUT /api/auth/password - change password (local users only)
	e.PUT("/api/auth/password", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		if user.Provider != "local" {
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

		return c.JSON(http.StatusOK, map[string]string{"message": "password updated"})
	})

	// POST /api/auth/api-keys - create API key
	e.POST("/api/auth/api-keys", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		var body struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&body); err != nil || body.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
		}

		plaintext, record, err := auth.CreateAPIKey(db, user.ID, body.Name, user.Role)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create API key"})
		}

		return c.JSON(http.StatusCreated, map[string]interface{}{
			"key":       plaintext, // shown once
			"id":        record.ID,
			"name":      record.Name,
			"keyPrefix": record.KeyPrefix,
			"role":      record.Role,
			"createdAt": record.CreatedAt,
		})
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

		result := make([]map[string]interface{}, 0, len(keys))
		for _, k := range keys {
			result = append(result, map[string]interface{}{
				"id":        k.ID,
				"name":      k.Name,
				"keyPrefix": k.KeyPrefix,
				"role":      k.Role,
				"createdAt": k.CreatedAt,
				"lastUsed":  k.LastUsed,
			})
		}

		return c.JSON(http.StatusOK, map[string]interface{}{"keys": result})
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
			names, err := services.ListModels(
				app.ModelConfigLoader(), app.ModelLoader(), nil, services.SKIP_IF_CONFIGURED,
			)
			if err == nil {
				modelNames = names
			}
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"agent_features": auth.AgentFeatureMetas(),
			"api_features":   auth.APIFeatureMetas(),
			"models":         modelNames,
		})
	}, adminMw)

	// GET /api/auth/admin/users - list all users
	e.GET("/api/auth/admin/users", func(c echo.Context) error {
		var users []auth.User
		if err := db.Order("created_at ASC").Find(&users).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list users"})
		}

		result := make([]map[string]interface{}, 0, len(users))
		for _, u := range users {
			entry := map[string]interface{}{
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
			result = append(result, entry)
		}

		return c.JSON(http.StatusOK, map[string]interface{}{"users": result})
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
		return c.JSON(http.StatusOK, map[string]interface{}{
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

		return c.JSON(http.StatusOK, map[string]interface{}{
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

		return c.JSON(http.StatusOK, map[string]interface{}{
			"message":        "model allowlist updated",
			"user_id":        targetID,
			"allowed_models": allowlist,
		})
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
		code := hex.EncodeToString(codeBytes)

		invite := &auth.InviteCode{
			ID:        uuid.New().String(),
			Code:      code,
			CreatedBy: admin.ID,
			ExpiresAt: time.Now().Add(time.Duration(body.ExpiresInHours) * time.Hour),
		}
		if err := db.Create(invite).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create invite"})
		}

		return c.JSON(http.StatusCreated, map[string]interface{}{
			"id":        invite.ID,
			"code":      invite.Code,
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

		result := make([]map[string]interface{}, 0, len(invites))
		for _, inv := range invites {
			entry := map[string]interface{}{
				"id":        inv.ID,
				"code":      inv.Code,
				"expiresAt": inv.ExpiresAt,
				"createdAt": inv.CreatedAt,
				"usedAt":    inv.UsedAt,
				"createdBy": map[string]interface{}{
					"id":   inv.Creator.ID,
					"name": inv.Creator.Name,
				},
			}
			if inv.UsedBy != nil && inv.Consumer != nil {
				entry["usedBy"] = map[string]interface{}{
					"id":   inv.Consumer.ID,
					"name": inv.Consumer.Name,
				}
			} else {
				entry["usedBy"] = nil
			}
			result = append(result, entry)
		}

		return c.JSON(http.StatusOK, map[string]interface{}{"invites": result})
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

	// GET /api/auth/invite/:code/check - public, validates invite code
	e.GET("/api/auth/invite/:code/check", func(c echo.Context) error {
		code := c.Param("code")
		_, err := auth.ValidateInvite(db, code)
		return c.JSON(http.StatusOK, map[string]interface{}{
			"valid": err == nil,
		})
	})
}
