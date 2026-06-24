// Package workerregistry provides a shared HTTP client for worker node
// registration, heartbeating, draining, and deregistration against a
// LocalAI frontend. Both the backend worker (WorkerCMD) and the agent
// worker (AgentWorkerCMD) use this instead of duplicating the logic.
package workerregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// RegistrationClient talks to the frontend's /api/node/* endpoints.
type RegistrationClient struct {
	FrontendURL       string
	RegistrationToken string
	HTTPTimeout       time.Duration // used for registration calls; defaults to 10s
	client            *http.Client
	clientOnce        sync.Once
}

// httpTimeout returns the configured timeout or a sensible default.
func (c *RegistrationClient) httpTimeout() time.Duration {
	if c.HTTPTimeout > 0 {
		return c.HTTPTimeout
	}
	return 10 * time.Second
}

// httpClient returns the shared HTTP client, initializing it on first use.
func (c *RegistrationClient) httpClient() *http.Client {
	c.clientOnce.Do(func() {
		c.client = httpclient.NewWithTimeout(c.httpTimeout())
	})
	return c.client
}

// baseURL returns FrontendURL with any trailing slash stripped.
func (c *RegistrationClient) baseURL() string {
	return strings.TrimRight(c.FrontendURL, "/")
}

// setAuth adds an Authorization header when a token is configured.
func (c *RegistrationClient) setAuth(req *http.Request) {
	if c.RegistrationToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.RegistrationToken)
	}
}

// RegisterResponse is the JSON body returned by /api/node/register.
type RegisterResponse struct {
	ID           string `json:"id"`
	Status       string `json:"status,omitempty"` // "pending" until an admin approves the node
	APIToken     string `json:"api_token,omitempty"`
	NatsJWT      string `json:"nats_jwt,omitempty"`
	NatsUserSeed string `json:"nats_user_seed,omitempty"`
}

// RegisterFull sends a single registration request and returns the full
// response (node ID, approval status, and optional API token / NATS creds).
// Re-registration is idempotent: the frontend preserves the node row and mints
// a fresh NATS JWT each call, so this doubles as the credential-refresh call.
func (c *RegistrationClient) RegisterFull(ctx context.Context, body map[string]any) (*RegisterResponse, error) {
	jsonBody, _ := json.Marshal(body)
	url := c.baseURL() + "/api/node/register"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("posting to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("registration failed with status %d", resp.StatusCode)
	}

	var result RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

// Register sends a single registration request and returns the node ID and
// optional credentials (API token for agent workers, NATS JWT when configured).
func (c *RegistrationClient) Register(ctx context.Context, body map[string]any) (nodeID, apiToken, natsJWT, natsSeed string, err error) {
	res, err := c.RegisterFull(ctx, body)
	if err != nil {
		return "", "", "", "", err
	}
	return res.ID, res.APIToken, res.NatsJWT, res.NatsUserSeed, nil
}

// RegisterWithRetry retries registration with exponential backoff.
func (c *RegistrationClient) RegisterWithRetry(ctx context.Context, body map[string]any, maxRetries int) (nodeID, apiToken, natsJWT, natsSeed string, err error) {
	backoff := 2 * time.Second
	maxBackoff := 30 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		nodeID, apiToken, natsJWT, natsSeed, err = c.Register(ctx, body)
		if err == nil {
			return nodeID, apiToken, natsJWT, natsSeed, nil
		}
		if attempt == maxRetries {
			return "", "", "", "", fmt.Errorf("failed after %d attempts: %w", maxRetries, err)
		}
		xlog.Warn("Registration failed, retrying", "attempt", attempt, "next_retry", backoff, "error", err)
		select {
		case <-ctx.Done():
			return "", "", "", "", ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
	return nodeID, apiToken, natsJWT, natsSeed, err
}

// Heartbeat sends a single heartbeat POST with the given body.
func (c *RegistrationClient) Heartbeat(ctx context.Context, nodeID string, body map[string]any) error {
	jsonBody, _ := json.Marshal(body)
	url := c.baseURL() + "/api/node/" + nodeID + "/heartbeat"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("creating heartbeat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// HeartbeatLoop runs heartbeats at the given interval until ctx is cancelled.
// bodyFn is called each tick to build the heartbeat payload (e.g. VRAM stats).
func (c *RegistrationClient) HeartbeatLoop(ctx context.Context, nodeID string, interval time.Duration, bodyFn func() map[string]any) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			body := bodyFn()
			if err := c.Heartbeat(ctx, nodeID, body); err != nil {
				xlog.Warn("Heartbeat failed", "error", err)
			}
		}
	}
}

// Drain sets the node to draining status via POST /api/node/:id/drain.
func (c *RegistrationClient) Drain(ctx context.Context, nodeID string) error {
	url := c.baseURL() + "/api/node/" + nodeID + "/drain"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("creating drain request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("drain failed with status %d", resp.StatusCode)
	}
	return nil
}

// WaitForDrain polls GET /api/node/:id/models until all models report 0
// in-flight requests, or until timeout elapses.
func (c *RegistrationClient) WaitForDrain(ctx context.Context, nodeID string, timeout time.Duration) {
	url := c.baseURL() + "/api/node/" + nodeID + "/models"

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			xlog.Warn("Failed to create drain poll request", "error", err)
			return
		}
		c.setAuth(req)

		resp, err := c.httpClient().Do(req)
		if err != nil {
			xlog.Warn("Drain poll failed, will retry", "error", err)
			select {
			case <-ctx.Done():
				xlog.Warn("Drain wait cancelled")
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}
		var models []struct {
			InFlight int `json:"in_flight"`
		}
		json.NewDecoder(resp.Body).Decode(&models)
		resp.Body.Close()

		total := 0
		for _, m := range models {
			total += m.InFlight
		}
		if total == 0 {
			xlog.Info("All in-flight requests drained")
			return
		}
		xlog.Info("Waiting for in-flight requests", "count", total)
		select {
		case <-ctx.Done():
			xlog.Warn("Drain wait cancelled")
			return
		case <-time.After(1 * time.Second):
		}
	}
	xlog.Warn("Drain timeout reached, proceeding with shutdown")
}

// Deregister marks the node as offline via POST /api/node/:id/deregister.
// The node row is preserved in the database so re-registration restores
// approval status.
func (c *RegistrationClient) Deregister(ctx context.Context, nodeID string) error {
	url := c.baseURL() + "/api/node/" + nodeID + "/deregister"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("creating deregister request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deregistration failed with status %d", resp.StatusCode)
	}
	return nil
}

// GracefulDeregister performs drain -> wait -> deregister in sequence.
// This is the standard shutdown sequence for backend workers.
func (c *RegistrationClient) GracefulDeregister(nodeID string) {
	if c.FrontendURL == "" || nodeID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := c.Drain(ctx, nodeID); err != nil {
		xlog.Warn("Failed to set drain status", "error", err)
	} else {
		c.WaitForDrain(ctx, nodeID, 30*time.Second)
	}

	if err := c.Deregister(ctx, nodeID); err != nil {
		xlog.Error("Failed to deregister", "error", err)
	} else {
		xlog.Info("Deregistered from frontend")
	}
}
