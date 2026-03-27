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
	"time"

	"github.com/mudler/xlog"
)

// RegistrationClient talks to the frontend's /api/node/* endpoints.
type RegistrationClient struct {
	FrontendURL       string
	RegistrationToken string
	HTTPTimeout       time.Duration // used for registration calls; defaults to 10s
}

// httpTimeout returns the configured timeout or a sensible default.
func (c *RegistrationClient) httpTimeout() time.Duration {
	if c.HTTPTimeout > 0 {
		return c.HTTPTimeout
	}
	return 10 * time.Second
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
	ID       string `json:"id"`
	APIToken string `json:"api_token,omitempty"`
}

// Register sends a single registration request and returns the node ID and
// (optionally) an auto-provisioned API token.
func (c *RegistrationClient) Register(body map[string]any) (string, string, error) {
	jsonBody, _ := json.Marshal(body)
	url := c.baseURL() + "/api/node/register"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := (&http.Client{Timeout: c.httpTimeout()}).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("posting to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("registration failed with status %d", resp.StatusCode)
	}

	var result RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decoding response: %w", err)
	}
	return result.ID, result.APIToken, nil
}

// RegisterWithRetry retries registration with exponential backoff.
func (c *RegistrationClient) RegisterWithRetry(body map[string]any, maxRetries int) (string, string, error) {
	backoff := 2 * time.Second
	maxBackoff := 30 * time.Second

	var nodeID, apiToken string
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		nodeID, apiToken, err = c.Register(body)
		if err == nil {
			return nodeID, apiToken, nil
		}
		if attempt == maxRetries {
			return "", "", fmt.Errorf("failed after %d attempts: %w", maxRetries, err)
		}
		xlog.Warn("Registration failed, retrying", "attempt", attempt, "next_retry", backoff, "error", err)
		time.Sleep(backoff)
		backoff = min(backoff*2, maxBackoff)
	}
	return nodeID, apiToken, err
}

// Heartbeat sends a single heartbeat POST with the given body.
func (c *RegistrationClient) Heartbeat(nodeID string, body map[string]any) error {
	jsonBody, _ := json.Marshal(body)
	url := c.baseURL() + "/api/node/" + nodeID + "/heartbeat"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("creating heartbeat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
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
			if err := c.Heartbeat(nodeID, body); err != nil {
				xlog.Warn("Heartbeat failed", "error", err)
			}
		}
	}
}

// Drain sets the node to draining status via POST /api/node/:id/drain.
func (c *RegistrationClient) Drain(nodeID string) error {
	url := c.baseURL() + "/api/node/" + nodeID + "/drain"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, nil)
	c.setAuth(req)

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("drain failed with status %d", resp.StatusCode)
	}
	return nil
}

// WaitForDrain polls GET /api/node/:id/models until all models report 0
// in-flight requests, or until timeout elapses.
func (c *RegistrationClient) WaitForDrain(nodeID string, timeout time.Duration) {
	url := c.baseURL() + "/api/node/" + nodeID + "/models"
	client := &http.Client{Timeout: 5 * time.Second}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		c.setAuth(req)

		resp, err := client.Do(req)
		if err != nil {
			break
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
		time.Sleep(1 * time.Second)
	}
	xlog.Warn("Drain timeout reached, proceeding with shutdown")
}

// Deregister marks the node as offline via POST /api/node/:id/deregister.
// The node row is preserved in the database so re-registration restores
// approval status.
func (c *RegistrationClient) Deregister(nodeID string) error {
	url := c.baseURL() + "/api/node/" + nodeID + "/deregister"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, nil)
	c.setAuth(req)

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
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

	if err := c.Drain(nodeID); err != nil {
		xlog.Warn("Failed to set drain status", "error", err)
	} else {
		c.WaitForDrain(nodeID, 30*time.Second)
	}

	if err := c.Deregister(nodeID); err != nil {
		xlog.Error("Failed to deregister", "error", err)
	} else {
		xlog.Info("Deregistered from frontend")
	}
}
