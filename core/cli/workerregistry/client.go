// Package workerregistry provides a shared HTTP client for worker node
// registration, heartbeating, draining, and deregistration against a
// LocalAI frontend. Both the backend worker (WorkerCMD) and the agent
// worker (AgentWorkerCMD) use this instead of duplicating the logic.
package workerregistry

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// RegistrationClient talks to the frontend's /api/node/* endpoints.
//
// Registration is the one call that takes a join token or the shared
// registration token. It returns the own credential of the node, and every
// other call, and every later registration, presents that credential.
type RegistrationClient struct {
	FrontendURL string
	// RegistrationToken is the shared registration token of the deployment. It
	// is sent in registration only.
	RegistrationToken string
	// JoinToken is a single-use token that an admin minted. It is sent in
	// registration only, and only until the node holds its own credential.
	JoinToken string
	// Credential keeps the own credential of the node between runs, so that a
	// restart needs no join token. Nil keeps it in memory only.
	Credential  CredentialStore
	HTTPTimeout time.Duration // used for registration calls; defaults to 10s
	// HTTPClient, when set, carries every call instead of a client built from
	// HTTPTimeout: a worker that joined over P2P reaches the frontend through
	// the P2P layer, whatever host FrontendURL names.
	HTTPClient *http.Client
	client     *http.Client
	clientOnce sync.Once

	credMu      sync.Mutex
	credLoaded  bool
	nodeToken   string
	workerToken string
}

// CredentialStore keeps the own credential of a node.
type CredentialStore interface {
	// Load returns the stored credential, or the empty string when there is none
	// for this frontend and node name.
	Load() (string, error)
	// Save stores the credential.
	Save(token string) error
}

// NodeToken returns the own credential of the node, loading it from the store on
// first use. It is empty until the node has registered.
func (c *RegistrationClient) NodeToken() string {
	c.credMu.Lock()
	defer c.credMu.Unlock()
	if !c.credLoaded {
		c.credLoaded = true
		if c.Credential != nil {
			tok, err := c.Credential.Load()
			if err != nil {
				xlog.Warn("Could not read the stored node credential", "error", err)
			}
			c.nodeToken = tok
		}
	}
	return c.nodeToken
}

// WorkerToken returns the data plane token that the last registration handed
// over, or empty. A worker that registered with the shared registration token
// derives it from that token instead (workerctl.WorkerAuthToken).
func (c *RegistrationClient) WorkerToken() string {
	c.credMu.Lock()
	defer c.credMu.Unlock()
	return c.workerToken
}

func (c *RegistrationClient) adopt(res *RegisterResponse) {
	c.credMu.Lock()
	defer c.credMu.Unlock()
	if res.WorkerToken != "" {
		c.workerToken = res.WorkerToken
	}
	if res.NodeToken == "" {
		return
	}
	c.credLoaded = true
	c.nodeToken = res.NodeToken
	if c.Credential != nil {
		if err := c.Credential.Save(res.NodeToken); err != nil {
			// The worker runs on, and needs a new join token after a restart.
			xlog.Error("Could not store the node credential: a restart will need a new join token", "error", err)
		}
	}
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
		if c.HTTPClient != nil {
			c.client = c.HTTPClient
			return
		}
		c.client = httpclient.NewWithTimeout(c.httpTimeout())
	})
	return c.client
}

// baseURL returns FrontendURL with any trailing slash stripped.
func (c *RegistrationClient) baseURL() string {
	return strings.TrimRight(c.FrontendURL, "/")
}

// setAuth adds the own credential of the node as the Authorization header. The
// registration token is not a credential for any call but registration.
func (c *RegistrationClient) setAuth(req *http.Request) {
	if tok := c.NodeToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// The routes a worker can ask a credential for, as the frontend names them.
// They mirror the names of the route columns of a node (nodes.RoutesOf) and are
// copied rather than imported, for the reason statusPending is.
const (
	// RouteNATS is NATS for the control verbs and a direct dial of the worker
	// for the data. Its credential is the NATS user JWT and the handover of the
	// NATS address.
	RouteNATS = "nats"
	// RouteTunnel is the tunnel that the worker holds open to a frontend, over a
	// websocket or over P2P. Its credential is the tunnel token of the node.
	RouteTunnel = "tunnel"
)

// RegisterResponse is the JSON body returned by /api/node/register.
//
// The frontend mints a credential only for the routes that the registration
// asks for ("routes" in the body): the tunnel token for the tunnel, and the
// NATS user JWT with the address of NATS for NATS. A registration that asks for
// no route gets the node ID and the status, and no credential.
type RegisterResponse struct {
	ID           string `json:"id"`
	Status       string `json:"status,omitempty"` // "pending" until an admin approves the node
	APIToken     string `json:"api_token,omitempty"`
	NatsJWT      string `json:"nats_jwt,omitempty"`
	NatsUserSeed string `json:"nats_user_seed,omitempty"`
	// NodeToken is the own credential of the node for the node routes
	// (heartbeat, drain, deregister) and for registering again. It is sent once,
	// to a registration that used a join token or the registration token.
	NodeToken string `json:"node_token,omitempty"`
	// WorkerToken is the bearer token between the frontends and this worker for
	// the data plane. Empty when the deployment has none.
	WorkerToken string `json:"worker_token,omitempty"`
	// TunnelToken is the own credential of the node for the tunnel. It is sent
	// once, and only to a registration that asks for the tunnel.
	TunnelToken string `json:"tunnel_token,omitempty"`
	// NatsURL is the address workers use for NATS, NatsCA the CA of the server as
	// PEM, and NatsClientTLS says the server asks for a client certificate, which
	// the frontend cannot hand over. Sent to a registration that asks for NATS.
	NatsURL       string `json:"nats_url,omitempty"`
	NatsCA        string `json:"nats_ca,omitempty"`
	NatsClientTLS bool   `json:"nats_client_tls,omitempty"`
}

// RegisterFull sends a single registration request and returns the full
// response (node ID, approval status, and optional API token / NATS creds).
// Re-registration is idempotent: the frontend preserves the node row and mints
// a fresh NATS JWT each call, so this doubles as the credential-refresh call.
func (c *RegistrationClient) RegisterFull(ctx context.Context, body map[string]any) (*RegisterResponse, error) {
	// What the worker holds goes in the request. The frontend tries the own
	// credential first, then the join token, then the registration token, so a
	// worker that restarts never spends a join token.
	body = maps.Clone(body)
	if body == nil {
		body = map[string]any{}
	}
	delete(body, "token")
	if c.RegistrationToken != "" {
		body["token"] = c.RegistrationToken
	}
	if c.JoinToken != "" {
		body["join_token"] = c.JoinToken
	}
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
	c.adopt(&result)
	return &result, nil
}

// RegisterWithRetry retries registration with exponential backoff.
func (c *RegistrationClient) RegisterWithRetry(ctx context.Context, body map[string]any, maxRetries int) (nodeID, apiToken, natsJWT, natsSeed string, err error) {
	res, err := c.RegisterFullWithRetry(ctx, body, maxRetries)
	if err != nil {
		return "", "", "", "", err
	}
	return res.ID, res.APIToken, res.NatsJWT, res.NatsUserSeed, nil
}

// RegisterFullWithRetry is RegisterWithRetry that returns the whole response.
func (c *RegistrationClient) RegisterFullWithRetry(ctx context.Context, body map[string]any, maxRetries int) (*RegisterResponse, error) {
	if maxRetries < 1 {
		return nil, fmt.Errorf("registering: %d attempts allowed, want at least one", maxRetries)
	}
	backoff := 2 * time.Second
	maxBackoff := 30 * time.Second

	var err error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		var res *RegisterResponse
		res, err = c.RegisterFull(ctx, body)
		if err == nil {
			return res, nil
		}
		if attempt == maxRetries {
			return nil, fmt.Errorf("failed after %d attempts: %w", maxRetries, err)
		}
		xlog.Warn("Registration failed, retrying", "attempt", attempt, "next_retry", backoff, "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
	return nil, err
}

// Heartbeat sends a single heartbeat POST with the given body. It fails only when
// the request cannot be made.
func (c *RegistrationClient) Heartbeat(ctx context.Context, nodeID string, body map[string]any) error {
	resp, err := c.post(ctx, nodeID, body)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// SendHeartbeat sends a heartbeat and fails when the frontend refuses it, for
// example for a node that it does not know. Heartbeat, by contrast, fails only
// when the request cannot be made.
func (c *RegistrationClient) SendHeartbeat(ctx context.Context, nodeID string, body map[string]any) error {
	resp, err := c.post(ctx, nodeID, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("heartbeat refused with status %d", resp.StatusCode)
	}
	return nil
}

func (c *RegistrationClient) post(ctx context.Context, nodeID string, body map[string]any) (*http.Response, error) {
	jsonBody, _ := json.Marshal(body)
	url := c.baseURL() + "/api/node/" + nodeID + "/heartbeat"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating heartbeat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)
	return c.httpClient().Do(req)
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
	return c.postDrain(ctx, nodeID, nil)
}

// postDrain sends POST /api/node/:id/drain, with body as JSON when it is not
// nil.
func (c *RegistrationClient) postDrain(ctx context.Context, nodeID string, body any) error {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding drain request: %w", err)
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/api/node/"+nodeID+"/drain", payload)
	if err != nil {
		return fmt.Errorf("creating drain request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.setAuth(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
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

// Defaults of the graceful shutdown of a worker.
const (
	// DefaultShutdownRequestWait is how long a worker that shuts down waits
	// for the requests in flight on its replicas.
	DefaultShutdownRequestWait = 30 * time.Second
	// DefaultShutdownTimeout bounds drain, wait and deregistration together.
	DefaultShutdownTimeout = 60 * time.Second
)

// ShutdownOptions controls GracefulShutdown.
type ShutdownOptions struct {
	// LoadsEnded tells the frontend that the worker stopped every load it
	// ran and their processes exited, so it releases their jobs at once.
	LoadsEnded bool
	// RequestWait bounds the wait for the requests in flight. Zero means
	// DefaultShutdownRequestWait.
	RequestWait time.Duration
	// Timeout bounds the whole sequence. Zero means DefaultShutdownTimeout.
	Timeout time.Duration
}

// GracefulDeregister performs drain -> wait -> deregister in sequence with
// the default windows. This is the standard shutdown sequence for workers.
func (c *RegistrationClient) GracefulDeregister(nodeID string) {
	c.GracefulShutdown(nodeID, ShutdownOptions{})
}

// GracefulShutdown drains the node, waits for the requests in flight on its
// replicas (up to RequestWait), and deregisters it. The drain keeps the
// replicas routable while the frontend places replacements; the
// deregistration removes them.
func (c *RegistrationClient) GracefulShutdown(nodeID string, opts ShutdownOptions) {
	if c.FrontendURL == "" || nodeID == "" {
		return
	}
	wait := cmp.Or(opts.RequestWait, DefaultShutdownRequestWait)
	total := cmp.Or(opts.Timeout, DefaultShutdownTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), total)
	defer cancel()

	if err := c.drainForShutdown(ctx, nodeID, opts.LoadsEnded); err != nil {
		xlog.Warn("Failed to set drain status", "error", err)
	} else {
		c.WaitForDrain(ctx, nodeID, wait)
	}

	if err := c.Deregister(ctx, nodeID); err != nil {
		xlog.Error("Failed to deregister", "error", err)
	} else {
		xlog.Info("Deregistered from frontend")
	}
}

// drainForShutdown drains the node and says that the worker shuts down.
func (c *RegistrationClient) drainForShutdown(ctx context.Context, nodeID string, loadsEnded bool) error {
	return c.postDrain(ctx, nodeID, map[string]bool{"shutdown": true, "loads_ended": loadsEnded})
}

// SplitSync sends the split report of a node and returns the model-split
// workers attached to it (POST /api/node/{id}/split). An error leaves the
// caller with its last answer: the frontend may be down, or may not share
// over P2P.
func (c *RegistrationClient) SplitSync(ctx context.Context, nodeID string, rep workerctl.SplitReport) (*workerctl.SplitAssignment, error) {
	body, err := json.Marshal(rep)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/api/node/"+nodeID+"/split", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating split report request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("split report refused with status %d", resp.StatusCode)
	}
	var out workerctl.SplitAssignment
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("reading the split assignment: %w", err)
	}
	return &out, nil
}
