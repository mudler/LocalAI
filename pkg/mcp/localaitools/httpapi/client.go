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
	"slices"
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
	// /api/models/capabilities lists every installed model (config-backed and
	// loose) with its backend and capability flags — the same data the old
	// GET / welcome JSON used to carry, on a route that does not redirect.
	// (The welcome endpoint was unregistered when the React UI moved to /app,
	// so GET / now 301s to the SPA and the no-redirect httpclient rejects it.)
	var resp struct {
		Data []struct {
			ID           string   `json:"id"`
			Backend      string   `json:"backend"`
			Capabilities []string `json:"capabilities"`
			Disabled     bool     `json:"disabled"`
			Pinned       bool     `json:"pinned"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, routeModelsCapabilities, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]localaitools.InstalledModel, 0, len(resp.Data))
	for _, m := range resp.Data {
		if capability != localaitools.CapabilityAny && !hasCapability(m.Capabilities, capability) {
			continue
		}
		out = append(out, localaitools.InstalledModel{
			Name:         m.ID,
			Backend:      m.Backend,
			Capabilities: capabilityLabels(m.Capabilities),
			Disabled:     m.Disabled,
			Pinned:       m.Pinned,
		})
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
	if req.Variant != "" {
		body["variant"] = req.Variant
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

func (c *Client) LoadModel(ctx context.Context, model string) ([]string, error) {
	// On a load failure the endpoint returns a non-2xx whose body (carrying the
	// per-sub-model failure detail) is folded into the HTTPError by c.do.
	var resp schema.ModelLoadResponse
	if err := c.do(ctx, http.MethodPost, routeBackendLoad, map[string]string{"model": model}, &resp); err != nil {
		return nil, err
	}
	return resp.Loaded, nil
}

// ---- Model aliases ----

// SetAlias is swap-first: it PATCHes the alias config (a deep-merge that
// validates the target and preserves any other fields), and only creates a
// fresh config when the PATCH reports the model doesn't exist yet. We prefer
// PATCH over POST /models/import for existing names because import rewrites
// the whole file, whereas PATCH gives a reliable 404 not-found signal
// (ErrHTTPNotFound) to branch on and never clobbers an existing config.
func (c *Client) SetAlias(ctx context.Context, name, target string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if target == "" {
		return errors.New("target is required")
	}
	err := c.do(ctx, http.MethodPatch, routeModelConfigJSON(name), map[string]any{"alias": target}, nil)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrHTTPNotFound) {
		return err
	}
	// No such config yet: create it. The import endpoint validates the alias
	// target server-side, same as the PATCH path.
	return c.do(ctx, http.MethodPost, routeModelImport, map[string]any{"name": name, "alias": target}, nil)
}

func (c *Client) ListAliases(ctx context.Context) ([]localaitools.AliasInfo, error) {
	// /api/aliases returns []{name,target} directly - pass it through.
	var out []localaitools.AliasInfo
	if err := c.do(ctx, http.MethodGet, routeAliases, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
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
	// Composed from live, non-redirecting routes — the old GET / welcome JSON
	// is gone (the React UI owns / now), so pull the pieces separately.
	var ver struct {
		Version string `json:"version"`
	}
	if err := c.do(ctx, http.MethodGet, routeVersion, nil, &ver); err != nil {
		return nil, err
	}
	info := &localaitools.SystemInfo{Version: ver.Version}

	var backends []struct {
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, routeBackends, nil, &backends); err == nil {
		for _, b := range backends {
			info.InstalledBackends = append(info.InstalledBackends, b.Name)
		}
	}
	slices.Sort(info.InstalledBackends)

	var sys struct {
		LoadedModels []struct {
			ID string `json:"id"`
		} `json:"loaded_models"`
	}
	if err := c.do(ctx, http.MethodGet, routeSystem, nil, &sys); err == nil {
		for _, m := range sys.LoadedModels {
			info.LoadedModels = append(info.LoadedModels, m.ID)
		}
	}
	return info, nil
}

// hasCapability reports whether any of the model's FLAG_* capability labels
// equals the requested capability (case-insensitive, whole-word).
func hasCapability(flags []string, want localaitools.Capability) bool {
	wantStr := strings.TrimPrefix(strings.ToUpper(string(want)), "FLAG_")
	for _, f := range flags {
		if strings.TrimPrefix(strings.ToUpper(f), "FLAG_") == wantStr {
			return true
		}
	}
	return false
}

// capabilityLabels trims the "FLAG_" prefix so the LLM sees "CHAT", "TTS", …
// matching the in-process client's output.
func capabilityLabels(flags []string) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, strings.TrimPrefix(f, "FLAG_"))
	}
	return out
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

func (c *Client) ListScheduling(ctx context.Context) ([]localaitools.ModelSchedulingConfig, error) {
	var out []localaitools.ModelSchedulingConfig
	if err := c.do(ctx, http.MethodGet, routeScheduling, nil, &out); err != nil {
		if errors.Is(err, ErrHTTPNotFound) {
			return []localaitools.ModelSchedulingConfig{}, nil
		}
		return nil, err
	}
	return out, nil
}

func (c *Client) GetScheduling(ctx context.Context, modelName string) (*localaitools.ModelSchedulingConfig, error) {
	if modelName == "" {
		return nil, errors.New("model_name is required")
	}
	var out localaitools.ModelSchedulingConfig
	if err := c.do(ctx, http.MethodGet, routeModelScheduling(modelName), nil, &out); err != nil {
		if errors.Is(err, ErrHTTPNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (c *Client) SetScheduling(ctx context.Context, req localaitools.SetSchedulingRequest) (*localaitools.ModelSchedulingConfig, error) {
	if req.ModelName == "" {
		return nil, errors.New("model_name is required")
	}
	var out localaitools.ModelSchedulingConfig
	if err := c.do(ctx, http.MethodPost, routeScheduling, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteScheduling(ctx context.Context, modelName string) error {
	if modelName == "" {
		return errors.New("model_name is required")
	}
	return c.do(ctx, http.MethodDelete, routeModelScheduling(modelName), nil, nil)
}

func (c *Client) SetNodeVRAMBudget(ctx context.Context, nodeID, budget string) error {
	// PUT with an empty value clears the override server-side (Task 9), so we
	// use PUT uniformly rather than switching to DELETE for the clear case.
	body := map[string]any{"value": budget}
	return c.do(ctx, http.MethodPut, routeNodeVRAMBudget(nodeID), body, nil)
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

// ---- Voice profile library ----

func (c *Client) ListVoiceProfiles(ctx context.Context) ([]localaitools.VoiceProfile, error) {
	var response struct {
		Data []localaitools.VoiceProfile `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, routeVoiceProfiles, nil, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

func (c *Client) CreateVoiceProfile(ctx context.Context, req localaitools.CreateVoiceProfileRequest) (*localaitools.VoiceProfile, error) {
	var profile localaitools.VoiceProfile
	if err := c.do(ctx, http.MethodPost, routeVoiceProfiles, req, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (c *Client) DeleteVoiceProfile(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("id is required")
	}
	return c.do(ctx, http.MethodDelete, routeVoiceProfileDelete(id), nil, nil)
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

func (c *Client) GetRouterCorpusStats(ctx context.Context, routerModel string) (*localaitools.RouterCorpusStats, error) {
	var out localaitools.RouterCorpusStats
	path := fmt.Sprintf("/api/router/%s/corpus/stats", url.PathEscape(routerModel))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) SeedRouterCorpus(ctx context.Context, req localaitools.RouterCorpusSeedRequest) (*localaitools.RouterCorpusSeedResult, error) {
	// The REST body carries entries only; the router rides in the path.
	body := struct {
		Entries []localaitools.RouterCorpusEntry `json:"entries"`
	}{Entries: req.Entries}
	var out localaitools.RouterCorpusSeedResult
	path := fmt.Sprintf("/api/router/%s/corpus", url.PathEscape(req.Router))
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ClearRouterCorpus(ctx context.Context, routerModel string) (*localaitools.RouterCorpusClearResult, error) {
	var out localaitools.RouterCorpusClearResult
	path := fmt.Sprintf("/api/router/%s/corpus", url.PathEscape(routerModel))
	if err := c.do(ctx, http.MethodDelete, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Failover chains ----

func (c *Client) ListFailoverChains(ctx context.Context) ([]localaitools.FailoverChainInfo, error) {
	var out struct {
		Chains []localaitools.FailoverChainInfo `json:"chains"`
	}
	if err := c.do(ctx, http.MethodGet, routeFailover, nil, &out); err != nil {
		return nil, err
	}
	return out.Chains, nil
}

func (c *Client) PinFailoverTarget(ctx context.Context, chain, target string) error {
	return c.do(ctx, http.MethodPost, routeFailover+"/"+url.PathEscape(chain)+"/pin", map[string]string{"target": target}, nil)
}

func (c *Client) CancelModelLoad(ctx context.Context, model, jobID string) (localaitools.LoadCancelResult, error) {
	var result localaitools.LoadCancelResult
	err := c.do(ctx, http.MethodPost, "/api/models/"+url.PathEscape(model)+"/load-cancel", map[string]string{"job_id": jobID}, &result)
	return result, err
}

func (c *Client) UnpinFailoverTarget(ctx context.Context, chain string) error {
	return c.do(ctx, http.MethodDelete, routeFailover+"/"+url.PathEscape(chain)+"/pin", nil, nil)
}
