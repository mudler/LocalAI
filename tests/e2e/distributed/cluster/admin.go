package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

const (
	// adminPassword is sent with "acknowledge_weak_password": true, which sets
	// PasswordPolicy{AllowWeak: true} and skips the length floor and the zxcvbn
	// score entirely (core/http/auth/password.go). Only the technical
	// invariants still apply: non-empty, at most 72 bytes, no NUL. The
	// acknowledgement is deliberate rather than incidental, so a future
	// tightening of the policy cannot break every failover spec at setup time.
	adminPassword = "e2e-admin-password"
	// sessionCookieName mirrors the unexported constant in core/http/auth.
	// The register handler returns 201 both for "user created, here is your
	// session" and for "this email already exists" (a deliberate account
	// enumeration defence), so the status code alone cannot tell the two
	// apart: the presence of this cookie is the only reliable signal.
	sessionCookieName = "session"
	// authRequestTimeout bounds one register/login round trip.
	authRequestTimeout = 30 * time.Second
	// bodyExcerptLimit caps how much of an error response is quoted back.
	bodyExcerptLimit = 512
)

// ForTestingEmpty returns a Cluster with no processes. It exists so the package's
// own argument-validation specs do not need to start anything.
func ForTestingEmpty() *Cluster {
	return &Cluster{}
}

// AdminSession registers the admin user on frontend i and returns a client
// carrying the resulting session cookie. The email matches LOCALAI_ADMIN_EMAIL,
// which core/http/auth exempts from the approval gate and assigns the admin
// role, so registration alone yields an active admin session.
//
// Call this ONCE per cluster and share the client. Two reasons:
//
// One, a single rate limiter of 5 requests per minute per client IP guards
// POST /api/auth/token-login, POST /api/auth/register, POST /api/auth/login AND
// PUT /api/auth/password (core/http/routes/auth.go:190). They share one budget,
// and every e2e request arrives from 127.0.0.1, so a spec that changes a
// password spends from the same five.
//
// Two, the returned client is already good for every frontend: sessions live in
// the shared Postgres auth DB, the harness pins one HMAC secret across replicas
// so the session row resolves at any of them, and Go's cookie jar keys cookies
// by host without the port.
func (c *Cluster) AdminSession(i int) (*http.Client, error) {
	base, err := c.frontendBaseURL(i)
	if err != nil {
		return nil, err
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("creating cookie jar: %w", err)
	}
	// httpclient hardens the transport and refuses redirects; the jar is the one
	// thing it does not configure, and a session cookie is the whole point here.
	client := httpclient.NewWithTimeout(authRequestTimeout)
	client.Jar = jar

	credentials := map[string]any{
		"email":    c.opts.AdminEmail,
		"password": adminPassword,
	}
	registration := map[string]any{
		"email":                     c.opts.AdminEmail,
		"password":                  adminPassword,
		"name":                      "E2E Admin",
		"acknowledge_weak_password": true,
	}

	registerStatus, registerBody, err := postJSON(client, base+"/api/auth/register", registration)
	if err != nil {
		return nil, fmt.Errorf("registering admin on frontend %d: %w", i, err)
	}
	if hasSessionCookie(jar, base) {
		return client, nil
	}

	// No cookie means the user already existed (a repeat call against the same
	// Postgres), or registration was rejected. Log in; on failure the
	// registration response is the diagnosis, so carry it into the error.
	loginStatus, loginBody, err := postJSON(client, base+"/api/auth/login", credentials)
	if err != nil {
		return nil, fmt.Errorf("logging in admin on frontend %d: %w", i, err)
	}
	if loginStatus != http.StatusOK {
		return nil, fmt.Errorf(
			"admin login on frontend %d returned %d (%s); registration had returned %d (%s)",
			i, loginStatus, loginBody, registerStatus, registerBody)
	}
	if !hasSessionCookie(jar, base) {
		return nil, fmt.Errorf("admin login on frontend %d returned 200 but set no %q cookie: %s", i, sessionCookieName, loginBody)
	}
	return client, nil
}

// GetJSON performs an authenticated GET against a frontend and decodes the body.
func (c *Cluster) GetJSON(client *http.Client, frontend int, path string, out any) error {
	base, err := c.frontendBaseURL(frontend)
	if err != nil {
		return err
	}
	resp, err := client.Get(base + path)
	if err != nil {
		return fmt.Errorf("GET %s on frontend %d: %w", path, frontend, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s on frontend %d returned %d: %s", path, frontend, resp.StatusCode, excerpt(resp.Body))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s from frontend %d: %w", path, frontend, err)
	}
	return nil
}

// frontendBaseURL validates the index before FrontendURL indexes the slice: a
// bare index panic in a helper every failover spec calls is far harder to read
// than a named error.
func (c *Cluster) frontendBaseURL(i int) (string, error) {
	if i < 0 || i >= len(c.frontends) {
		return "", fmt.Errorf("frontend %d out of range (cluster has %d)", i, len(c.frontends))
	}
	return c.FrontendURL(i), nil
}

// postJSON sends body as JSON and returns the status plus an excerpt of the
// response, closing the body in every path.
func postJSON(client *http.Client, endpoint string, body any) (int, string, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, "", fmt.Errorf("marshalling request body: %w", err)
	}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(encoded))
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, excerpt(resp.Body), nil
}

// hasSessionCookie reports whether the jar holds a usable session for base.
func hasSessionCookie(jar *cookiejar.Jar, base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == sessionCookieName && cookie.Value != "" {
			return true
		}
	}
	return false
}

func excerpt(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, bodyExcerptLimit))
	if err != nil {
		return fmt.Sprintf("<unreadable body: %v>", err)
	}
	return string(bytes.TrimSpace(data))
}
