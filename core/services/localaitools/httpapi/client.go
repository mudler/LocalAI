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
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/pkg/modelsearch"
	"github.com/mudler/LocalAI/pkg/vram"
)

// Client is a thin REST wrapper. It maps each LocalAIClient method to the
// matching admin endpoint. Errors from non-2xx responses include the body for
// the MCP layer to surface verbatim to the LLM.
type Client struct {
	BaseURL string
	APIKey  string

	HTTPClient *http.Client
	// InferenceHTTPClient sends the client tools' requests, which can run for
	// minutes. Nil: a client with a ten-minute timeout, made by New.
	InferenceHTTPClient *http.Client
	// Files reads the input files of the client tools and stores their results
	// on this machine. Nil: the tools that need a file report that no store is
	// configured.
	Files localaitools.FileStore
}

// New returns a Client targeting baseURL with an optional bearer token.
func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: httpclient.NewWithTimeout(60 * time.Second),

		InferenceHTTPClient: httpclient.NewWithTimeout(10 * time.Minute),
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

func (c *Client) SearchModels(ctx context.Context, req localaitools.ModelSearchRequest) (*localaitools.ModelSearchResponse, error) {
	v := url.Values{}
	v.Set("q", req.Query)
	if req.Providers != "" {
		v.Set("providers", req.Providers)
	}
	if req.Limit > 0 {
		v.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Format != "" {
		v.Set("format", req.Format)
	}
	if req.Sort != "" {
		v.Set("sort", req.Sort)
	}
	var out localaitools.ModelSearchResponse
	if err := c.do(ctx, http.MethodGet, routeModelsSearch+"?"+v.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) InspectRemoteModel(ctx context.Context, req localaitools.RemoteModelRequest) (*localaitools.RemoteModelInfo, error) {
	var files modelsearch.RepoFiles
	if err := c.do(ctx, http.MethodGet, routeModelsRemoteFiles+"?"+url.Values{"uri": {req.URI}}.Encode(), nil, &files); err != nil {
		return nil, err
	}
	info := &localaitools.RemoteModelInfo{RepoFiles: &files}
	if req.Estimate || req.File != "" {
		v := url.Values{"uri": {req.URI}}
		if req.File != "" {
			v.Set("file", req.File)
		}
		if req.Contexts != "" {
			v.Set("contexts", req.Contexts)
		}
		var est modelsearch.EstimateResult
		if err := c.do(ctx, http.MethodGet, routeModelsRemoteEstimate+"?"+v.Encode(), nil, &est); err != nil {
			return nil, err
		}
		info.Estimate = &est
	}
	return info, nil
}

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
	// Server expects preferences as a JSON object.
	prefs := map[string]string{}
	if req.BackendPreference != "" {
		prefs["backend"] = req.BackendPreference
	}
	if req.Quantization != "" {
		prefs["quantizations"] = req.Quantization
	}
	if len(prefs) > 0 {
		body["preferences"] = prefs
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

func (c *Client) ListBackendLibraryUpdates(ctx context.Context) ([]localaitools.BackendLibraryUpdate, error) {
	var resp struct {
		Updates []localaitools.BackendLibraryUpdate `json:"updates"`
	}
	if err := c.do(ctx, http.MethodGet, routeBackendsLibraryUpdates, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Updates == nil {
		resp.Updates = []localaitools.BackendLibraryUpdate{}
	}
	return resp.Updates, nil
}

func (c *Client) BackendUpdates(ctx context.Context) (*backendupdates.Snapshot, error) {
	var out backendupdates.Snapshot
	if err := c.do(ctx, http.MethodGet, routeBackendsUpdates, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CheckBackendUpdates(ctx context.Context) (*backendupdates.Snapshot, error) {
	var out backendupdates.Snapshot
	if err := c.do(ctx, http.MethodPost, routeBackendsUpdatesCheck, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
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

func (c *Client) InstallLibraryBackend(ctx context.Context, req localaitools.InstallLibraryBackendRequest) (*localaitools.InstallLibraryBackendResult, error) {
	body := map[string]any{"sourceId": req.SourceID, "name": req.Name, "version": req.Version}
	var resp struct {
		Status   string `json:"status"`
		Name     string `json:"name"`
		SourceID string `json:"sourceId"`
	}
	if err := c.do(ctx, http.MethodPost, routeBackendsLibraryInstall, body, &resp); err != nil {
		return nil, err
	}
	return &localaitools.InstallLibraryBackendResult{Status: resp.Status, Name: resp.Name, SourceID: resp.SourceID}, nil
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

// ---- Model updates ----

func (c *Client) ModelUpdates(ctx context.Context) (*modelupdates.Snapshot, error) {
	var out modelupdates.Snapshot
	if err := c.do(ctx, http.MethodGet, routeModelsUpdates, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CheckModelUpdates(ctx context.Context) (*modelupdates.Snapshot, error) {
	var out modelupdates.Snapshot
	if err := c.do(ctx, http.MethodPost, routeModelsUpdatesCheck, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpgradeModel(ctx context.Context, name string, configOnly bool, choices map[string]string) (string, error) {
	if name == "" {
		return "", errors.New("name is required")
	}
	var resp schema.ModelUpgradeResponse
	body := schema.ModelUpgradeRequest{ConfigOnly: configOnly, Choices: choices}
	if err := c.do(ctx, http.MethodPost, routeModelUpgrade(name), body, &resp); err != nil {
		return "", err
	}
	return resp.JobID, nil
}

func (c *Client) RetryModelUpgrade(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", errors.New("name is required")
	}
	var resp schema.ModelUpgradeResponse
	if err := c.do(ctx, http.MethodPost, routeModelPath(name, "upgrade/retry"), nil, &resp); err != nil {
		return "", err
	}
	return resp.JobID, nil
}

func (c *Client) RemoveOldModelVersion(ctx context.Context, name string) (*localaitools.ModelRemoveOldResult, error) {
	if name == "" {
		return nil, errors.New("name is required")
	}
	var resp schema.ModelRemoveOldResponse
	if err := c.do(ctx, http.MethodPost, routeModelPath(name, "remove-old"), nil, &resp); err != nil {
		return nil, err
	}
	return &localaitools.ModelRemoveOldResult{Name: resp.Name, Removed: resp.Removed, Kept: resp.Kept, Errors: resp.Errors}, nil
}

func (c *Client) SetModelHold(ctx context.Context, name string, hold bool) (*localaitools.ModelHoldResult, error) {
	if name == "" {
		return nil, errors.New("name is required")
	}
	var resp schema.ModelHoldResponse
	if err := c.do(ctx, http.MethodPut, routeModelPath(name, "hold"), schema.ModelHoldRequest{Hold: hold}, &resp); err != nil {
		return nil, err
	}
	return &localaitools.ModelHoldResult{Name: resp.Name, Hold: resp.Hold}, nil
}

func (c *Client) AdoptModel(ctx context.Context, name, galleryName, entry string) (*localaitools.ModelAdoptResult, error) {
	if name == "" {
		return nil, errors.New("name is required")
	}
	var resp schema.ModelAdoptResponse
	if err := c.do(ctx, http.MethodPost, routeModelPath(name, "adopt"), schema.ModelAdoptRequest{Gallery: galleryName, Entry: entry}, &resp); err != nil {
		return nil, err
	}
	return &localaitools.ModelAdoptResult{Name: resp.Name, Gallery: resp.Gallery, Entry: resp.Entry, FilesMatch: resp.FilesMatch}, nil
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
		ID          string                        `json:"id"`
		Address     string                        `json:"address"`
		HTTPAddress string                        `json:"http_address"`
		Status      string                        `json:"status"`
		Attached    string                        `json:"attached"`
		PeerID      string                        `json:"peer_id"`
		SplitServer bool                          `json:"split_server"`
		RouteState  []localaitools.NodeRouteState `json:"route_state"`
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
			Route:       localaitools.NodeRoute(strings.Split(n.Attached, ","), n.PeerID),
			PeerID:      n.PeerID,
			SplitServer: n.SplitServer,
			Routes:      n.RouteState,
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

func (c *Client) SetNodeSplitServer(ctx context.Context, nodeID string, on bool) (*localaitools.NodeSplitServer, error) {
	var out localaitools.NodeSplitServer
	if err := c.do(ctx, http.MethodPut, routeNodeSplitServer(nodeID), map[string]bool{"split_server": on}, &out); err != nil {
		return nil, err
	}
	out.NodeID = nodeID
	return &out, nil
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

// ---- Cluster carrier ----

// ---- Carrier weights ----

type carrierWeightsWire struct {
	CarrierWeights        localaitools.CarrierWeightValues `json:"carrier_weights"`
	CarrierWeightsDefault localaitools.CarrierWeightValues `json:"carrier_weights_default"`
}

func carrierWeightsFromWire(w carrierWeightsWire) *localaitools.CarrierWeightsInfo {
	cur, def := w.CarrierWeights, w.CarrierWeightsDefault
	return &localaitools.CarrierWeightsInfo{Distributed: true, Weights: &cur, Defaults: &def}
}

// GetCarrierWeights reads GET /api/cluster/settings. A frontend that is not
// distributed answers 503, which is reported as distributed=false.
func (c *Client) GetCarrierWeights(ctx context.Context) (*localaitools.CarrierWeightsInfo, error) {
	var raw carrierWeightsWire
	if err := c.do(ctx, http.MethodGet, routeClusterSettings, nil, &raw); err != nil {
		var he *HTTPError
		if errors.As(err, &he) && he.StatusCode == http.StatusServiceUnavailable {
			return &localaitools.CarrierWeightsInfo{Distributed: false}, nil
		}
		return nil, err
	}
	return carrierWeightsFromWire(raw), nil
}

// SetCarrierWeights sends PUT /api/cluster/settings with carrier_weights.
func (c *Client) SetCarrierWeights(ctx context.Context, req localaitools.SetCarrierWeightsRequest) (*localaitools.CarrierWeightsInfo, error) {
	weights := map[string]int{}
	if req.NATS != nil {
		weights["nats"] = *req.NATS
	}
	if req.WebSocket != nil {
		weights["websocket"] = *req.WebSocket
	}
	if req.P2P != nil {
		weights["p2p"] = *req.P2P
	}
	if len(weights) == 0 {
		return nil, errors.New("give at least one of nats, websocket or p2p")
	}
	var raw struct {
		Settings carrierWeightsWire `json:"settings"`
	}
	if err := c.do(ctx, http.MethodPut, routeClusterSettings, map[string]any{"carrier_weights": weights}, &raw); err != nil {
		return nil, err
	}
	return carrierWeightsFromWire(raw.Settings), nil
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

// ---- P2P ----

// p2pStatusWire, p2pDeviceWire and p2pRelayWire are the parts of the REST
// answers the tools show.
type p2pStatusWire struct {
	Mode           string   `json:"mode"`
	Running        bool     `json:"running"`
	Error          string   `json:"error"`
	PeerID         string   `json:"peer_id"`
	Addrs          []string `json:"addrs"`
	Reachability   string   `json:"reachability"`
	PendingInvites int      `json:"pending_invites"`
	Devices        int      `json:"devices"`
	RelayService   struct {
		Running bool `json:"running"`
	} `json:"relay_service"`
}

type p2pDeviceWire struct {
	localaitools.P2PDeviceInfo
	Path struct {
		Kind       string `json:"kind"`
		RelayLabel string `json:"relay_label"`
	} `json:"path"`
}

func (c *Client) GetP2PStatus(ctx context.Context) (*localaitools.P2PStatusInfo, error) {
	var w p2pStatusWire
	if err := c.do(ctx, http.MethodGet, routeP2PStatus, nil, &w); err != nil {
		return nil, err
	}
	return &localaitools.P2PStatusInfo{
		Mode: w.Mode, Running: w.Running, Error: w.Error, PeerID: w.PeerID, Addrs: w.Addrs,
		Reachability: w.Reachability, RelayService: w.RelayService.Running,
		PendingInvites: w.PendingInvites, Devices: w.Devices,
	}, nil
}

func (c *Client) ListP2PDevices(ctx context.Context) ([]localaitools.P2PDeviceInfo, error) {
	var out struct {
		Devices []p2pDeviceWire `json:"devices"`
	}
	if err := c.do(ctx, http.MethodGet, routeP2PDevices, nil, &out); err != nil {
		return nil, err
	}
	devs := make([]localaitools.P2PDeviceInfo, 0, len(out.Devices))
	for _, d := range out.Devices {
		info := d.P2PDeviceInfo
		info.Path, info.RelayLabel = d.Path.Kind, d.Path.RelayLabel
		devs = append(devs, info)
	}
	return devs, nil
}

func (c *Client) ListP2PInvites(ctx context.Context) ([]localaitools.P2PInviteInfo, error) {
	var out struct {
		Invites []localaitools.P2PInviteInfo `json:"invites"`
	}
	if err := c.do(ctx, http.MethodGet, routeP2PInvites, nil, &out); err != nil {
		return nil, err
	}
	return out.Invites, nil
}

func (c *Client) ListP2PRelays(ctx context.Context) ([]localaitools.P2PRelayInfo, error) {
	var out struct {
		Relays []localaitools.P2PRelayInfo `json:"relays"`
	}
	if err := c.do(ctx, http.MethodGet, routeP2PRelays, nil, &out); err != nil {
		return nil, err
	}
	return out.Relays, nil
}

func (c *Client) GetP2PRelayLimits(ctx context.Context, id string) (*localaitools.P2PRelayLimitsInfo, error) {
	var out struct {
		Limits    localaitools.P2PRelayLimitsInfo `json:"limits"`
		Ceiling   localaitools.P2PRelayCeiling    `json:"ceiling"`
		CanChange bool                            `json:"can_change"`
		Restarted bool                            `json:"restarted"`
	}
	if err := c.do(ctx, http.MethodGet, routeP2PRelays+"/"+url.PathEscape(id)+"/limits", nil, &out); err != nil {
		return nil, err
	}
	info := out.Limits
	info.Ceiling, info.CanChange, info.Restarted = out.Ceiling, out.CanChange, out.Restarted
	return &info, nil
}

func (c *Client) SetP2PRelayLimits(ctx context.Context, id string, req localaitools.SetP2PRelayLimitsRequest) (*localaitools.P2PRelayLimitsInfo, error) {
	var out struct {
		Limits    localaitools.P2PRelayLimitsInfo `json:"limits"`
		Ceiling   localaitools.P2PRelayCeiling    `json:"ceiling"`
		CanChange bool                            `json:"can_change"`
		Restarted bool                            `json:"restarted"`
	}
	body := struct {
		CircuitBytes   *int64 `json:"circuit_bytes,omitempty"`
		CircuitSeconds *int64 `json:"circuit_seconds,omitempty"`
		RateBPS        *int64 `json:"rate_bps,omitempty"`
	}{req.CircuitBytes, req.CircuitSeconds, req.RateBPS}
	if err := c.do(ctx, http.MethodPut, routeP2PRelays+"/"+url.PathEscape(id)+"/limits", body, &out); err != nil {
		return nil, err
	}
	info := out.Limits
	info.Ceiling, info.CanChange, info.Restarted = out.Ceiling, out.CanChange, out.Restarted
	return &info, nil
}

func (c *Client) RevokeP2PDevice(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, routeP2PDevices+"/"+url.PathEscape(id), nil, nil)
}

func (c *Client) CancelP2PInvite(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, routeP2PInvites+"/"+url.PathEscape(id), nil, nil)
}

// p2pSplitWire is the part of a model-split worker the tools show.
type p2pSplitWire struct {
	localaitools.P2PSplitWorkerInfo
	Path struct {
		Kind       string `json:"kind"`
		RelayLabel string `json:"relay_label"`
	} `json:"path"`
	NodeRelayWire *struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	} `json:"node_relay"`
}

func (w p2pSplitWire) info() localaitools.P2PSplitWorkerInfo {
	info := w.P2PSplitWorkerInfo
	info.Path, info.RelayLabel = w.Path.Kind, w.Path.RelayLabel
	if w.NodeRelayWire != nil {
		info.NodeRelay, info.NodeRelayReason = w.NodeRelayWire.State, w.NodeRelayWire.Reason
	}
	return info
}

func (c *Client) ListP2PSplitWorkers(ctx context.Context) ([]localaitools.P2PSplitWorkerInfo, error) {
	var out struct {
		Workers []p2pSplitWire `json:"workers"`
	}
	if err := c.do(ctx, http.MethodGet, routeP2PSplitWorkers, nil, &out); err != nil {
		return nil, err
	}
	ws := make([]localaitools.P2PSplitWorkerInfo, 0, len(out.Workers))
	for _, w := range out.Workers {
		ws = append(ws, w.info())
	}
	return ws, nil
}

func (c *Client) SetP2PSplitSlowPath(ctx context.Context, id string, allow bool) (*localaitools.P2PSplitWorkerInfo, error) {
	var w p2pSplitWire
	if err := c.do(ctx, http.MethodPatch, routeP2PSplitWorkers+"/"+url.PathEscape(id), map[string]bool{"allow_slow_path": allow}, &w); err != nil {
		return nil, err
	}
	info := w.info()
	return &info, nil
}

func (c *Client) SetP2PSplitNode(ctx context.Context, id, nodeID string) (*localaitools.P2PSplitWorkerInfo, error) {
	var w p2pSplitWire
	if err := c.do(ctx, http.MethodPatch, routeP2PSplitWorkers+"/"+url.PathEscape(id), map[string]string{"node_id": nodeID}, &w); err != nil {
		return nil, err
	}
	info := w.info()
	return &info, nil
}

// ---- Shared folders ----

func (c *Client) ListSharedFolders(ctx context.Context) ([]localaitools.SharedFolder, error) {
	var out struct {
		Folders []localaitools.SharedFolder `json:"folders"`
	}
	if err := c.do(ctx, http.MethodGet, routeSharedFolders, nil, &out); err != nil {
		return nil, err
	}
	return out.Folders, nil
}

func (c *Client) CreateSharedFolder(ctx context.Context, name string) (*localaitools.SharedFolder, error) {
	var out localaitools.SharedFolder
	if err := c.do(ctx, http.MethodPost, routeSharedFolders, map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RenameSharedFolder(ctx context.Context, id int64, name string) (*localaitools.SharedFolder, error) {
	var out localaitools.SharedFolder
	if err := c.do(ctx, http.MethodPatch, routeSharedFolder(id), map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteSharedFolder(ctx context.Context, id int64) (*localaitools.SharedFolderRemoved, error) {
	var out localaitools.SharedFolderRemoved
	if err := c.do(ctx, http.MethodDelete, routeSharedFolder(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RestoreSharedFolder(ctx context.Context, rev int64) (*localaitools.SharedFolder, error) {
	var out localaitools.SharedFolder
	if err := c.do(ctx, http.MethodPost, routeSharedFolderRestore, map[string]int64{"rev": rev}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) SetSharedFolderMember(ctx context.Context, id int64, member, mode string) (*localaitools.SharedFolder, error) {
	var out localaitools.SharedFolder
	if err := c.do(ctx, http.MethodPut, routeSharedFolderMember(id, member), map[string]string{"mode": mode}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RemoveSharedFolderMember(ctx context.Context, id int64, member string) (*localaitools.SharedFolder, error) {
	var out localaitools.SharedFolder
	if err := c.do(ctx, http.MethodDelete, routeSharedFolderMember(id, member), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListSharedFolderEvents(ctx context.Context, limit int) ([]localaitools.SharedFolderEvent, error) {
	var out struct {
		Events []localaitools.SharedFolderEvent `json:"events"`
	}
	if err := c.do(ctx, http.MethodGet, routeSharedFolderEvents+"?limit="+strconv.Itoa(limit), nil, &out); err != nil {
		return nil, err
	}
	return out.Events, nil
}

func (c *Client) ListChatTools(ctx context.Context) (localaitools.ChatBuiltinToolList, error) {
	var out localaitools.ChatBuiltinToolList
	if err := c.do(ctx, http.MethodGet, routeChatTools, nil, &out); err != nil {
		return localaitools.ChatBuiltinToolList{}, err
	}
	return out, nil
}

func (c *Client) ListHostExecEvents(ctx context.Context, limit int) ([]localaitools.HostExecEvent, error) {
	var out struct {
		Events []localaitools.HostExecEvent `json:"events"`
	}
	if err := c.do(ctx, http.MethodGet, routeHostExecEvents+"?limit="+strconv.Itoa(limit), nil, &out); err != nil {
		return nil, err
	}
	return out.Events, nil
}

func (c *Client) GetChatEgress(ctx context.Context) (*localaitools.ChatEgress, error) {
	var out localaitools.ChatEgress
	if err := c.do(ctx, http.MethodGet, routeChatEgress, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListEgressEvents(ctx context.Context, q localaitools.EgressEventsQuery) ([]localaitools.EgressEvent, error) {
	v := url.Values{}
	v.Set("limit", strconv.Itoa(q.Limit))
	for k, x := range map[string]string{"user": q.User, "chat": q.Chat, "verdict": q.Verdict, "host": q.Host} {
		if x != "" {
			v.Set(k, x)
		}
	}
	var out struct {
		Events []localaitools.EgressEvent `json:"events"`
	}
	if err := c.do(ctx, http.MethodGet, routeEgressEvents+"?"+v.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.Events, nil
}

func (c *Client) SetChatEgressCeiling(ctx context.Context, ceiling string) (*localaitools.ChatEgress, error) {
	if err := c.do(ctx, http.MethodPost, routeSettings, map[string]any{"chats_egress_ceiling": ceiling}, nil); err != nil {
		return nil, err
	}
	return c.GetChatEgress(ctx)
}

// ---- Federation ----

// The REST answers of the federation routes have the field names of the
// DTOs, so they decode into them directly.

func (c *Client) GetFederation(ctx context.Context) (*localaitools.FederationInfo, error) {
	var out localaitools.FederationInfo
	if err := c.do(ctx, http.MethodGet, routeFederation, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ManageFederationGroup(ctx context.Context, req localaitools.FederationGroupAction) (*localaitools.FederationGroupInfo, error) {
	group := routeFederationGroups + "/" + url.PathEscape(req.Name)
	var (
		method, path string
		body         any
	)
	switch req.Action {
	case localaitools.FederationActionCreate:
		method, path = http.MethodPost, routeFederationGroups
		body = map[string]any{"name": req.Name, "policy": req.Policy, "members": req.Members}
	case localaitools.FederationActionUpdate:
		patch := map[string]any{}
		if req.Policy != "" {
			patch["policy"] = req.Policy
		}
		if req.Members != nil {
			patch["members"] = req.Members
		}
		method, path, body = http.MethodPatch, group, patch
	case localaitools.FederationActionDelete:
		return nil, c.do(ctx, http.MethodDelete, group, nil, nil)
	case localaitools.FederationActionDrain, localaitools.FederationActionUndrain:
		method, path = http.MethodPost, group+"/drain"
		body = map[string]any{"model": req.Model, "drain": req.Action == localaitools.FederationActionDrain}
	default:
		return nil, fmt.Errorf("unknown federation action %q", req.Action)
	}
	var out localaitools.FederationGroupInfo
	if err := c.do(ctx, method, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
