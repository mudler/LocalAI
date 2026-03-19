package auth

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"gorm.io/gorm"
)

const (
	contextKeyUser = "auth_user"
	contextKeyRole = "auth_role"
)

// Middleware returns an Echo middleware that handles authentication.
//
// Resolution order:
//  1. If auth not enabled AND no legacy API keys → pass through
//  2. Skip auth for exempt paths (PathWithoutAuth + /api/auth/)
//  3. If auth enabled (db != nil):
//     a. Try "session" cookie → DB lookup
//     b. Try Authorization: Bearer → session ID, then user API key
//     c. Try x-api-key / xi-api-key → user API key
//     d. Try "token" cookie → legacy API key check
//     e. Check all extracted keys against legacy ApiKeys → synthetic admin
//  4. If auth not enabled → delegate to legacy API key validation
//  5. If no auth found for /api/ or /v1/ paths → 401
//  6. Otherwise pass through (static assets, UI pages, etc.)
func Middleware(db *gorm.DB, appConfig *config.ApplicationConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			authEnabled := db != nil
			hasLegacyKeys := len(appConfig.ApiKeys) > 0

			// 1. No auth at all
			if !authEnabled && !hasLegacyKeys {
				return next(c)
			}

			path := c.Request().URL.Path
			exempt := isExemptPath(path, appConfig)
			authenticated := false

			// 2. Try to authenticate (populates user in context if possible)
			if authEnabled {
				user := tryAuthenticate(c, db, appConfig)
				if user != nil {
					c.Set(contextKeyUser, user)
					c.Set(contextKeyRole, user.Role)
					authenticated = true

					// Session rotation for cookie-based sessions
					if session, ok := c.Get("_auth_session").(*Session); ok {
						MaybeRotateSession(c, db, session, appConfig.Auth.APIKeyHMACSecret)
					}
				}
			}

			// 3. Legacy API key validation (works whether auth is enabled or not)
			if !authenticated && hasLegacyKeys {
				key := extractKey(c)
				if key != "" && isValidLegacyKey(key, appConfig) {
					syntheticUser := &User{
						ID:   "legacy-api-key",
						Name: "API Key User",
						Role: RoleAdmin,
					}
					c.Set(contextKeyUser, syntheticUser)
					c.Set(contextKeyRole, RoleAdmin)
					authenticated = true
				}
			}

			// 4. If authenticated or exempt path, proceed
			if authenticated || exempt {
				return next(c)
			}

			// 5. Require auth for API paths
			if isAPIPath(path) {
				return authError(c, appConfig)
			}

			// 6. Pass through for non-API paths when auth is DB-based
			// (the React UI will redirect to login as needed)
			if authEnabled && !hasLegacyKeys {
				return next(c)
			}

			// 7. Legacy behavior: if API keys are set, all paths require auth
			if hasLegacyKeys {
				// Check GET exemptions
				if appConfig.DisableApiKeyRequirementForHttpGet && c.Request().Method == http.MethodGet {
					for _, rx := range appConfig.HttpGetExemptedEndpoints {
						if rx.MatchString(c.Path()) {
							return next(c)
						}
					}
				}
				return authError(c, appConfig)
			}

			return next(c)
		}
	}
}

// RequireAdmin returns middleware that checks the user has admin role.
func RequireAdmin() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			user := GetUser(c)
			if user == nil {
				return c.JSON(http.StatusUnauthorized, schema.ErrorResponse{
					Error: &schema.APIError{
						Message: "Authentication required",
						Code:    http.StatusUnauthorized,
						Type:    "authentication_error",
					},
				})
			}
			if user.Role != RoleAdmin {
				return c.JSON(http.StatusForbidden, schema.ErrorResponse{
					Error: &schema.APIError{
						Message: "Admin access required",
						Code:    http.StatusForbidden,
						Type:    "authorization_error",
					},
				})
			}
			return next(c)
		}
	}
}

// NoopMiddleware returns a middleware that does nothing (pass-through).
// Used when auth is disabled to satisfy route registration that expects
// an admin middleware parameter.
func NoopMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return next
	}
}

// RequireFeature returns middleware that checks the user has access to the given feature.
// If no auth DB is provided, it passes through (backward compat).
// Admins always pass. Regular users must have the feature enabled in their permissions.
func RequireFeature(db *gorm.DB, feature string) echo.MiddlewareFunc {
	if db == nil {
		return NoopMiddleware()
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			user := GetUser(c)
			if user == nil {
				return c.JSON(http.StatusUnauthorized, schema.ErrorResponse{
					Error: &schema.APIError{
						Message: "Authentication required",
						Code:    http.StatusUnauthorized,
						Type:    "authentication_error",
					},
				})
			}
			if user.Role == RoleAdmin {
				return next(c)
			}
			perm, err := GetCachedUserPermissions(c, db, user.ID)
			if err != nil {
				return c.JSON(http.StatusForbidden, schema.ErrorResponse{
					Error: &schema.APIError{
						Message: "feature not enabled for your account",
						Code:    http.StatusForbidden,
						Type:    "authorization_error",
					},
				})
			}
			val, exists := perm.Permissions[feature]
			if !exists {
				if !isDefaultOnFeature(feature) {
					return c.JSON(http.StatusForbidden, schema.ErrorResponse{
						Error: &schema.APIError{
							Message: "feature not enabled for your account",
							Code:    http.StatusForbidden,
							Type:    "authorization_error",
						},
					})
				}
			} else if !val {
				return c.JSON(http.StatusForbidden, schema.ErrorResponse{
					Error: &schema.APIError{
						Message: "feature not enabled for your account",
						Code:    http.StatusForbidden,
						Type:    "authorization_error",
					},
				})
			}
			return next(c)
		}
	}
}

// GetUser returns the authenticated user from the echo context, or nil.
func GetUser(c echo.Context) *User {
	u, ok := c.Get(contextKeyUser).(*User)
	if !ok {
		return nil
	}
	return u
}

// GetUserRole returns the role of the authenticated user, or empty string.
func GetUserRole(c echo.Context) string {
	role, _ := c.Get(contextKeyRole).(string)
	return role
}

// RequireRouteFeature returns a global middleware that checks the user has access
// to the feature required by the matched route. It uses the RouteFeatureRegistry
// to look up the required feature for each route pattern + HTTP method.
// If no entry matches, the request passes through (no restriction).
func RequireRouteFeature(db *gorm.DB) echo.MiddlewareFunc {
	if db == nil {
		return NoopMiddleware()
	}
	// Pre-build lookup map: "METHOD:pattern" -> feature
	lookup := map[string]string{}
	for _, rf := range RouteFeatureRegistry {
		lookup[rf.Method+":"+rf.Pattern] = rf.Feature
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Path() // Echo route pattern (e.g. "/v1/engines/:model/completions")
			method := c.Request().Method
			feature := lookup[method+":"+path]
			if feature == "" {
				feature = lookup["*:"+path]
			}
			if feature == "" {
				return next(c) // no restriction for this route
			}
			user := GetUser(c)
			if user == nil {
				return next(c) // auth middleware handles unauthenticated
			}
			if user.Role == RoleAdmin {
				return next(c)
			}
			perm, err := GetCachedUserPermissions(c, db, user.ID)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, schema.ErrorResponse{
					Error: &schema.APIError{
						Message: "failed to check permissions",
						Code:    http.StatusInternalServerError,
						Type:    "server_error",
					},
				})
			}
			val, exists := perm.Permissions[feature]
			if !exists {
				if !isDefaultOnFeature(feature) {
					return c.JSON(http.StatusForbidden, schema.ErrorResponse{
						Error: &schema.APIError{
							Message: "feature not enabled for your account: " + feature,
							Code:    http.StatusForbidden,
							Type:    "authorization_error",
						},
					})
				}
			} else if !val {
				return c.JSON(http.StatusForbidden, schema.ErrorResponse{
					Error: &schema.APIError{
						Message: "feature not enabled for your account: " + feature,
						Code:    http.StatusForbidden,
						Type:    "authorization_error",
					},
				})
			}
			return next(c)
		}
	}
}

// RequireModelAccess returns a global middleware that checks the user is allowed
// to use the resolved model. It extracts the model name directly from the request
// (path param, query param, JSON body, or form value) rather than relying on a
// context key set by downstream route-specific middleware.
func RequireModelAccess(db *gorm.DB) echo.MiddlewareFunc {
	if db == nil {
		return NoopMiddleware()
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			user := GetUser(c)
			if user == nil {
				return next(c)
			}
			if user.Role == RoleAdmin {
				return next(c)
			}

			// Check if this user even has a model allowlist enabled before
			// doing the expensive body read. Most users won't have restrictions.
			// Uses request-scoped cache to avoid duplicate DB hit when
			// RequireRouteFeature already fetched permissions.
			perm, err := GetCachedUserPermissions(c, db, user.ID)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, schema.ErrorResponse{
					Error: &schema.APIError{
						Message: "failed to check permissions",
						Code:    http.StatusInternalServerError,
						Type:    "server_error",
					},
				})
			}
			allowlist := perm.AllowedModels
			if !allowlist.Enabled {
				return next(c)
			}

			modelName := extractModelFromRequest(c)
			if modelName == "" {
				return next(c)
			}

			for _, m := range allowlist.Models {
				if m == modelName {
					return next(c)
				}
			}

			return c.JSON(http.StatusForbidden, schema.ErrorResponse{
				Error: &schema.APIError{
					Message: "access denied to model: " + modelName,
					Code:    http.StatusForbidden,
					Type:    "authorization_error",
				},
			})
		}
	}
}

// extractModelFromRequest extracts the model name from various request sources.
// It checks URL path params, query params, JSON body, and form values.
// For JSON bodies, it peeks at the body and resets it so downstream handlers
// can still read it.
func extractModelFromRequest(c echo.Context) string {
	// 1. URL path param (e.g. /v1/engines/:model/completions)
	if model := c.Param("model"); model != "" {
		return model
	}
	// 2. Query param
	if model := c.QueryParam("model"); model != "" {
		return model
	}
	// 3. Peek at JSON body
	if strings.HasPrefix(c.Request().Header.Get("Content-Type"), "application/json") {
		body, err := io.ReadAll(c.Request().Body)
		c.Request().Body = io.NopCloser(bytes.NewReader(body)) // always reset
		if err == nil && len(body) > 0 {
			var m struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(body, &m) == nil && m.Model != "" {
				return m.Model
			}
		}
	}
	// 4. Form value (multipart/form-data)
	if model := c.FormValue("model"); model != "" {
		return model
	}
	return ""
}

// tryAuthenticate attempts to authenticate the request using the database.
func tryAuthenticate(c echo.Context, db *gorm.DB, appConfig *config.ApplicationConfig) *User {
	hmacSecret := appConfig.Auth.APIKeyHMACSecret

	// a. Session cookie
	if cookie, err := c.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		if user, session := ValidateSession(db, cookie.Value, hmacSecret); user != nil {
			// Store session for rotation check in middleware
			c.Set("_auth_session", session)
			return user
		}
	}

	// b. Authorization: Bearer token
	authHeader := c.Request().Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")

		// Try as session ID first
		if user, _ := ValidateSession(db, token, hmacSecret); user != nil {
			return user
		}

		// Try as user API key
		if key, err := ValidateAPIKey(db, token, hmacSecret); err == nil {
			return &key.User
		}
	}

	// c. x-api-key / xi-api-key headers
	for _, header := range []string{"x-api-key", "xi-api-key"} {
		if key := c.Request().Header.Get(header); key != "" {
			if apiKey, err := ValidateAPIKey(db, key, hmacSecret); err == nil {
				return &apiKey.User
			}
		}
	}

	// d. token cookie (legacy)
	if cookie, err := c.Cookie("token"); err == nil && cookie.Value != "" {
		// Try as user API key
		if key, err := ValidateAPIKey(db, cookie.Value, hmacSecret); err == nil {
			return &key.User
		}
	}

	return nil
}

// extractKey extracts an API key from the request (all sources).
func extractKey(c echo.Context) string {
	// Authorization header
	auth := c.Request().Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if auth != "" {
		return auth
	}

	// x-api-key
	if key := c.Request().Header.Get("x-api-key"); key != "" {
		return key
	}

	// xi-api-key
	if key := c.Request().Header.Get("xi-api-key"); key != "" {
		return key
	}

	// token cookie
	if cookie, err := c.Cookie("token"); err == nil && cookie.Value != "" {
		return cookie.Value
	}

	return ""
}

// isValidLegacyKey checks if the key matches any configured API key
// using constant-time comparison to prevent timing attacks.
func isValidLegacyKey(key string, appConfig *config.ApplicationConfig) bool {
	for _, validKey := range appConfig.ApiKeys {
		if subtle.ConstantTimeCompare([]byte(key), []byte(validKey)) == 1 {
			return true
		}
	}
	return false
}

// isExemptPath returns true if the path should skip authentication.
func isExemptPath(path string, appConfig *config.ApplicationConfig) bool {
	// Auth endpoints are always public
	if strings.HasPrefix(path, "/api/auth/") {
		return true
	}

	// Check configured exempt paths
	for _, p := range appConfig.PathWithoutAuth {
		if strings.HasPrefix(path, p) {
			return true
		}
	}

	return false
}

// isAPIPath returns true for paths that always require authentication.
func isAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/") ||
		strings.HasPrefix(path, "/v1/") ||
		strings.HasPrefix(path, "/models/") ||
		strings.HasPrefix(path, "/backends/") ||
		strings.HasPrefix(path, "/backend/") ||
		strings.HasPrefix(path, "/tts") ||
		strings.HasPrefix(path, "/vad") ||
		strings.HasPrefix(path, "/video") ||
		strings.HasPrefix(path, "/stores/") ||
		strings.HasPrefix(path, "/system") ||
		strings.HasPrefix(path, "/ws/") ||
		strings.HasPrefix(path, "/generated-") ||
		path == "/metrics"
}

// authError returns an appropriate error response.
func authError(c echo.Context, appConfig *config.ApplicationConfig) error {
	c.Response().Header().Set("WWW-Authenticate", "Bearer")

	if appConfig.OpaqueErrors {
		return c.NoContent(http.StatusUnauthorized)
	}

	contentType := c.Request().Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		return c.JSON(http.StatusUnauthorized, schema.ErrorResponse{
			Error: &schema.APIError{
				Message: "An authentication key is required",
				Code:    http.StatusUnauthorized,
				Type:    "invalid_request_error",
			},
		})
	}

	return c.JSON(http.StatusUnauthorized, schema.ErrorResponse{
		Error: &schema.APIError{
			Message: "An authentication key is required",
			Code:    http.StatusUnauthorized,
			Type:    "invalid_request_error",
		},
	})
}
