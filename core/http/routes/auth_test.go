//go:build auth

package routes_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

func newTestAuthApp(db *gorm.DB, appConfig *config.ApplicationConfig) *echo.Echo {
	e := echo.New()

	// Apply auth middleware
	e.Use(auth.Middleware(db, appConfig))

	// We can't use routes.RegisterAuthRoutes directly since it needs *application.Application.
	// Instead, we register the routes manually for testing.

	// GET /api/auth/status
	e.GET("/api/auth/status", func(c echo.Context) error {
		authEnabled := db != nil
		providers := []string{}
		hasUsers := false

		if authEnabled {
			var count int64
			db.Model(&auth.User{}).Count(&count)
			hasUsers = count > 0

			providers = append(providers, "local")
			if appConfig.Auth.GitHubClientID != "" {
				providers = append(providers, "github")
			}
		}

		resp := map[string]interface{}{
			"authEnabled": authEnabled,
			"providers":   providers,
			"hasUsers":    hasUsers,
		}

		user := auth.GetUser(c)
		if user != nil {
			resp["user"] = map[string]interface{}{
				"id":   user.ID,
				"role": user.Role,
			}
		} else {
			resp["user"] = nil
		}
		return c.JSON(http.StatusOK, resp)
	})

	// POST /api/auth/register
	e.POST("/api/auth/register", func(c echo.Context) error {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Name     string `json:"name"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		}
		body.Email = strings.TrimSpace(body.Email)
		body.Name = strings.TrimSpace(body.Name)
		if body.Email == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "email is required"})
		}
		if len(body.Password) < 8 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		}
		var existing auth.User
		if err := db.Where("email = ? AND provider = ?", body.Email, "local").First(&existing).Error; err == nil {
			return c.JSON(http.StatusConflict, map[string]string{"error": "an account with this email already exists"})
		}
		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
		}
		role := auth.AssignRole(db, body.Email, appConfig.Auth.AdminEmail)
		status := auth.StatusActive
		if appConfig.Auth.RegistrationMode == "approval" && role != auth.RoleAdmin {
			status = auth.StatusPending
		}
		name := body.Name
		if name == "" {
			name = body.Email
		}
		user := &auth.User{
			ID: uuid.New().String(), Email: body.Email, Name: name,
			Provider: "local", Subject: body.Email, PasswordHash: hash,
			Role: role, Status: status,
		}
		if err := db.Create(user).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
		}
		if status == auth.StatusPending {
			return c.JSON(http.StatusOK, map[string]interface{}{"message": "registration successful, awaiting admin approval", "pending": true})
		}
		sessionID, err := auth.CreateSession(db, user.ID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		}
		auth.SetSessionCookie(c, sessionID)
		return c.JSON(http.StatusCreated, map[string]interface{}{
			"user": map[string]interface{}{"id": user.ID, "email": user.Email, "name": user.Name, "role": user.Role},
		})
	})

	// POST /api/auth/login
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
		auth.MaybePromote(db, &user, appConfig.Auth.AdminEmail)
		sessionID, err := auth.CreateSession(db, user.ID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		}
		auth.SetSessionCookie(c, sessionID)
		return c.JSON(http.StatusOK, map[string]interface{}{
			"user": map[string]interface{}{"id": user.ID, "email": user.Email, "name": user.Name, "role": user.Role},
		})
	})

	// POST /api/auth/logout
	e.POST("/api/auth/logout", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}
		if cookie, err := c.Cookie("session"); err == nil && cookie.Value != "" {
			auth.DeleteSession(db, cookie.Value)
		}
		auth.ClearSessionCookie(c)
		return c.JSON(http.StatusOK, map[string]string{"message": "logged out"})
	})

	// GET /api/auth/me
	e.GET("/api/auth/me", func(c echo.Context) error {
		user := auth.GetUser(c)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}
		return c.JSON(http.StatusOK, map[string]interface{}{
			"id":    user.ID,
			"email": user.Email,
			"role":  user.Role,
		})
	})

	// POST /api/auth/api-keys
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
			"key":       plaintext,
			"id":        record.ID,
			"name":      record.Name,
			"keyPrefix": record.KeyPrefix,
		})
	})

	// GET /api/auth/api-keys
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
			})
		}
		return c.JSON(http.StatusOK, map[string]interface{}{"keys": result})
	})

	// DELETE /api/auth/api-keys/:id
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

	// Admin: GET /api/auth/admin/users
	adminMw := auth.RequireAdmin()
	e.GET("/api/auth/admin/users", func(c echo.Context) error {
		var users []auth.User
		db.Order("created_at ASC").Find(&users)
		result := make([]map[string]interface{}, 0, len(users))
		for _, u := range users {
			result = append(result, map[string]interface{}{"id": u.ID, "role": u.Role, "email": u.Email})
		}
		return c.JSON(http.StatusOK, map[string]interface{}{"users": result})
	}, adminMw)

	// Admin: PUT /api/auth/admin/users/:id/role
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

	// Admin: DELETE /api/auth/admin/users/:id
	e.DELETE("/api/auth/admin/users/:id", func(c echo.Context) error {
		currentUser := auth.GetUser(c)
		targetID := c.Param("id")
		if currentUser.ID == targetID {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot delete yourself"})
		}
		db.Where("user_id = ?", targetID).Delete(&auth.Session{})
		db.Where("user_id = ?", targetID).Delete(&auth.UserAPIKey{})
		result := db.Where("id = ?", targetID).Delete(&auth.User{})
		if result.RowsAffected == 0 {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "user deleted"})
	}, adminMw)

	// Regular API endpoint for testing
	e.POST("/v1/chat/completions", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	e.GET("/v1/models", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	return e
}

// Helper to create test user
func createRouteTestUser(db *gorm.DB, email, role string) *auth.User {
	user := &auth.User{
		ID:       "user-" + email,
		Email:    email,
		Name:     "Test " + role,
		Provider: "github",
		Subject:  "sub-" + email,
		Role:     role,
		Status:   auth.StatusActive,
	}
	Expect(db.Create(user).Error).ToNot(HaveOccurred())
	return user
}

func doAuthRequest(e *echo.Echo, method, path string, body []byte, opts ...func(*http.Request)) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, opt := range opts {
		opt(req)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func withSession(sessionID string) func(*http.Request) {
	return func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	}
}

func withBearer(token string) func(*http.Request) {
	return func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

var _ = Describe("Auth Routes", Label("auth"), func() {
	var (
		db        *gorm.DB
		appConfig *config.ApplicationConfig
	)

	BeforeEach(func() {
		var err error
		db, err = auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		appConfig = config.NewApplicationConfig()
		appConfig.Auth.Enabled = true
		appConfig.Auth.GitHubClientID = "test-client-id"
	})

	Context("GET /api/auth/status", func() {
		It("returns authEnabled=true and provider list when auth enabled", func() {
			app := newTestAuthApp(db, appConfig)
			rec := doAuthRequest(app, "GET", "/api/auth/status", nil)
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			Expect(resp["authEnabled"]).To(BeTrue())
			providers := resp["providers"].([]interface{})
			Expect(providers).To(ContainElement("github"))
		})

		It("returns authEnabled=false when auth disabled", func() {
			app := newTestAuthApp(nil, config.NewApplicationConfig())
			rec := doAuthRequest(app, "GET", "/api/auth/status", nil)
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			Expect(resp["authEnabled"]).To(BeFalse())
		})

		It("returns user info when authenticated", func() {
			user := createRouteTestUser(db, "status@test.com", auth.RoleAdmin)
			sessionID, _ := auth.CreateSession(db, user.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "GET", "/api/auth/status", nil, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			Expect(resp["user"]).ToNot(BeNil())
		})

		It("returns user=null when not authenticated", func() {
			app := newTestAuthApp(db, appConfig)
			rec := doAuthRequest(app, "GET", "/api/auth/status", nil)
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			Expect(resp["user"]).To(BeNil())
		})

		It("returns hasUsers=false on fresh DB", func() {
			app := newTestAuthApp(db, appConfig)
			rec := doAuthRequest(app, "GET", "/api/auth/status", nil)

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			Expect(resp["hasUsers"]).To(BeFalse())
		})
	})

	Context("POST /api/auth/logout", func() {
		It("deletes session and clears cookie", func() {
			user := createRouteTestUser(db, "logout@test.com", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, user.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "POST", "/api/auth/logout", nil, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))

			// Session should be deleted
			Expect(auth.ValidateSession(db, sessionID)).To(BeNil())
		})

		It("returns 401 when not authenticated", func() {
			app := newTestAuthApp(db, appConfig)
			rec := doAuthRequest(app, "POST", "/api/auth/logout", nil)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})
	})

	Context("GET /api/auth/me", func() {
		It("returns current user profile", func() {
			user := createRouteTestUser(db, "me@test.com", auth.RoleAdmin)
			sessionID, _ := auth.CreateSession(db, user.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "GET", "/api/auth/me", nil, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			Expect(resp["email"]).To(Equal("me@test.com"))
			Expect(resp["role"]).To(Equal("admin"))
		})

		It("returns 401 when not authenticated", func() {
			app := newTestAuthApp(db, appConfig)
			rec := doAuthRequest(app, "GET", "/api/auth/me", nil)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})
	})

	Context("POST /api/auth/api-keys", func() {
		It("creates API key and returns plaintext once", func() {
			user := createRouteTestUser(db, "apikey@test.com", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, user.ID)
			app := newTestAuthApp(db, appConfig)

			body, _ := json.Marshal(map[string]string{"name": "my key"})
			rec := doAuthRequest(app, "POST", "/api/auth/api-keys", body, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusCreated))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			Expect(resp["key"]).To(HavePrefix("lai-"))
			Expect(resp["name"]).To(Equal("my key"))
		})

		It("key is usable for authentication", func() {
			user := createRouteTestUser(db, "apikey2@test.com", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, user.ID)
			app := newTestAuthApp(db, appConfig)

			body, _ := json.Marshal(map[string]string{"name": "usable key"})
			rec := doAuthRequest(app, "POST", "/api/auth/api-keys", body, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusCreated))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			apiKey := resp["key"].(string)

			// Use the key for API access
			rec = doAuthRequest(app, "GET", "/v1/models", nil, withBearer(apiKey))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("returns 401 when not authenticated", func() {
			app := newTestAuthApp(db, appConfig)
			body, _ := json.Marshal(map[string]string{"name": "test"})
			rec := doAuthRequest(app, "POST", "/api/auth/api-keys", body)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})
	})

	Context("GET /api/auth/api-keys", func() {
		It("lists user's API keys without plaintext", func() {
			user := createRouteTestUser(db, "list@test.com", auth.RoleUser)
			auth.CreateAPIKey(db, user.ID, "key1", auth.RoleUser)
			auth.CreateAPIKey(db, user.ID, "key2", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, user.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "GET", "/api/auth/api-keys", nil, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			keys := resp["keys"].([]interface{})
			Expect(keys).To(HaveLen(2))
		})

		It("does not show other users' keys", func() {
			user1 := createRouteTestUser(db, "user1@test.com", auth.RoleUser)
			user2 := createRouteTestUser(db, "user2@test.com", auth.RoleUser)
			auth.CreateAPIKey(db, user1.ID, "user1-key", auth.RoleUser)
			auth.CreateAPIKey(db, user2.ID, "user2-key", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, user1.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "GET", "/api/auth/api-keys", nil, withSession(sessionID))
			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			keys := resp["keys"].([]interface{})
			Expect(keys).To(HaveLen(1))
		})
	})

	Context("DELETE /api/auth/api-keys/:id", func() {
		It("revokes user's own key", func() {
			user := createRouteTestUser(db, "revoke@test.com", auth.RoleUser)
			plaintext, record, err := auth.CreateAPIKey(db, user.ID, "to-revoke", auth.RoleUser)
			Expect(err).ToNot(HaveOccurred())
			sessionID, _ := auth.CreateSession(db, user.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "DELETE", "/api/auth/api-keys/"+record.ID, nil, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))

			// Key should no longer work
			rec = doAuthRequest(app, "GET", "/v1/models", nil, withBearer(plaintext))
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("returns 404 for another user's key", func() {
			user1 := createRouteTestUser(db, "owner@test.com", auth.RoleUser)
			user2 := createRouteTestUser(db, "attacker@test.com", auth.RoleUser)
			_, record, _ := auth.CreateAPIKey(db, user1.ID, "secret-key", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, user2.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "DELETE", "/api/auth/api-keys/"+record.ID, nil, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusNotFound))
		})
	})

	Context("Admin: GET /api/auth/admin/users", func() {
		It("returns all users for admin", func() {
			admin := createRouteTestUser(db, "admin@test.com", auth.RoleAdmin)
			createRouteTestUser(db, "user@test.com", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, admin.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "GET", "/api/auth/admin/users", nil, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			users := resp["users"].([]interface{})
			Expect(users).To(HaveLen(2))
		})

		It("returns 403 for non-admin user", func() {
			user := createRouteTestUser(db, "nonadmin@test.com", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, user.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "GET", "/api/auth/admin/users", nil, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})
	})

	Context("Admin: PUT /api/auth/admin/users/:id/role", func() {
		It("changes user role", func() {
			admin := createRouteTestUser(db, "admin2@test.com", auth.RoleAdmin)
			user := createRouteTestUser(db, "promote@test.com", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, admin.ID)
			app := newTestAuthApp(db, appConfig)

			body, _ := json.Marshal(map[string]string{"role": "admin"})
			rec := doAuthRequest(app, "PUT", "/api/auth/admin/users/"+user.ID+"/role", body, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))

			// Verify in DB
			var updated auth.User
			db.First(&updated, "id = ?", user.ID)
			Expect(updated.Role).To(Equal(auth.RoleAdmin))
		})

		It("prevents self-demotion", func() {
			admin := createRouteTestUser(db, "self-demote@test.com", auth.RoleAdmin)
			sessionID, _ := auth.CreateSession(db, admin.ID)
			app := newTestAuthApp(db, appConfig)

			body, _ := json.Marshal(map[string]string{"role": "user"})
			rec := doAuthRequest(app, "PUT", "/api/auth/admin/users/"+admin.ID+"/role", body, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
		})

		It("returns 403 for non-admin", func() {
			user := createRouteTestUser(db, "sneaky@test.com", auth.RoleUser)
			other := createRouteTestUser(db, "victim@test.com", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, user.ID)
			app := newTestAuthApp(db, appConfig)

			body, _ := json.Marshal(map[string]string{"role": "admin"})
			rec := doAuthRequest(app, "PUT", "/api/auth/admin/users/"+other.ID+"/role", body, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})
	})

	Context("Admin: DELETE /api/auth/admin/users/:id", func() {
		It("deletes user and cascades to sessions + API keys", func() {
			admin := createRouteTestUser(db, "admin3@test.com", auth.RoleAdmin)
			target := createRouteTestUser(db, "delete-me@test.com", auth.RoleUser)
			auth.CreateSession(db, target.ID)
			auth.CreateAPIKey(db, target.ID, "target-key", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, admin.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "DELETE", "/api/auth/admin/users/"+target.ID, nil, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusOK))

			// User should be gone
			var count int64
			db.Model(&auth.User{}).Where("id = ?", target.ID).Count(&count)
			Expect(count).To(Equal(int64(0)))

			// Sessions and keys should be gone
			db.Model(&auth.Session{}).Where("user_id = ?", target.ID).Count(&count)
			Expect(count).To(Equal(int64(0)))
			db.Model(&auth.UserAPIKey{}).Where("user_id = ?", target.ID).Count(&count)
			Expect(count).To(Equal(int64(0)))
		})

		It("prevents self-deletion", func() {
			admin := createRouteTestUser(db, "admin4@test.com", auth.RoleAdmin)
			sessionID, _ := auth.CreateSession(db, admin.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "DELETE", "/api/auth/admin/users/"+admin.ID, nil, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
		})

		It("returns 403 for non-admin", func() {
			user := createRouteTestUser(db, "sneak@test.com", auth.RoleUser)
			target := createRouteTestUser(db, "target2@test.com", auth.RoleUser)
			sessionID, _ := auth.CreateSession(db, user.ID)
			app := newTestAuthApp(db, appConfig)

			rec := doAuthRequest(app, "DELETE", "/api/auth/admin/users/"+target.ID, nil, withSession(sessionID))
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})
	})

	Context("POST /api/auth/register", func() {
		It("registers first user as admin", func() {
			app := newTestAuthApp(db, appConfig)
			body, _ := json.Marshal(map[string]string{"email": "first@test.com", "password": "password123", "name": "First User"})
			rec := doAuthRequest(app, "POST", "/api/auth/register", body)
			Expect(rec.Code).To(Equal(http.StatusCreated))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			user := resp["user"].(map[string]interface{})
			Expect(user["role"]).To(Equal("admin"))
			Expect(user["email"]).To(Equal("first@test.com"))

			// Session cookie should be set
			cookies := rec.Result().Cookies()
			found := false
			for _, c := range cookies {
				if c.Name == "session" && c.Value != "" {
					found = true
				}
			}
			Expect(found).To(BeTrue())
		})

		It("registers second user as regular user", func() {
			createRouteTestUser(db, "existing@test.com", auth.RoleAdmin)
			app := newTestAuthApp(db, appConfig)
			body, _ := json.Marshal(map[string]string{"email": "second@test.com", "password": "password123"})
			rec := doAuthRequest(app, "POST", "/api/auth/register", body)
			Expect(rec.Code).To(Equal(http.StatusCreated))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			user := resp["user"].(map[string]interface{})
			Expect(user["role"]).To(Equal("user"))
		})

		It("rejects duplicate email", func() {
			app := newTestAuthApp(db, appConfig)
			body, _ := json.Marshal(map[string]string{"email": "dup@test.com", "password": "password123"})
			rec := doAuthRequest(app, "POST", "/api/auth/register", body)
			Expect(rec.Code).To(Equal(http.StatusCreated))

			rec = doAuthRequest(app, "POST", "/api/auth/register", body)
			Expect(rec.Code).To(Equal(http.StatusConflict))
		})

		It("rejects short password", func() {
			app := newTestAuthApp(db, appConfig)
			body, _ := json.Marshal(map[string]string{"email": "short@test.com", "password": "1234567"})
			rec := doAuthRequest(app, "POST", "/api/auth/register", body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
		})

		It("rejects empty email", func() {
			app := newTestAuthApp(db, appConfig)
			body, _ := json.Marshal(map[string]string{"email": "", "password": "password123"})
			rec := doAuthRequest(app, "POST", "/api/auth/register", body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
		})

		It("returns pending when registration mode is approval", func() {
			createRouteTestUser(db, "admin-existing@test.com", auth.RoleAdmin)
			appConfig.Auth.RegistrationMode = "approval"
			defer func() { appConfig.Auth.RegistrationMode = "" }()

			app := newTestAuthApp(db, appConfig)
			body, _ := json.Marshal(map[string]string{"email": "pending@test.com", "password": "password123"})
			rec := doAuthRequest(app, "POST", "/api/auth/register", body)
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			Expect(resp["pending"]).To(BeTrue())
		})
	})

	Context("POST /api/auth/login", func() {
		It("logs in with correct credentials", func() {
			app := newTestAuthApp(db, appConfig)
			// Register first
			body, _ := json.Marshal(map[string]string{"email": "login@test.com", "password": "password123"})
			doAuthRequest(app, "POST", "/api/auth/register", body)

			// Login
			body, _ = json.Marshal(map[string]string{"email": "login@test.com", "password": "password123"})
			rec := doAuthRequest(app, "POST", "/api/auth/login", body)
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			user := resp["user"].(map[string]interface{})
			Expect(user["email"]).To(Equal("login@test.com"))
		})

		It("rejects wrong password", func() {
			app := newTestAuthApp(db, appConfig)
			body, _ := json.Marshal(map[string]string{"email": "wrong@test.com", "password": "password123"})
			doAuthRequest(app, "POST", "/api/auth/register", body)

			body, _ = json.Marshal(map[string]string{"email": "wrong@test.com", "password": "wrongpassword"})
			rec := doAuthRequest(app, "POST", "/api/auth/login", body)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("rejects non-existent user", func() {
			app := newTestAuthApp(db, appConfig)
			body, _ := json.Marshal(map[string]string{"email": "nobody@test.com", "password": "password123"})
			rec := doAuthRequest(app, "POST", "/api/auth/login", body)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("rejects pending user", func() {
			createRouteTestUser(db, "admin-for-pending@test.com", auth.RoleAdmin)
			appConfig.Auth.RegistrationMode = "approval"
			defer func() { appConfig.Auth.RegistrationMode = "" }()

			app := newTestAuthApp(db, appConfig)
			body, _ := json.Marshal(map[string]string{"email": "pending-login@test.com", "password": "password123"})
			doAuthRequest(app, "POST", "/api/auth/register", body)

			appConfig.Auth.RegistrationMode = ""
			body, _ = json.Marshal(map[string]string{"email": "pending-login@test.com", "password": "password123"})
			rec := doAuthRequest(app, "POST", "/api/auth/login", body)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})
	})

	Context("GET /api/auth/status providers", func() {
		It("includes local provider when auth is enabled", func() {
			app := newTestAuthApp(db, appConfig)
			rec := doAuthRequest(app, "GET", "/api/auth/status", nil)
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			providers := resp["providers"].([]interface{})
			Expect(providers).To(ContainElement("local"))
			Expect(providers).To(ContainElement("github"))
		})
	})
})
