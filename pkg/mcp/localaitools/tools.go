package localaitools

// Tool names exposed by the LocalAI Assistant MCP server. Use these
// constants — never bare strings — when registering tools, asserting the
// catalog in tests, or referencing tool names from other packages. The
// embedded skill prompts under prompts/ keep the bare strings because
// go:embed-ed markdown can't reference Go constants; prompts_test.go guards
// that the mutating names stay aligned with the confirmation rule.
const (
	// Read-only tools.
	ToolGallerySearch        = "gallery_search"
	ToolListInstalledModels  = "list_installed_models"
	ToolListGalleries        = "list_galleries"
	ToolGetJobStatus         = "get_job_status"
	ToolGetModelConfig       = "get_model_config"
	ToolListBackends         = "list_backends"
	ToolListKnownBackends    = "list_known_backends"
	ToolSystemInfo           = "system_info"
	ToolListNodes            = "list_nodes"
	ToolVRAMEstimate         = "vram_estimate"
	ToolGetBranding          = "get_branding"
	ToolGetUsageStats        = "get_usage_stats"
	ToolGetPIIEvents         = "get_pii_events"
	ToolGetMiddlewareStatus  = "get_middleware_status"
	ToolGetRouterDecisions   = "get_router_decisions"
	ToolGetRouterCorpusStats = "get_router_corpus_stats"
	ToolListVoiceProfiles    = "list_voice_profiles"

	// Mutating tools — guarded by Options.DisableMutating and the
	// LLM-side safety prompt (see prompts/10_safety.md).
	ToolInstallModel       = "install_model"
	ToolImportModelURI     = "import_model_uri"
	ToolDeleteModel        = "delete_model"
	ToolEditModelConfig    = "edit_model_config"
	ToolReloadModels       = "reload_models"
	ToolLoadModel          = "load_model"
	ToolInstallBackend     = "install_backend"
	ToolUpgradeBackend     = "upgrade_backend"
	ToolToggleModelState   = "toggle_model_state"
	ToolToggleModelPinned  = "toggle_model_pinned"
	ToolSetBranding        = "set_branding"
	ToolSetAlias           = "set_alias"
	ToolSeedRouterCorpus   = "seed_router_corpus"
	ToolClearRouterCorpus  = "clear_router_corpus"
	ToolCreateVoiceProfile = "create_voice_profile"
	ToolDeleteVoiceProfile = "delete_voice_profile"
	ToolSetNodeVRAMBudget  = "set_node_vram_budget"

	// ToolListAliases is read-only but lives here so the alias tools stay
	// grouped; the catalog tests assert its read-only placement.
	ToolListAliases = "list_aliases"
)

// DefaultServerName is the MCP Implementation.Name surfaced when
// Options.ServerName is empty. Use the constant when you want a stable
// reference across packages (e.g. test fixtures, CLI defaults).
const DefaultServerName = "localai-admin"

// mutatingToolNames is the canonical safety-prompt coverage list. Registration
// remains grouped by feature, while prompts_test.go mechanically ensures every
// state-changing tool is named in the confirmation rule.
var mutatingToolNames = []string{
	ToolInstallModel,
	ToolImportModelURI,
	ToolDeleteModel,
	ToolEditModelConfig,
	ToolReloadModels,
	ToolLoadModel,
	ToolInstallBackend,
	ToolUpgradeBackend,
	ToolToggleModelState,
	ToolToggleModelPinned,
	ToolSetBranding,
	ToolSetAlias,
	ToolSeedRouterCorpus,
	ToolClearRouterCorpus,
	ToolCreateVoiceProfile,
	ToolDeleteVoiceProfile,
	ToolSetNodeVRAMBudget,
}
