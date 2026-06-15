// Package httpapi provides a LocalAIClient that talks to a remote LocalAI
// instance over its REST API. Used by the standalone "local-ai mcp-server"
// subcommand to control a remote deployment over stdio.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/pkg/httpclient"
	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
	"github.com/mudler/LocalAI/pkg/vram"
)

// Client is a thin REST wrapper. It maps each LocalAIClient method to the
// matching admin endpoint. Errors from non-2xx responses include the body for
// the MCP layer to surface verbatim to the LLM.
type Client struct {
	BaseURL string
	APIKey  string

	HTTPClient *http.Client
}

// New returns a Client targeting baseURL with an optional bearer token.
func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: httpclient.NewWithTimeout(60 * time.Second),
	}
}

// Compile-time assertion.
var _ localaitools.LocalAIClient = (*Client)(nil)

// HTTPError is returned by do() for non-2xx responses. Callers should use
// errors.Is(err, ErrHTTPNotFound) instead of substring-matching on
// err.Error() — the latter is brittle to status-code formatting changes.
type HTTPError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: %d %s: %s", e.Method, e.Path, e.StatusCode, http.StatusText(e.StatusCode), strings.TrimSpace(e.Body))
}

// ErrHTTPNotFound is the sentinel for "the resource you asked for doesn't
// exist". Match it via errors.Is on an *HTTPError.
var ErrHTTPNotFound = errors.New("httpapi: not found")

// Is supports errors.Is(*HTTPError, ErrHTTPNotFound). The 500-with-text
// branch is a transitional fallback for /models/jobs/:uuid which today
// returns a 500 carrying "could not find any status for ID" instead of a
// proper 404. Drop the branch when the server is fixed.
func (e *HTTPError) Is(target error) bool {
	if target != ErrHTTPNotFound {
		return false
	}
	if e.StatusCode == http.StatusNotFound {
		return true
	}
	return e.StatusCode == http.StatusInternalServerError && strings.Contains(e.Body, "could not find")
}

// ---- HTTP helpers ----

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode %s %s response: %w (body=%q)", method, path, err, truncate(string(respBody), 200))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---- Models / gallery (read) ----

func (c *Client) GallerySearch(ctx context.Context, q localaitools.GallerySearchQuery) ([]gallery.Metadata, error) {
	// /models/available already returns []gallery.Metadata — pass it
	// through after applying the LLM-supplied filters client-side.
	var metas []gallery.Metadata
	if err := c.do(ctx, http.MethodGet, routeModelsAvail, nil, &metas); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	out := make([]gallery.Metadata, 0, limit)
	needle := strings.ToLower(q.Query)
	tag := strings.ToLower(q.Tag)
	for _, m := range metas {
		if q.Gallery != "" && m.Gallery.Name != q.Gallery {
			continue
		}
		if needle != "" && !contains(m.Name, needle) && !contains(m.Description, needle) && !containsTagsAny(m.Tags, needle) {
			continue
		}
		if tag != "" && !containsTagExact(m.Tags, tag) {
			continue
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (c *Client) ListInstalledModels(ctx context.Context, capability localaitools.Capability) ([]localaitools.InstalledModel, error) {
	_ = capability // Capability filtering is unavailable over the welcome HTTP shape today; see TODO below.
	// /v1/models is the OpenAI-compat shape; we use the LocalAI welcome JSON
	// for richer info.
	var welcome struct {
		ModelsConfig []struct {
			Name    string `json:"name"`
			Backend string `json:"backend"`
		} `json:"ModelsConfig"`
	}
	if err := c.do(ctx, http.MethodGet, routeWelcome, nil, &welcome); err != nil {
		return nil, err
	}
	// Capability filtering is unavailable over HTTP without a dedicated endpoint
	// — for now we return everything and let the LLM filter from the names. A
	// follow-up should add a /api/models?capability=chat endpoint.
	out := make([]localaitools.InstalledModel, 0, len(welcome.ModelsConfig))
	for _, m := range welcome.ModelsConfig {
		out = append(out, localaitools.InstalledModel{Name: m.Name, Backend: m.Backend})
	}
	return out, nil
}

func (c *Client) ListGalleries(ctx context.Context) ([]config.Gallery, error) {
	// /models/galleries returns []config.Gallery directly.
	var out []config.Gallery
	if err := c.do(ctx, http.MethodGet, routeModelsGall, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetJobStatus(ctx context.Context, jobID string) (*localaitools.JobStatus, error) {
	if jobID == "" {
		return nil, errors.New("job id is required")
	}
	var raw struct {
		Processed          bool    `json:"processed"`
		Cancelled          bool    `json:"cancelled"`
		Progress           float64 `json:"progress"`
		Message            string  `json:"message"`
		FileSize           string  `json:"file_size"`
		DownloadedSize     string  `json:"downloaded_size"`
		Error              string  `json:"error,omitempty"`
		GalleryElementName string  `json:"gallery_element_name"`
	}
	if err := c.do(ctx, http.MethodGet, routeJobStatus(jobID), nil, &raw); err != nil {
		// "no such job" is not a real failure — surface (nil, nil) so the
		// LLM can stop polling without treating the response as an error.
		if errors.Is(err, ErrHTTPNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &localaitools.JobStatus{
		ID:                 jobID,
		Processed:          raw.Processed,
		Cancelled:          raw.Cancelled,
		Progress:           raw.Progress,
		TotalFileSize:      raw.FileSize,
		DownloadedFileSize: raw.DownloadedSize,
		Message:            raw.Message,
		ErrorMessage:       raw.Error,
	}, nil
}

// GetModelConfig is intentionally a stub for the HTTP client: LocalAI's
// /models/edit/:name endpoint returns rendered HTML, not JSON, so the
// standalone CLI's `get_model_config` tool surfaces a clear error to the
// LLM. Tracked under the localai-assistant follow-ups (see
// .agents/localai-assistant-mcp.md) — once a JSON-only
// GET /api/models/config-yaml/:name endpoint lands on the server, this
// method calls it and the stub goes away.
//
// FIXME(localai-assistant): wire to a JSON read-back endpoint.
func (c *Client) GetModelConfig(_ context.Context, _ string) (*localaitools.ModelConfigView, error) {
	return nil, errors.New("get_model_config over HTTP not yet supported by this client; use the in-process inproc client or REST /models/edit/{name}")
}

// ---- Models / gallery (write) ----

func (c *Client) InstallModel(ctx context.Context, req localaitools.InstallModelRequest) (string, error) {
	body := map[string]any{"id": req.ModelName}
	if req.GalleryName != "" {
		body["id"] = req.GalleryName + "@" + req.ModelName
	}
	body["name"] = req.ModelName
	if len(req.Overrides) > 0 {
		body["overrides"] = req.Overrides
	}
	var resp struct {
		ID        string `json:"uuid"`
		StatusURL string `json:"status"`
	}
	if err := c.do(ctx, http.MethodPost, routeModelsApply, body, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (c *Client) ImportModelURI(ctx context.Context, req localaitools.ImportModelURIRequest) (*localaitools.ImportModelURIResponse, error) {
	if req.URI == "" {
		return nil, errors.New("uri is required")
	}
	body := map[string]any{"uri": req.URI}
	if req.BackendPreference != "" {
		// Server expects preferences as a JSON object; wrap the backend
		// preference accordingly.
		body["preferences"] = map[string]string{"backend": req.BackendPreference}
	}

	rawReq, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+routeModelsImport, bytes.NewReader(rawReq))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)

	// 400 with `error: "ambiguous import"` is not a transport error — it's the
	// disambiguation signal. Translate it back into AmbiguousBackend so the
	// MCP layer surface stays identical regardless of in-process vs HTTP.
	if resp.StatusCode == http.StatusBadRequest {
		var amb struct {
			Error      string   `json:"error"`
			Detail     string   `json:"detail"`
			Modality   string   `json:"modality"`
			Candidates []string `json:"candidates"`
			Hint       string   `json:"hint"`
		}
		if json.Unmarshal(respBody, &amb) == nil && amb.Error == "ambiguous import" {
			return &localaitools.ImportModelURIResponse{
				AmbiguousBackend:  true,
				Modality:          amb.Modality,
				BackendCandidates: amb.Candidates,
				Hint:              amb.Hint,
			}, nil
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("POST %s: %d %s: %s", routeModelsImport, resp.StatusCode, http.StatusText(resp.StatusCode), strings.TrimSpace(string(respBody)))
	}

	var raw struct {
		ID string `json:"uuid"`
	}
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, fmt.Errorf("decode import response: %w", err)
	}
	return &localaitools.ImportModelURIResponse{JobID: raw.ID}, nil
}

func (c *Client) DeleteModel(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, routeModelDelete(name), nil, nil)
}

func (c *Client) EditModelConfig(ctx context.Context, name string, patch map[string]any) error {
	return c.do(ctx, http.MethodPatch, routeModelConfigJSON(name), patch, nil)
}

func (c *Client) ReloadModels(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, routeModelsReload, nil, nil)
}

// ---- Backends ----

func (c *Client) ListBackends(ctx context.Context) ([]localaitools.Backend, error) {
	var raw []struct {
		Name      string `json:"name"`
		Installed bool   `json:"installed"`
	}
	if err := c.do(ctx, http.MethodGet, routeBackends, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]localaitools.Backend, 0, len(raw))
	for _, b := range raw {
		out = append(out, localaitools.Backend{Name: b.Name, Installed: b.Installed})
	}
	return out, nil
}

func (c *Client) ListKnownBackends(ctx context.Context) ([]schema.KnownBackend, error) {
	// /backends/known emits []schema.KnownBackend directly — pass through.
	var out []schema.KnownBackend
	if err := c.do(ctx, http.MethodGet, routeBackendsKnown, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) InstallBackend(ctx context.Context, req localaitools.InstallBackendRequest) (string, error) {
	body := map[string]any{"id": req.BackendName}
	if req.GalleryName != "" {
		body["id"] = req.GalleryName + "@" + req.BackendName
	}
	body["name"] = req.BackendName
	var resp struct {
		ID string `json:"uuid"`
	}
	if err := c.do(ctx, http.MethodPost, routeBackendsApply, body, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (c *Client) UpgradeBackend(ctx context.Context, name string) (string, error) {
	var resp struct {
		ID string `json:"uuid"`
	}
	if err := c.do(ctx, http.MethodPost, routeBackendUpgrade(name), nil, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

// ---- System ----

func (c *Client) SystemInfo(ctx context.Context) (*localaitools.SystemInfo, error) {
	var welcome struct {
		Version           string          `json:"Version"`
		LoadedModels      []any           `json:"LoadedModels"`
		InstalledBackends map[string]bool `json:"InstalledBackends"`
	}
	if err := c.do(ctx, http.MethodGet, routeWelcome, nil, &welcome); err != nil {
		return nil, err
	}
	info := &localaitools.SystemInfo{Version: welcome.Version}
	for name := range welcome.InstalledBackends {
		info.InstalledBackends = append(info.InstalledBackends, name)
	}
	// LoadedModels shape varies; we don't attempt to decode it client-side.
	return info, nil
}

func (c *Client) ListNodes(ctx context.Context) ([]localaitools.Node, error) {
	var raw []struct {
		ID          string `json:"id"`
		Address     string `json:"address"`
		HTTPAddress string `json:"http_address"`
		Status      string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, routeNodes, nil, &raw); err != nil {
		// Treat 404/disabled as "no nodes" to keep parity with single-process.
		if errors.Is(err, ErrHTTPNotFound) {
			return []localaitools.Node{}, nil
		}
		return nil, err
	}
	out := make([]localaitools.Node, 0, len(raw))
	for _, n := range raw {
		out = append(out, localaitools.Node{
			ID:          n.ID,
			Address:     n.Address,
			HTTPAddress: n.HTTPAddress,
			Healthy:     n.Status == "healthy",
		})
	}
	return out, nil
}

func (c *Client) VRAMEstimate(ctx context.Context, req localaitools.VRAMEstimateRequest) (*vram.EstimateResult, error) {
	body := map[string]any{"model": req.ModelName}
	if req.ContextSize > 0 {
		body["context_size"] = req.ContextSize
	}
	if req.GPULayers != 0 {
		body["gpu_layers"] = req.GPULayers
	}
	if req.KVQuantBits > 0 {
		body["kv_quant_bits"] = req.KVQuantBits
	}
	// /api/models/vram-estimate returns a wrapper carrying vram.EstimateResult
	// (size_bytes/size_display/vram_bytes/vram_display) plus context-note
	// fields. Decode directly into EstimateResult — the LLM gets the
	// pre-formatted display strings, identical to REST.
	var out vram.EstimateResult
	if err := c.do(ctx, http.MethodPost, routeVRAMEstimate, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- State ----

func (c *Client) ToggleModelState(ctx context.Context, name string, action modeladmin.Action) error {
	return c.do(ctx, http.MethodPut, routeToggleModelState(name, string(action)), nil, nil)
}

func (c *Client) ToggleModelPinned(ctx context.Context, name string, action modeladmin.Action) error {
	return c.do(ctx, http.MethodPut, routeToggleModelPinned(name, string(action)), nil, nil)
}

// ---- Branding ----

// brandingResponse mirrors the JSON shape emitted by GET /api/branding.
// We don't import the server-side type here so the MCP HTTP client stays
// independent of the localai endpoint package.
type brandingResponse struct {
	InstanceName      string `json:"instance_name"`
	InstanceTagline   string `json:"instance_tagline"`
	LogoURL           string `json:"logo_url"`
	LogoHorizontalURL string `json:"logo_horizontal_url"`
	FaviconURL        string `json:"favicon_url"`
}

func (c *Client) GetBranding(ctx context.Context) (*localaitools.Branding, error) {
	var raw brandingResponse
	if err := c.do(ctx, http.MethodGet, routeBranding, nil, &raw); err != nil {
		return nil, err
	}
	return (*localaitools.Branding)(&raw), nil
}

func (c *Client) SetBranding(ctx context.Context, req localaitools.SetBrandingRequest) (*localaitools.Branding, error) {
	// Text fields ride the existing /api/settings POST, which maps the
	// pointer fields onto RuntimeSettings.InstanceName / InstanceTagline.
	body := map[string]any{}
	if req.InstanceName != nil {
		body["instance_name"] = *req.InstanceName
	}
	if req.InstanceTagline != nil {
		body["instance_tagline"] = *req.InstanceTagline
	}
	if len(body) == 0 {
		return c.GetBranding(ctx)
	}
	if err := c.do(ctx, http.MethodPost, routeSettings, body, nil); err != nil {
		return nil, err
	}
	return c.GetBranding(ctx)
}

// ---- Usage / billing ----

func (c *Client) GetUsageStats(ctx context.Context, q localaitools.UsageStatsQuery) (*localaitools.UsageStats, error) {
	period := q.Period
	if period == "" {
		period = "month"
	}
	path := routeUsage
	if q.All {
		path = routeUsageAll
	}
	// Build query string. The /api/usage server expects these exact param
	// names; any change there must update both sides.
	qs := url.Values{}
	qs.Set("period", period)
	if q.UserID != "" && q.All {
		qs.Set("user_id", q.UserID)
	}
	if enc := qs.Encode(); enc != "" {
		path = path + "?" + enc
	}

	var raw struct {
		Viewer struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"viewer"`
		Totals struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
			RequestCount     int64 `json:"request_count"`
		} `json:"totals"`
		Usage []struct {
			Bucket           string `json:"bucket"`
			Model            string `json:"model"`
			UserID           string `json:"user_id"`
			UserName         string `json:"user_name"`
			PromptTokens     int64  `json:"prompt_tokens"`
			CompletionTokens int64  `json:"completion_tokens"`
			TotalTokens      int64  `json:"total_tokens"`
			RequestCount     int64  `json:"request_count"`
		} `json:"usage"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	out := &localaitools.UsageStats{
		Viewer: localaitools.UsageViewer{ID: raw.Viewer.ID, Name: raw.Viewer.Name, Role: raw.Viewer.Role},
		Period: period,
		Totals: localaitools.UsageTotals{
			PromptTokens:     raw.Totals.PromptTokens,
			CompletionTokens: raw.Totals.CompletionTokens,
			TotalTokens:      raw.Totals.TotalTokens,
			RequestCount:     raw.Totals.RequestCount,
		},
		Buckets: make([]localaitools.UsageBucket, 0, len(raw.Usage)),
	}
	for _, b := range raw.Usage {
		out.Buckets = append(out.Buckets, localaitools.UsageBucket{
			Bucket:           b.Bucket,
			Model:            b.Model,
			UserID:           b.UserID,
			UserName:         b.UserName,
			PromptTokens:     b.PromptTokens,
			CompletionTokens: b.CompletionTokens,
			TotalTokens:      b.TotalTokens,
			RequestCount:     b.RequestCount,
		})
	}
	return out, nil
}

// ---- PII filter ----

func (c *Client) ListPIIPatterns(ctx context.Context) ([]localaitools.PIIPattern, error) {
	var raw struct {
		Patterns []localaitools.PIIPattern `json:"patterns"`
	}
	if err := c.do(ctx, http.MethodGet, routePIIPatterns, nil, &raw); err != nil {
		return nil, err
	}
	return raw.Patterns, nil
}

func (c *Client) GetPIIEvents(ctx context.Context, q localaitools.PIIEventsQuery) ([]localaitools.PIIEvent, error) {
	qs := url.Values{}
	if q.CorrelationID != "" {
		qs.Set("correlation_id", q.CorrelationID)
	}
	if q.UserID != "" {
		qs.Set("user_id", q.UserID)
	}
	if q.PatternID != "" {
		qs.Set("pattern_id", q.PatternID)
	}
	// The MCP get_pii_events tool is PII-shaped; the events store is now
	// shared with proxy events that have no pattern_id/action. Scope to
	// kind=pii so the LLM-facing audit stays coherent.
	qs.Set("kind", "pii")
	if q.Limit > 0 {
		qs.Set("limit", fmt.Sprintf("%d", q.Limit))
	}
	path := routePIIEvents
	if enc := qs.Encode(); enc != "" {
		path = path + "?" + enc
	}

	var raw struct {
		Events []localaitools.PIIEvent `json:"events"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	return raw.Events, nil
}

func (c *Client) TestPIIRedaction(ctx context.Context, req localaitools.PIIRedactTestRequest) (*localaitools.PIIRedactTestResult, error) {
	var out localaitools.PIIRedactTestResult
	if err := c.do(ctx, http.MethodPost, routePIITest, map[string]string{"text": req.Text}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) SetPIIPatternAction(ctx context.Context, req localaitools.PIIPatternActionUpdate) error {
	if req.ID == "" {
		return fmt.Errorf("pattern id is required")
	}
	body := map[string]any{}
	if req.Action != "" {
		body["action"] = req.Action
	}
	if req.Disabled != nil {
		body["disabled"] = *req.Disabled
	}
	if len(body) == 0 {
		return fmt.Errorf("must specify action and/or disabled")
	}
	return c.do(ctx, http.MethodPut, routePIIPatternByID(req.ID), body, nil)
}

func (c *Client) PersistPIIPatterns(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, routePIIPatternsPersist, nil, nil)
}

func (c *Client) GetMiddlewareStatus(ctx context.Context) (*localaitools.MiddlewareStatus, error) {
	var out localaitools.MiddlewareStatus
	if err := c.do(ctx, http.MethodGet, routeMiddleware, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetRouterDecisions(ctx context.Context, q localaitools.RouterDecisionsQuery) ([]localaitools.RouterDecision, error) {
	qs := url.Values{}
	if q.CorrelationID != "" {
		qs.Set("correlation_id", q.CorrelationID)
	}
	if q.UserID != "" {
		qs.Set("user_id", q.UserID)
	}
	if q.RouterModel != "" {
		qs.Set("router_model", q.RouterModel)
	}
	if q.Limit > 0 {
		qs.Set("limit", fmt.Sprintf("%d", q.Limit))
	}
	path := routeRouterDecisions
	if enc := qs.Encode(); enc != "" {
		path = path + "?" + enc
	}
	var raw struct {
		Decisions []localaitools.RouterDecision `json:"decisions"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	return raw.Decisions, nil
}

// ---- helpers ----

func contains(haystack, lowerNeedle string) bool {
	return strings.Contains(strings.ToLower(haystack), lowerNeedle)
}

func containsTagsAny(tags []string, lowerNeedle string) bool {
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), lowerNeedle) {
			return true
		}
	}
	return false
}

func containsTagExact(tags []string, lowerNeedle string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, lowerNeedle) {
			return true
		}
	}
	return false
}
