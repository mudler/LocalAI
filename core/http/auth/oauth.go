package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"
	"golang.org/x/oauth2"
	githubOAuth "golang.org/x/oauth2/github"
	"gorm.io/gorm"
)

// providerEntry holds the OAuth2/OIDC config for a single provider.
type providerEntry struct {
	oauth2Config oauth2.Config
	oidcVerifier *oidc.IDTokenVerifier // nil for GitHub (API-based user info)
	name         string
	userInfoURL  string // only used for GitHub
}

// oauthUserInfo is a provider-agnostic representation of an authenticated user.
type oauthUserInfo struct {
	Subject   string
	Email     string
	Name      string
	AvatarURL string
}

// OAuthManager manages multiple OAuth/OIDC providers.
type OAuthManager struct {
	providers map[string]*providerEntry
}

// OAuthParams groups the parameters needed to create an OAuthManager.
type OAuthParams struct {
	GitHubClientID     string
	GitHubClientSecret string
	OIDCIssuer         string
	OIDCClientID       string
	OIDCClientSecret   string
}

// NewOAuthManager creates an OAuthManager from the given params.
func NewOAuthManager(baseURL string, params OAuthParams) (*OAuthManager, error) {
	m := &OAuthManager{providers: make(map[string]*providerEntry)}

	if params.GitHubClientID != "" {
		m.providers[ProviderGitHub] = &providerEntry{
			name: ProviderGitHub,
			oauth2Config: oauth2.Config{
				ClientID:     params.GitHubClientID,
				ClientSecret: params.GitHubClientSecret,
				Endpoint:     githubOAuth.Endpoint,
				RedirectURL:  baseURL + "/api/auth/github/callback",
				Scopes:       []string{"user:email", "read:user"},
			},
			userInfoURL: "https://api.github.com/user",
		}
	}

	if params.OIDCClientID != "" && params.OIDCIssuer != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		provider, err := oidc.NewProvider(ctx, params.OIDCIssuer)
		if err != nil {
			return nil, fmt.Errorf("OIDC discovery failed for %s: %w", params.OIDCIssuer, err)
		}

		verifier := provider.Verifier(&oidc.Config{ClientID: params.OIDCClientID})

		m.providers[ProviderOIDC] = &providerEntry{
			name: ProviderOIDC,
			oauth2Config: oauth2.Config{
				ClientID:     params.OIDCClientID,
				ClientSecret: params.OIDCClientSecret,
				Endpoint:     provider.Endpoint(),
				RedirectURL:  baseURL + "/api/auth/oidc/callback",
				Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
			},
			oidcVerifier: verifier,
		}
	}

	return m, nil
}

// Providers returns the list of configured provider names.
func (m *OAuthManager) Providers() []string {
	names := make([]string, 0, len(m.providers))
	for name := range m.providers {
		names = append(names, name)
	}
	return names
}

// LoginHandler redirects the user to the OAuth provider's login page.
func (m *OAuthManager) LoginHandler(providerName string) echo.HandlerFunc {
	return func(c echo.Context) error {
		provider, ok := m.providers[providerName]
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "unknown provider"})
		}

		state, err := generateState()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to generate state"})
		}

		secure := isSecure(c)
		c.SetCookie(&http.Cookie{
			Name:     "oauth_state",
			Value:    state,
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   600, // 10 minutes
		})

		// Store invite code in cookie if provided
		if inviteCode := c.QueryParam("invite_code"); inviteCode != "" {
			c.SetCookie(&http.Cookie{
				Name:     "invite_code",
				Value:    inviteCode,
				Path:     "/",
				HttpOnly: true,
				Secure:   secure,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   600,
			})
		}

		url := provider.oauth2Config.AuthCodeURL(state)
		return c.Redirect(http.StatusTemporaryRedirect, url)
	}
}

// CallbackHandler handles the OAuth callback, creates/updates the user, and
// creates a session.
func (m *OAuthManager) CallbackHandler(providerName string, db *gorm.DB, adminEmail, registrationMode string) echo.HandlerFunc {
	return func(c echo.Context) error {
		provider, ok := m.providers[providerName]
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "unknown provider"})
		}

		// Validate state
		stateCookie, err := c.Cookie("oauth_state")
		if err != nil || stateCookie.Value == "" || stateCookie.Value != c.QueryParam("state") {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid OAuth state"})
		}

		// Clear state cookie
		c.SetCookie(&http.Cookie{
			Name:     "oauth_state",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   isSecure(c),
			MaxAge:   -1,
		})

		// Exchange code for token
		code := c.QueryParam("code")
		if code == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing authorization code"})
		}

		ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer cancel()

		token, err := provider.oauth2Config.Exchange(ctx, code)
		if err != nil {
			xlog.Error("OAuth code exchange failed", "provider", providerName, "error", err)
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "OAuth authentication failed"})
		}

		// Fetch user info — branch based on provider type
		var userInfo *oauthUserInfo
		if provider.oidcVerifier != nil {
			userInfo, err = extractOIDCUserInfo(ctx, provider.oidcVerifier, token)
		} else {
			userInfo, err = fetchGitHubUserInfoAsOAuth(ctx, token.AccessToken)
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to fetch user info"})
		}

		// Retrieve invite code from cookie if present
		var inviteCode string
		if ic, err := c.Cookie("invite_code"); err == nil && ic.Value != "" {
			inviteCode = ic.Value
			// Clear the invite code cookie
			c.SetCookie(&http.Cookie{
				Name:     "invite_code",
				Value:    "",
				Path:     "/",
				HttpOnly: true,
				Secure:   isSecure(c),
				MaxAge:   -1,
			})
		}

		// Upsert user (with invite code support)
		user, err := upsertOAuthUser(db, providerName, userInfo, adminEmail, registrationMode)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
		}

		// For new users that are pending, check if they have a valid invite
		if user.Status != StatusActive && inviteCode != "" {
			if invite, err := ValidateInvite(db, inviteCode); err == nil {
				user.Status = StatusActive
				db.Model(user).Update("status", StatusActive)
				ConsumeInvite(db, invite, user.ID)
			}
		}

		if user.Status != StatusActive {
			if registrationMode == "invite" {
				return c.JSON(http.StatusForbidden, map[string]string{"error": "a valid invite code is required to register"})
			}
			return c.JSON(http.StatusForbidden, map[string]string{"error": "account pending approval"})
		}

		// Maybe promote on login
		MaybePromote(db, user, adminEmail)

		// Create session
		sessionID, err := CreateSession(db, user.ID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		}

		SetSessionCookie(c, sessionID)
		return c.Redirect(http.StatusTemporaryRedirect, "/app")
	}
}

// extractOIDCUserInfo extracts user info from the OIDC ID token.
func extractOIDCUserInfo(ctx context.Context, verifier *oidc.IDTokenVerifier, token *oauth2.Token) (*oauthUserInfo, error) {
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, fmt.Errorf("no id_token in token response")
	}

	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("failed to verify ID token: %w", err)
	}

	var claims struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to parse ID token claims: %w", err)
	}

	return &oauthUserInfo{
		Subject:   claims.Sub,
		Email:     claims.Email,
		Name:      claims.Name,
		AvatarURL: claims.Picture,
	}, nil
}

type githubUserInfo struct {
	ID        int    `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// fetchGitHubUserInfoAsOAuth fetches GitHub user info and returns it as oauthUserInfo.
func fetchGitHubUserInfoAsOAuth(ctx context.Context, accessToken string) (*oauthUserInfo, error) {
	info, err := fetchGitHubUserInfo(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	return &oauthUserInfo{
		Subject:   fmt.Sprintf("%d", info.ID),
		Email:     info.Email,
		Name:      info.Name,
		AvatarURL: info.AvatarURL,
	}, nil
}

func fetchGitHubUserInfo(ctx context.Context, accessToken string) (*githubUserInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var info githubUserInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}

	// If no public email, fetch from /user/emails
	if info.Email == "" {
		info.Email, _ = fetchGitHubPrimaryEmail(ctx, accessToken)
	}

	return &info, nil
}

func fetchGitHubPrimaryEmail(ctx context.Context, accessToken string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user/emails", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var emails []githubEmail
	if err := json.Unmarshal(body, &emails); err != nil {
		return "", err
	}

	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}

	// Fall back to first verified email
	for _, e := range emails {
		if e.Verified {
			return e.Email, nil
		}
	}

	return "", fmt.Errorf("no verified email found")
}

func upsertOAuthUser(db *gorm.DB, provider string, info *oauthUserInfo, adminEmail, registrationMode string) (*User, error) {
	var user User
	err := db.Where("provider = ? AND subject = ?", provider, info.Subject).First(&user).Error
	if err == nil {
		// Existing user — update profile fields
		user.Name = info.Name
		user.AvatarURL = info.AvatarURL
		if info.Email != "" {
			user.Email = info.Email
		}
		db.Save(&user)
		return &user, nil
	}

	// New user
	status := StatusActive
	if registrationMode == "approval" || registrationMode == "invite" {
		status = StatusPending
	}

	role := AssignRole(db, info.Email, adminEmail)
	// First user is always active regardless of registration mode
	if role == RoleAdmin {
		status = StatusActive
	}

	user = User{
		ID:        uuid.New().String(),
		Email:     info.Email,
		Name:      info.Name,
		AvatarURL: info.AvatarURL,
		Provider:  provider,
		Subject:   info.Subject,
		Role:      role,
		Status:    status,
	}

	if err := db.Create(&user).Error; err != nil {
		return nil, err
	}

	return &user, nil
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
