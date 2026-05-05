// Package inproc provides an in-process LocalAIClient that calls LocalAI
// services directly. Used by the chat handler when a chat session opts into
// the LocalAI Assistant modality, avoiding an HTTP loopback to the same
// process and the synthetic admin-credential dance that would entail.
package inproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/gallery/importers"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/internal"
	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/vram"
)

// Client implements localaitools.LocalAIClient by calling LocalAI services
// directly. It is intentionally a thin shim — distribution and persistence
// concerns are handled by the underlying services (GalleryService is already
// distributed-aware, ModelConfigLoader manages on-disk YAML, etc.), so this
// layer just translates between MCP DTOs and service signatures.
type Client struct {
	AppConfig    *config.ApplicationConfig
	SystemState  *system.SystemState
	ConfigLoader *config.ModelConfigLoader
	ModelLoader  *model.ModelLoader
	Gallery      *galleryop.GalleryService

	modelAdmin *modeladmin.ConfigService
}

// New builds a Client wired to the given services. All fields are required
// except ModelLoader (used only for SystemInfo's loaded-models report and
// best-effort ShutdownModel calls during config edits).
func New(appConfig *config.ApplicationConfig, systemState *system.SystemState, cl *config.ModelConfigLoader, ml *model.ModelLoader, gs *galleryop.GalleryService) *Client {
	return &Client{
		AppConfig:    appConfig,
		SystemState:  systemState,
		ConfigLoader: cl,
		ModelLoader:  ml,
		Gallery:      gs,
		modelAdmin:   modeladmin.NewConfigService(cl, appConfig),
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
