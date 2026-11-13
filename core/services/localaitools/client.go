package localaitools

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/pkg/vram"
)

// LocalAIClient is the surface tools depend on. It has two implementations:
//
//   - inproc.Client (in-process; calls LocalAI services directly)
//   - httpapi.Client (out-of-process; calls the LocalAI REST API)
//
// Tool handlers and the embedded skill prompts are agnostic to which
// implementation backs the client.
//
// Where the same shape already exists elsewhere in the codebase
// (config.Gallery, gallery.Metadata, schema.KnownBackend, vram.EstimateResult,
// modeladmin.Action/Capability) we surface it directly rather than maintain
// a parallel DTO — keeping the LLM-visible wire format aligned with the
// rest of LocalAI by construction.
type LocalAIClient interface {
	// ---- Models / gallery (read) ----
	GallerySearch(ctx context.Context, q GallerySearchQuery) ([]gallery.Metadata, error)
	// SearchModels asks remote providers (Hugging Face, Pirate Face) for
	// models that are not in a configured gallery. A result's URI goes to
	// ImportModelURI.
	SearchModels(ctx context.Context, req ModelSearchRequest) (*ModelSearchResponse, error)
	// InspectRemoteModel reads one search result: its weight files grouped by
	// quantization with sizes, licence, gated flag, the backend an import
	// would pick, and optionally a memory estimate. Nothing is downloaded.
	InspectRemoteModel(ctx context.Context, req RemoteModelRequest) (*RemoteModelInfo, error)
	ListInstalledModels(ctx context.Context, capability Capability) ([]InstalledModel, error)
	ListGalleries(ctx context.Context) ([]config.Gallery, error)
	GetJobStatus(ctx context.Context, jobID string) (*JobStatus, error)
	GetModelConfig(ctx context.Context, name string) (*ModelConfigView, error)

	// ---- Models / gallery (write) ----
	InstallModel(ctx context.Context, req InstallModelRequest) (jobID string, err error)
	DeleteModel(ctx context.Context, name string) error
	EditModelConfig(ctx context.Context, name string, patch map[string]any) error
	ReloadModels(ctx context.Context) error
	// LoadModel pre-loads a model into memory by name (the inverse of shutting
	// it down). For a realtime pipeline model every configured sub-model is
	// loaded; it returns the model names that became resident.
	LoadModel(ctx context.Context, model string) ([]string, error)
	// CancelModelLoad cancels one distributed load attempt, named by the job id
	// that load-status reports. It never cancels a replacement attempt.
	CancelModelLoad(ctx context.Context, model, jobID string) (LoadCancelResult, error)
	ImportModelURI(ctx context.Context, req ImportModelURIRequest) (*ImportModelURIResponse, error)

	// ---- Model aliases ----
	// SetAlias creates the alias `name` pointing at `target`, or swaps an
	// existing alias's target. The server validates that `target` is an
	// existing, non-alias, enabled model. Deletion reuses DeleteModel.
	SetAlias(ctx context.Context, name, target string) error
	// ListAliases returns every configured alias and its target.
	ListAliases(ctx context.Context) ([]AliasInfo, error)

	// ---- Backends ----
	// ListBackends returns installed backends. The shape stays a thin
	// localaitools.Backend rather than gallery.SystemBackend because the
	// latter carries filesystem paths (RunFile, Metadata) the LLM
	// shouldn't see.
	ListBackends(ctx context.Context) ([]Backend, error)
	// ListKnownBackends returns the same shape as REST /backends/known.
	ListKnownBackends(ctx context.Context) ([]schema.KnownBackend, error)
	// ListBackendLibraryUpdates returns the installed library backends that
	// have a newer build in their library. It reads the cached index and
	// installs nothing.
	ListBackendLibraryUpdates(ctx context.Context) ([]BackendLibraryUpdate, error)
	// BackendUpdates returns the gallery and library backends that have a
	// newer build, with the freshness of each source. It answers from the
	// cache of the instance and contacts no registry.
	BackendUpdates(ctx context.Context) (*backendupdates.Snapshot, error)
	// CheckBackendUpdates checks the registries now, waits for the check and
	// returns the new data. It installs nothing.
	CheckBackendUpdates(ctx context.Context) (*backendupdates.Snapshot, error)
	InstallBackend(ctx context.Context, req InstallBackendRequest) (jobID string, err error)
	// InstallLibraryBackend installs a backend from a library entry. It
	// returns when the install is done.
	InstallLibraryBackend(ctx context.Context, req InstallLibraryBackendRequest) (*InstallLibraryBackendResult, error)
	UpgradeBackend(ctx context.Context, name string) (jobID string, err error)

	// ---- System ----
	SystemInfo(ctx context.Context) (*SystemInfo, error)
	ListNodes(ctx context.Context) ([]Node, error)
	ListScheduling(ctx context.Context) ([]ModelSchedulingConfig, error)
	GetScheduling(ctx context.Context, modelName string) (*ModelSchedulingConfig, error)
	SetScheduling(ctx context.Context, req SetSchedulingRequest) (*ModelSchedulingConfig, error)
	DeleteScheduling(ctx context.Context, modelName string) error
	// SetNodeVRAMBudget sets (or, with an empty budget, clears) a federated
	// node's VRAM allocation cap as a sticky admin override. Only meaningful
	// in distributed mode; single-process clients report it as unavailable.
	SetNodeVRAMBudget(ctx context.Context, nodeID, budget string) error
	VRAMEstimate(ctx context.Context, req VRAMEstimateRequest) (*vram.EstimateResult, error)

	// ---- State ----
	// ToggleModelState accepts modeladmin.ActionEnable / ActionDisable.
	ToggleModelState(ctx context.Context, name string, action modeladmin.Action) error
	// ToggleModelPinned accepts modeladmin.ActionPin / ActionUnpin.
	ToggleModelPinned(ctx context.Context, name string, action modeladmin.Action) error

	// ---- Branding / whitelabeling ----
	// GetBranding returns the configured instance branding (name, tagline,
	// asset URLs).
	GetBranding(ctx context.Context) (*Branding, error)
	// SetBranding updates the text branding fields. Asset uploads are not
	// exposed over MCP — admins use the Settings UI for binary files.
	SetBranding(ctx context.Context, req SetBrandingRequest) (*Branding, error)

	// ---- Shared folders ----
	// ListSharedFolders returns every shared folder with its members.
	ListSharedFolders(ctx context.Context) ([]SharedFolder, error)
	CreateSharedFolder(ctx context.Context, name string) (*SharedFolder, error)
	RenameSharedFolder(ctx context.Context, id int64, name string) (*SharedFolder, error)
	// DeleteSharedFolder moves the folder to the trash; RestoreSharedFolder
	// with the returned undo_rev brings it back with its members.
	DeleteSharedFolder(ctx context.Context, id int64) (*SharedFolderRemoved, error)
	RestoreSharedFolder(ctx context.Context, rev int64) (*SharedFolder, error)
	// SetSharedFolderMember adds a member or changes its mode (read or write).
	SetSharedFolderMember(ctx context.Context, id int64, member, mode string) (*SharedFolder, error)
	RemoveSharedFolderMember(ctx context.Context, id int64, member string) (*SharedFolder, error)
	ListSharedFolderEvents(ctx context.Context, limit int) ([]SharedFolderEvent, error)

	// ---- Built-in chat tools ----

	// ListChatTools lists the built-in chat tools an add-on may replace (bash
	// on this machine, the web tools): on or off, who serves each, the add-ons
	// that replace it, and for bash the mode of chats_host_exec. It also lists
	// the built-in skills that installed add-ons replace, with who serves each.
	ListChatTools(ctx context.Context) (ChatBuiltinToolList, error)
	// ListHostExecEvents reads the audit of the commands the chats' built-in
	// bash ran on this machine, newest first.
	ListHostExecEvents(ctx context.Context, limit int) ([]HostExecEvent, error)

	// ---- Sandbox network ----

	// GetChatEgress returns the settings of the network of the chats' Sandbox
	// commands in force, the package registries and the counts of the last
	// 24 hours.
	GetChatEgress(ctx context.Context) (*ChatEgress, error)
	// ListEgressEvents reads the audit of the Sandbox network requests,
	// newest first.
	ListEgressEvents(ctx context.Context, q EgressEventsQuery) ([]EgressEvent, error)
	// SetChatEgressCeiling sets chats_egress_ceiling: off, registries or all.
	// The private-network switch is not here: its typed confirmation is a
	// person's act in Settings.
	SetChatEgressCeiling(ctx context.Context, ceiling string) (*ChatEgress, error)

	// ---- Usage / billing ----

	// GetUsageStats returns aggregated token usage. In single-user
	// no-auth mode this reports the synthetic local user's usage. The
	// implementation enforces "admin required to query other users".
	GetUsageStats(ctx context.Context, q UsageStatsQuery) (*UsageStats, error)

	// ---- PII filter ----
	// GetPIIEvents returns recent redaction events. Implementation
	// enforces "admin required" when auth is on. The regex pattern tools
	// were removed — detection policy lives on each detector model's
	// pii_detection block, managed via the model-config tools.
	GetPIIEvents(ctx context.Context, q PIIEventsQuery) ([]PIIEvent, error)

	// ---- Middleware admin ----
	// GetMiddlewareStatus returns the aggregated state surfaced on the
	// /app/middleware page: active PII patterns, per-model resolved
	// enabled state, recent event count, router placeholder.
	GetMiddlewareStatus(ctx context.Context) (*MiddlewareStatus, error)

	// ---- Router (intelligent routing) ----
	// GetRouterDecisions returns recent routing decisions for the
	// /app/middleware Routing tab and for agent-driven introspection.
	// Admin-required when auth is on.
	GetRouterDecisions(ctx context.Context, q RouterDecisionsQuery) ([]RouterDecision, error)

	// GetRouterCorpusStats reports a knn router's corpus size and
	// per-label counts — counts only, texts are never exposed.
	GetRouterCorpusStats(ctx context.Context, routerModel string) (*RouterCorpusStats, error)

	// SeedRouterCorpus adds labelled exemplars to a knn router's
	// corpus (embedded server-side, persisted, indexed immediately).
	SeedRouterCorpus(ctx context.Context, req RouterCorpusSeedRequest) (*RouterCorpusSeedResult, error)

	// ClearRouterCorpus wipes a knn router's corpus — file and live
	// index.
	ClearRouterCorpus(ctx context.Context, routerModel string) (*RouterCorpusClearResult, error)

	// ---- Failover chains ----
	// ListFailoverChains reports every configured failover chain, its
	// currently active target, and the health of each target.
	ListFailoverChains(ctx context.Context) ([]FailoverChainInfo, error)
	// PinFailoverTarget forces chain to serve every request from target,
	// regardless of health, until unpinned.
	PinFailoverTarget(ctx context.Context, chain, target string) error
	// UnpinFailoverTarget removes chain's pin so health decides the
	// active target again.
	UnpinFailoverTarget(ctx context.Context, chain string) error

	// ---- Client (inference) ----
	// These run inference through the LocalAI API as the acting user, so the
	// route's permission, the model allow-list and the quota apply. Files
	// are references that a FileStore resolves; results are written there.
	GenerateImage(ctx context.Context, req GenerateImageRequest) (*ImageResult, error)
	EditImage(ctx context.Context, req EditImageRequest) (*ImageResult, error)
	Speak(ctx context.Context, req SpeakRequest) (*AudioResult, error)
	TranscribeAudio(ctx context.Context, req TranscribeAudioRequest) (*TranscriptResult, error)
	DescribeImage(ctx context.Context, req DescribeImageRequest) (*DescribeImageResult, error)
	Embed(ctx context.Context, req EmbedRequest) (*EmbedResult, error)
	Detect(ctx context.Context, req DetectRequest) (*DetectResult, error)
	Rerank(ctx context.Context, req RerankRequest) (*RerankResult, error)
	// ListModels lists the models the acting user may use, with their
	// capabilities. It goes through the API as that user, so the model
	// allow-list applies, and it carries no paths or settings. Unlike
	// ListInstalledModels (an administrator's view of the config), it works for
	// every user.
	ListModels(ctx context.Context, req ListModelsRequest) (*ModelList, error)
	GenerateVideo(ctx context.Context, req GenerateVideoRequest) (*VideoResult, error)
	GenerateSound(ctx context.Context, req GenerateSoundRequest) (*AudioResult, error)
	UpscaleImage(ctx context.Context, req UpscaleImageRequest) (*ImageResult, error)
	TransformAudio(ctx context.Context, req TransformAudioRequest) (*TransformAudioResult, error)
	VerifyVoice(ctx context.Context, req VoiceVerifyRequest) (*VerifyResult, error)
	IdentifyVoice(ctx context.Context, req IdentifyVoiceRequest) (*IdentifyResult, error)
	VerifyFace(ctx context.Context, req VerifyFaceRequest) (*VerifyResult, error)
	IdentifyFace(ctx context.Context, req IdentifyFaceRequest) (*IdentifyResult, error)

	// The voice profiles of the acting user (the voice_profile tool). They
	// go through the profile routes as that user, so the ownership rules of
	// the API apply: a user lists, uses and deletes their own profiles and
	// the instance-wide ones an administrator holds. CreateVoiceProfile
	// refuses unless the context carries the consent of a person
	// (WithVoiceConsent).
	ListVoiceProfiles(ctx context.Context) ([]VoiceProfile, error)
	CreateVoiceProfile(ctx context.Context, req CreateVoiceProfileRequest) (*VoiceProfile, error)
	DeleteVoiceProfile(ctx context.Context, id string) error
}
