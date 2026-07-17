// Package inproc provides an in-process LocalAIClient that calls LocalAI
// services directly. Used by the chat handler when a chat session opts into
// the LocalAI Assistant modality, avoiding an HTTP loopback to the same
// process and the synthetic admin-credential dance that would entail.
package inproc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/gallery/importers"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
	"github.com/mudler/LocalAI/internal"
	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/LocalAI/pkg/vram"
	"gopkg.in/yaml.v3"
)

// Client implements localaitools.LocalAIClient by calling LocalAI services
// directly. It is intentionally a thin shim — distribution and persistence
// concerns are handled by the underlying services (GalleryService is already
// distributed-aware, ModelConfigLoader manages on-disk YAML, etc.), so this
// layer just translates between MCP DTOs and service signatures.
type Client struct {
	AppConfig     *config.ApplicationConfig
	SystemState   *system.SystemState
	ConfigLoader  *config.ModelConfigLoader
	ModelLoader   *model.ModelLoader
	Gallery       *galleryop.GalleryService
	VoiceProfiles *voiceprofile.Store

	// StatsRecorder and FallbackUser are optional — they back the
	// get_usage_stats tool. nil StatsRecorder makes the tool return an
	// "unavailable" error, which keeps the assistant responsive on
	// deployments that ran with --disable-stats or where startup wired
	// the inproc client before stats were ready.
	StatsRecorder *billing.Recorder
	FallbackUser  *auth.User

	// PIIRedactor and PIIEvents back the list_pii_patterns,
	// get_pii_events, and test_pii_redaction tools. nil values cause
	// the tools to return a "filter disabled" error.
	PIIRedactor *pii.Redactor
	PIIEvents   pii.EventStore

	// RouterDecisions backs the get_router_decisions tool. nil makes
	// the tool return an empty list — same shape the REST endpoint
	// returns when stats are disabled.
	RouterDecisions router.DecisionStore

	modelAdmin *modeladmin.ConfigService
}

// New builds a Client wired to the given services. All fields are required
// except ModelLoader (used only for SystemInfo's loaded-models report and
// best-effort ShutdownModel calls during config edits) and the stats
// fields (StatsRecorder, FallbackUser) which gate get_usage_stats.
func New(appConfig *config.ApplicationConfig, systemState *system.SystemState, cl *config.ModelConfigLoader, ml *model.ModelLoader, gs *galleryop.GalleryService) *Client {
	return &Client{
		AppConfig:     appConfig,
		SystemState:   systemState,
		ConfigLoader:  cl,
		ModelLoader:   ml,
		Gallery:       gs,
		VoiceProfiles: voiceprofile.NewStore(appConfig.DataPath),
		modelAdmin:    modeladmin.NewConfigService(cl, appConfig),
	}
}

// Compile-time assertion that *Client satisfies localaitools.LocalAIClient.
var _ localaitools.LocalAIClient = (*Client)(nil)

// ---- Models / gallery (read) ----

func (c *Client) GallerySearch(_ context.Context, q localaitools.GallerySearchQuery) ([]gallery.Metadata, error) {
	galleries := c.AppConfig.Galleries
	if q.Gallery != "" {
		galleries = filterGalleries(galleries, q.Gallery)
	}
	models, err := gallery.AvailableGalleryModels(galleries, c.SystemState)
	if err != nil {
		return nil, fmt.Errorf("list gallery models: %w", err)
	}

	if q.Query != "" {
		models = models.Search(q.Query)
	}
	if q.Tag != "" {
		models = models.FilterByTag(q.Tag)
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}

	// Surface gallery.Metadata directly — same wire shape as gallery.AvailableGalleryModels
	// returns and the same shape REST /models/available emits, so REST and MCP stay aligned.
	out := make([]gallery.Metadata, 0, min(len(models), limit))
	for i, m := range models {
		if i >= limit {
			break
		}
		out = append(out, m.Metadata)
	}
	return out, nil
}

func (c *Client) ListInstalledModels(_ context.Context, capability localaitools.Capability) ([]localaitools.InstalledModel, error) {
	wantFlag, hasFlag := capabilityToFlag(capability)
	configs := c.ConfigLoader.GetModelConfigsByFilter(func(_ string, m *config.ModelConfig) bool {
		if !hasFlag {
			return true
		}
		return m.HasUsecases(wantFlag)
	})

	out := make([]localaitools.InstalledModel, 0, len(configs))
	for _, m := range configs {
		out = append(out, localaitools.InstalledModel{
			Name:         m.Name,
			Backend:      m.Backend,
			Capabilities: capabilityFlagsOf(&m),
		})
	}
	return out, nil
}

func (c *Client) ListGalleries(_ context.Context) ([]config.Gallery, error) {
	// AppConfig.Galleries is already []config.Gallery; the JSON shape
	// matches what REST /models/galleries emits.
	return c.AppConfig.Galleries, nil
}

func (c *Client) GetJobStatus(_ context.Context, jobID string) (*localaitools.JobStatus, error) {
	if jobID == "" {
		return nil, errors.New("job id is required")
	}
	st := c.Gallery.GetStatus(jobID)
	if st == nil {
		return nil, nil
	}
	out := &localaitools.JobStatus{
		ID:                 jobID,
		Processed:          st.Processed,
		Cancelled:          st.Cancelled,
		Progress:           st.Progress,
		TotalFileSize:      st.TotalFileSize,
		DownloadedFileSize: st.DownloadedFileSize,
		Message:            st.Message,
	}
	if st.Error != nil {
		out.ErrorMessage = st.Error.Error()
	}
	return out, nil
}

func (c *Client) GetModelConfig(ctx context.Context, name string) (*localaitools.ModelConfigView, error) {
	view, err := c.modelAdmin.GetConfig(ctx, name)
	if err != nil {
		return nil, err
	}
	return &localaitools.ModelConfigView{Name: view.Name, YAML: view.YAML, JSON: view.JSON}, nil
}

// ---- Models / gallery (write) ----

func (c *Client) InstallModel(ctx context.Context, req localaitools.InstallModelRequest) (string, error) {
	if req.ModelName == "" {
		return "", errors.New("model_name is required")
	}
	id, err := uuid.NewUUID()
	if err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	galleries := c.AppConfig.Galleries
	if req.GalleryName != "" {
		galleries = filterGalleries(galleries, req.GalleryName)
	}
	op := galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
		ID:                 id.String(),
		GalleryElementName: req.ModelName,
		Req: gallery.GalleryModel{
			Metadata: gallery.Metadata{Name: req.ModelName},
		},
		Galleries:        galleries,
		BackendGalleries: c.AppConfig.BackendGalleries,
	}
	if err := sendModelOp(ctx, c.Gallery.ModelGalleryChannel, op); err != nil {
		return "", err
	}
	return id.String(), nil
}

func (c *Client) ImportModelURI(ctx context.Context, req localaitools.ImportModelURIRequest) (*localaitools.ImportModelURIResponse, error) {
	if req.URI == "" {
		return nil, errors.New("uri is required")
	}
	// Build the preferences JSON expected by importers.DiscoverModelConfig.
	// Today only `backend` is meaningful; future fields can be added without
	// changing the MCP DTO.
	var prefs json.RawMessage
	if req.BackendPreference != "" {
		raw, err := json.Marshal(map[string]string{"backend": req.BackendPreference})
		if err != nil {
			return nil, fmt.Errorf("marshal preferences: %w", err)
		}
		prefs = raw
	}

	modelConfig, err := importers.DiscoverModelConfig(req.URI, prefs)
	if err != nil {
		var amb *importers.AmbiguousImportError
		if errors.As(err, &amb) {
			candidates := amb.Candidates
			if candidates == nil {
				candidates = []string{}
			}
			return &localaitools.ImportModelURIResponse{
				AmbiguousBackend:  true,
				Modality:          amb.Modality,
				BackendCandidates: candidates,
				Hint:              "call import_model_uri again with backend_preference set to one of backend_candidates",
			}, nil
		}
		if errors.Is(err, importers.ErrAmbiguousImport) {
			return &localaitools.ImportModelURIResponse{
				AmbiguousBackend:  true,
				BackendCandidates: []string{},
				Hint:              "call import_model_uri again with backend_preference set",
			}, nil
		}
		return nil, fmt.Errorf("discover model config: %w", err)
	}

	id, err := uuid.NewUUID()
	if err != nil {
		return nil, fmt.Errorf("generate job id: %w", err)
	}
	galleryID := req.URI
	if modelConfig.Name != "" {
		galleryID = modelConfig.Name
	}
	overrides := req.Overrides
	if overrides == nil {
		overrides = map[string]any{}
	}
	op := galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
		Req:                gallery.GalleryModel{Overrides: overrides},
		ID:                 id.String(),
		GalleryElementName: galleryID,
		GalleryElement:     &modelConfig,
		BackendGalleries:   c.AppConfig.BackendGalleries,
	}
	if err := sendModelOp(ctx, c.Gallery.ModelGalleryChannel, op); err != nil {
		return nil, err
	}
	return &localaitools.ImportModelURIResponse{
		JobID:               id.String(),
		DiscoveredModelName: modelConfig.Name,
	}, nil
}

func (c *Client) DeleteModel(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	op := galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
		Delete:             true,
		GalleryElementName: name,
	}
	if err := sendModelOp(ctx, c.Gallery.ModelGalleryChannel, op); err != nil {
		return err
	}
	c.ConfigLoader.RemoveModelConfig(name)
	return nil
}

func (c *Client) EditModelConfig(ctx context.Context, name string, patch map[string]any) error {
	_, err := c.modelAdmin.PatchConfig(ctx, name, patch)
	return err
}

func (c *Client) ReloadModels(_ context.Context) error {
	if c.SystemState == nil {
		return errors.New("system state not available")
	}
	return c.ConfigLoader.LoadModelConfigsFromPath(c.SystemState.Model.ModelsPath)
}

func (c *Client) LoadModel(ctx context.Context, model string) ([]string, error) {
	if c.ConfigLoader == nil || c.ModelLoader == nil {
		return nil, errors.New("model loader not available")
	}
	// Reuse the same preload path the REST /backend/load endpoint uses, so a
	// pipeline model loads all its sub-models and the behaviour stays identical
	// across the in-process and HTTP clients.
	return backend.PreloadModelByName(ctx, c.ConfigLoader, c.ModelLoader, c.AppConfig, model)
}

// ---- Model aliases ----

// SetAlias is swap-first to match the httpapi client: PatchConfig swaps an
// existing alias's target (validating it and preserving other fields) and
// returns ErrNotFound when the config doesn't exist yet, which is the signal
// to create it. createAlias mirrors the create path of ImportModelEndpoint.
func (c *Client) SetAlias(ctx context.Context, name, target string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if target == "" {
		return errors.New("target is required")
	}
	_, err := c.modelAdmin.PatchConfig(ctx, name, map[string]any{"alias": target})
	if err == nil {
		return nil
	}
	if !errors.Is(err, modeladmin.ErrNotFound) {
		return err
	}
	return c.createAlias(name, target)
}

// createAlias writes a fresh `{name, alias}` config to disk and reloads,
// mirroring localai.ImportModelEndpoint's create path: validate, validate the
// alias target, verify the path is trusted, write, reload, best-effort preload.
func (c *Client) createAlias(name, target string) error {
	if c.SystemState == nil {
		return errors.New("system state not available")
	}
	cfg := config.ModelConfig{Name: name, Alias: target}
	if valid, vErr := cfg.Validate(); !valid {
		if vErr != nil {
			return vErr
		}
		return errors.New("invalid alias configuration")
	}
	if err := c.ConfigLoader.ValidateAliasTarget(&cfg); err != nil {
		return err
	}
	modelsPath := c.SystemState.Model.ModelsPath
	if err := utils.VerifyPath(name+".yaml", modelsPath); err != nil {
		return fmt.Errorf("model path not trusted: %w", err)
	}
	// Marshal only the user-provided fields (not the full struct with Go
	// zero values), matching what the import endpoint persists for an alias.
	yamlData, err := yaml.Marshal(map[string]any{"name": name, "alias": target})
	if err != nil {
		return fmt.Errorf("marshal alias config: %w", err)
	}
	// 0600: the LocalAI process is the sole reader/writer of model configs,
	// and a tighter mode keeps the gosec G306 scan clean for this new write.
	if err := os.WriteFile(filepath.Join(modelsPath, name+".yaml"), yamlData, 0600); err != nil {
		return fmt.Errorf("write alias config: %w", err)
	}
	if err := c.ConfigLoader.LoadModelConfigsFromPath(modelsPath, c.AppConfig.ToConfigLoaderOptions()...); err != nil {
		return fmt.Errorf("reload configs: %w", err)
	}
	// Preload is best-effort - a failure here doesn't undo the create.
	_ = c.ConfigLoader.Preload(modelsPath)
	return nil
}

func (c *Client) ListAliases(_ context.Context) ([]localaitools.AliasInfo, error) {
	// Mirror localai.ListAliasesEndpoint: every config whose Alias is set.
	out := []localaitools.AliasInfo{}
	for _, cfg := range c.ConfigLoader.GetAllModelsConfigs() {
		if cfg.IsAlias() {
			out = append(out, localaitools.AliasInfo{Name: cfg.Name, Target: cfg.Alias})
		}
	}
	return out, nil
}

// ---- Backends ----

func (c *Client) ListBackends(_ context.Context) ([]localaitools.Backend, error) {
	systemBackends, err := c.Gallery.ListBackends()
	if err != nil {
		return nil, fmt.Errorf("list backends: %w", err)
	}
	out := make([]localaitools.Backend, 0, len(systemBackends))
	for name := range systemBackends {
		out = append(out, localaitools.Backend{Name: name, Installed: true})
	}
	return out, nil
}

func (c *Client) ListKnownBackends(_ context.Context) ([]schema.KnownBackend, error) {
	available, err := gallery.AvailableBackends(c.AppConfig.BackendGalleries, c.SystemState)
	if err != nil {
		return nil, fmt.Errorf("list known backends: %w", err)
	}
	// Match the wire shape of REST /backends/known so the tool output is
	// identical regardless of which client served it.
	out := make([]schema.KnownBackend, 0, len(available))
	for _, b := range available {
		out = append(out, schema.KnownBackend{
			Name:        b.GetName(),
			Description: b.GetDescription(),
			Installed:   b.GetInstalled(),
		})
	}
	return out, nil
}

func (c *Client) InstallBackend(ctx context.Context, req localaitools.InstallBackendRequest) (string, error) {
	if req.BackendName == "" {
		return "", errors.New("backend_name is required")
	}
	id, err := uuid.NewUUID()
	if err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	galleries := c.AppConfig.BackendGalleries
	if req.GalleryName != "" {
		galleries = filterGalleries(galleries, req.GalleryName)
	}
	op := galleryop.ManagementOp[gallery.GalleryBackend, any]{
		ID:                 id.String(),
		GalleryElementName: req.BackendName,
		Galleries:          galleries,
	}
	if err := sendBackendOp(ctx, c.Gallery.BackendGalleryChannel, op); err != nil {
		return "", err
	}
	return id.String(), nil
}

func (c *Client) UpgradeBackend(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", errors.New("name is required")
	}
	id, err := uuid.NewUUID()
	if err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	op := galleryop.ManagementOp[gallery.GalleryBackend, any]{
		ID:                 id.String(),
		GalleryElementName: name,
		Galleries:          c.AppConfig.BackendGalleries,
		Upgrade:            true,
	}
	if err := sendBackendOp(ctx, c.Gallery.BackendGalleryChannel, op); err != nil {
		return "", err
	}
	return id.String(), nil
}

// ---- System ----

func (c *Client) SystemInfo(_ context.Context) (*localaitools.SystemInfo, error) {
	info := &localaitools.SystemInfo{
		Version:     internal.PrintableVersion(),
		Distributed: c.AppConfig != nil && c.AppConfig.Distributed.Enabled,
	}
	if c.SystemState != nil {
		info.BackendsPath = c.SystemState.Backend.BackendsPath
		info.ModelsPath = c.SystemState.Model.ModelsPath
	}
	if c.ModelLoader != nil {
		for _, m := range c.ModelLoader.ListLoadedModels() {
			info.LoadedModels = append(info.LoadedModels, m.ID)
		}
	}
	if c.Gallery != nil {
		if backends, err := c.Gallery.ListBackends(); err == nil {
			for name := range backends {
				info.InstalledBackends = append(info.InstalledBackends, name)
			}
		}
	}
	return info, nil
}

func (c *Client) ListNodes(_ context.Context) ([]localaitools.Node, error) {
	// Node-registry wiring is the responsibility of the Application layer; an
	// empty list is the right answer in single-process mode and a sensible
	// stub until the Application plumbs the registry into this client.
	return []localaitools.Node{}, nil
}

func (c *Client) SetNodeVRAMBudget(_ context.Context, _, _ string) error {
	// The node registry is a distributed-mode concern owned by the Application
	// layer and is not wired into the in-process client (which also returns an
	// empty node list from ListNodes). Report it as unavailable rather than
	// silently succeeding so the assistant tells the admin the truth.
	return errors.New("per-node VRAM budgets are only available in distributed mode")
}

func (c *Client) VRAMEstimate(ctx context.Context, req localaitools.VRAMEstimateRequest) (*vram.EstimateResult, error) {
	resp, err := modeladmin.EstimateVRAM(ctx, modeladmin.VRAMRequest{
		Model:       req.ModelName,
		ContextSize: uint32(req.ContextSize),
		GPULayers:   req.GPULayers,
		KVQuantBits: req.KVQuantBits,
	}, c.ConfigLoader, c.SystemState)
	if err != nil {
		return nil, err
	}
	// Forward vram.EstimateResult unchanged so the LLM sees the same
	// shape (size_bytes / size_display / vram_bytes / vram_display) that
	// REST /api/models/vram-estimate returns.
	return &resp.EstimateResult, nil
}

// ---- State ----

func (c *Client) ToggleModelState(ctx context.Context, name string, action modeladmin.Action) error {
	_, err := c.modelAdmin.ToggleState(ctx, name, action, c.ModelLoader)
	return err
}

func (c *Client) ToggleModelPinned(ctx context.Context, name string, action modeladmin.Action) error {
	// No syncPinned callback wired here; the watchdog refresh callback is
	// owned by the HTTP handler today. The MCP-driven path skips it; the
	// next idle tick or manual reload picks the new pinned set up.
	_, err := c.modelAdmin.TogglePinned(ctx, name, action, nil)
	return err
}

// ---- Branding ----

// brandingAssetURL returns the same URL shape the public REST endpoint
// would emit so MCP and HTTP clients see identical wire output.
func brandingAssetURL(kind, file, defaultURL string) string {
	if file != "" {
		return "/branding/asset/" + kind
	}
	return defaultURL
}

func (c *Client) currentBranding() *localaitools.Branding {
	b := c.AppConfig.Branding
	return &localaitools.Branding{
		InstanceName:      b.InstanceName,
		InstanceTagline:   b.InstanceTagline,
		LogoURL:           brandingAssetURL("logo", b.LogoFile, "/static/logo.png"),
		LogoHorizontalURL: brandingAssetURL("logo_horizontal", b.LogoHorizontalFile, "/static/logo_horizontal.png"),
		FaviconURL:        brandingAssetURL("favicon", b.FaviconFile, "/favicon.svg"),
	}
}

func (c *Client) GetBranding(_ context.Context) (*localaitools.Branding, error) {
	return c.currentBranding(), nil
}

func (c *Client) SetBranding(_ context.Context, req localaitools.SetBrandingRequest) (*localaitools.Branding, error) {
	settings, err := c.AppConfig.ReadPersistedSettings()
	if err != nil {
		return nil, err
	}
	if req.InstanceName != nil {
		c.AppConfig.Branding.InstanceName = *req.InstanceName
		settings.InstanceName = req.InstanceName
	}
	if req.InstanceTagline != nil {
		c.AppConfig.Branding.InstanceTagline = *req.InstanceTagline
		settings.InstanceTagline = req.InstanceTagline
	}
	if err := c.AppConfig.WritePersistedSettings(settings); err != nil {
		return nil, err
	}
	return c.currentBranding(), nil
}

// ---- Voice profile library ----

func (c *Client) voiceProfileStore() (*voiceprofile.Store, error) {
	if c.VoiceProfiles != nil {
		return c.VoiceProfiles, nil
	}
	if c.AppConfig == nil {
		return nil, errors.New("voice profile store is unavailable")
	}
	c.VoiceProfiles = voiceprofile.NewStore(c.AppConfig.DataPath)
	return c.VoiceProfiles, nil
}

func (c *Client) ListVoiceProfiles(ctx context.Context) ([]localaitools.VoiceProfile, error) {
	store, err := c.voiceProfileStore()
	if err != nil {
		return nil, err
	}
	return store.List(ctx)
}

func (c *Client) CreateVoiceProfile(ctx context.Context, req localaitools.CreateVoiceProfileRequest) (*localaitools.VoiceProfile, error) {
	if req.AudioBase64 == "" {
		return nil, errors.New("audio_base64 is required")
	}
	if base64.StdEncoding.DecodedLen(len(req.AudioBase64)) > int(voiceprofile.MaxAudioBytes) {
		return nil, voiceprofile.ErrAudioTooLarge
	}
	store, err := c.voiceProfileStore()
	if err != nil {
		return nil, err
	}
	profile, err := store.Create(ctx, voiceprofile.CreateInput{
		Name:             req.Name,
		Description:      req.Description,
		Language:         req.Language,
		Transcript:       req.Transcript,
		ConsentConfirmed: req.ConsentConfirmed,
	}, base64.NewDecoder(base64.StdEncoding, strings.NewReader(req.AudioBase64)))
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (c *Client) DeleteVoiceProfile(ctx context.Context, id string) error {
	store, err := c.voiceProfileStore()
	if err != nil {
		return err
	}
	return store.Delete(ctx, id)
}

// ---- helpers ----

// sendModelOp pushes op onto ch but bails if ctx is cancelled before the
// gallery worker is ready to receive. Without the select the chat handler
// goroutine would block forever when the worker is paused or the buffer is
// full — the LLM keeps polling and the request goroutine leaks. When the
// caller cancels we surface ctx.Err() so the LLM stops polling.
func sendModelOp(ctx context.Context, ch chan galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig], op galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]) error {
	select {
	case ch <- op:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// sendBackendOp is the BackendGalleryChannel sibling of sendModelOp. Same
// rationale — see that comment.
func sendBackendOp(ctx context.Context, ch chan galleryop.ManagementOp[gallery.GalleryBackend, any], op galleryop.ManagementOp[gallery.GalleryBackend, any]) error {
	select {
	case ch <- op:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func filterGalleries(galleries []config.Gallery, name string) []config.Gallery {
	for _, g := range galleries {
		if g.Name == name {
			return []config.Gallery{g}
		}
	}
	return nil
}

// capabilityToFlag maps the public Capability constants to the loader's
// usecase bitflag. CapabilityAny (the empty value) selects all models.
func capabilityToFlag(capability localaitools.Capability) (config.ModelConfigUsecase, bool) {
	switch capability {
	case localaitools.CapabilityAny:
		return 0, false
	case localaitools.CapabilityChat:
		return config.FLAG_CHAT, true
	case localaitools.CapabilityCompletion:
		return config.FLAG_COMPLETION, true
	case localaitools.CapabilityEmbeddings:
		return config.FLAG_EMBEDDINGS, true
	case localaitools.CapabilityImage:
		return config.FLAG_IMAGE, true
	case localaitools.CapabilityTTS:
		return config.FLAG_TTS, true
	case localaitools.CapabilityTranscript:
		return config.FLAG_TRANSCRIPT, true
	case localaitools.CapabilityRerank:
		return config.FLAG_RERANK, true
	case localaitools.CapabilityVAD:
		return config.FLAG_VAD, true
	}
	return 0, false
}

// ---- Usage / billing ----

func (c *Client) GetUsageStats(ctx context.Context, q localaitools.UsageStatsQuery) (*localaitools.UsageStats, error) {
	if c.StatsRecorder == nil {
		return nil, errors.New("usage tracking is not available on this server")
	}
	period := q.Period
	if period == "" {
		period = "month"
	}

	// Resolve which user this is. In single-user no-auth mode the
	// inproc client doesn't have an echo context to read auth.GetUser
	// from, so the FallbackUser is the only available identity. When
	// auth IS on, the assistant runs under a privileged session and the
	// caller can pass q.UserID; we don't enforce admin here because the
	// MCP server itself is gated on admin (see prompts/10_safety.md).
	var viewerID, viewerName, viewerRole string
	switch {
	case q.UserID != "":
		viewerID = q.UserID
	case c.FallbackUser != nil:
		viewerID = c.FallbackUser.ID
		viewerName = c.FallbackUser.Name
		viewerRole = c.FallbackUser.Role
	default:
		return nil, errors.New("no user context for usage query (auth is on but no user id was provided)")
	}

	queryUser := viewerID
	if q.All {
		// /api/usage/all: cluster-wide by default, but honour the
		// optional UserID filter so admins can scope to one user —
		// matches the REST endpoint's ?user_id=… query param. Empty
		// q.UserID falls through to the cluster-wide aggregate.
		queryUser = q.UserID
	}

	rows, err := c.StatsRecorder.Aggregate(ctx, billing.AggregateQuery{
		UserID: queryUser,
		Period: period,
	})
	if err != nil {
		return nil, fmt.Errorf("aggregate usage: %w", err)
	}

	totals := localaitools.UsageTotals{}
	buckets := make([]localaitools.UsageBucket, 0, len(rows))
	for _, r := range rows {
		buckets = append(buckets, localaitools.UsageBucket{
			Bucket:           r.Bucket,
			Model:            r.Model,
			UserID:           r.UserID,
			UserName:         r.UserName,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			TotalTokens:      r.TotalTokens,
			RequestCount:     r.RequestCount,
		})
		totals.PromptTokens += r.PromptTokens
		totals.CompletionTokens += r.CompletionTokens
		totals.TotalTokens += r.TotalTokens
		totals.RequestCount += r.RequestCount
	}

	return &localaitools.UsageStats{
		Viewer:  localaitools.UsageViewer{ID: viewerID, Name: viewerName, Role: viewerRole},
		Period:  period,
		Totals:  totals,
		Buckets: buckets,
	}, nil
}

// ---- PII filter ----

func (c *Client) GetPIIEvents(ctx context.Context, q localaitools.PIIEventsQuery) ([]localaitools.PIIEvent, error) {
	if c.PIIEvents == nil {
		return nil, errors.New("PII filter is disabled")
	}
	events, err := c.PIIEvents.List(ctx, pii.ListQuery{
		CorrelationID: q.CorrelationID,
		UserID:        q.UserID,
		PatternID:     q.PatternID,
		Kind:          pii.KindPII,
		Limit:         q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list pii events: %w", err)
	}
	out := make([]localaitools.PIIEvent, 0, len(events))
	for _, e := range events {
		out = append(out, localaitools.PIIEvent{
			ID:            e.ID,
			CorrelationID: e.CorrelationID,
			UserID:        e.UserID,
			Direction:     string(e.Direction),
			PatternID:     e.PatternID,
			ByteOffset:    e.ByteOffset,
			Length:        e.Length,
			HashPrefix:    e.HashPrefix,
			Action:        string(e.Action),
			CreatedAt:     e.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return out, nil
}

func (c *Client) GetRouterDecisions(ctx context.Context, q localaitools.RouterDecisionsQuery) ([]localaitools.RouterDecision, error) {
	if c.RouterDecisions == nil {
		return []localaitools.RouterDecision{}, nil
	}
	rows, err := c.RouterDecisions.List(ctx, router.DecisionListQuery{
		CorrelationID: q.CorrelationID,
		UserID:        q.UserID,
		RouterModel:   q.RouterModel,
		Limit:         q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list router decisions: %w", err)
	}
	out := make([]localaitools.RouterDecision, 0, len(rows))
	for _, r := range rows {
		out = append(out, localaitools.RouterDecision{
			ID:             r.ID,
			CorrelationID:  r.CorrelationID,
			UserID:         r.UserID,
			RouterModel:    r.RouterModel,
			RequestedModel: r.RequestedModel,
			ServedModel:    r.ServedModel,
			Classifier:     r.Classifier,
			Label:          r.Label,
			Score:          r.Score,
			LatencyMs:      r.LatencyMs,
			Cached:         r.Cached,
			CreatedAt:      r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return out, nil
}

func (c *Client) GetMiddlewareStatus(ctx context.Context) (*localaitools.MiddlewareStatus, error) {
	router := localaitools.MiddlewareRouterStatus{
		Configured: false,
		Models:     []string{},
		Note:       "Intelligent routing is not yet implemented.",
	}
	piiSection := localaitools.MiddlewarePIIStatus{
		EnabledGlobally: c.PIIEvents != nil,
		Models:          []localaitools.MiddlewarePIIModel{},
	}
	piiSection.DefaultEnabledForBackends = []string{"cloud-proxy"}
	if c.ConfigLoader != nil {
		for _, cfg := range c.ConfigLoader.GetAllModelsConfigs() {
			cfg := cfg
			piiSection.Models = append(piiSection.Models, localaitools.MiddlewarePIIModel{
				Name:              cfg.Name,
				Backend:           cfg.Backend,
				Enabled:           cfg.PIIIsEnabled(),
				Explicit:          cfg.PII.Enabled != nil,
				DefaultForBackend: cfg.Backend == "cloud-proxy",
				Detectors:         cfg.PIIDetectors(),
			})
		}
	}
	if c.PIIEvents != nil {
		if n, err := c.PIIEvents.Count(ctx); err == nil {
			piiSection.RecentEventCount = n
		}
	}
	return &localaitools.MiddlewareStatus{PII: piiSection, Router: router}, nil
}

func capabilityFlagsOf(m *config.ModelConfig) []string {
	var out []string
	for label, flag := range config.GetAllModelConfigUsecases() {
		if flag == 0 {
			continue
		}
		if m.HasUsecases(flag) {
			// Trim "FLAG_" prefix for prettier output.
			out = append(out, label[len("FLAG_"):])
		}
	}
	return out
}
