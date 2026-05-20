package localaitools

// Tool names exposed by the LocalAI Assistant MCP server. Use these
// constants — never bare strings — when registering tools, asserting the
// catalog in tests, or referencing tool names from other packages. The
// embedded skill prompts under prompts/ keep the bare strings because
// go:embed-ed markdown can't reference Go constants; TestPromptsContain
// SafetyAnchors guards that those strings stay aligned.
const (
	// Read-only tools.
	ToolGallerySearch       = "gallery_search"
	ToolListInstalledModels = "list_installed_models"
	ToolListGalleries       = "list_galleries"
	ToolGetJobStatus        = "get_job_status"
	ToolGetModelConfig      = "get_model_config"
	ToolListBackends        = "list_backends"
	ToolListKnownBackends   = "list_known_backends"
	ToolSystemInfo          = "system_info"
	ToolListNodes           = "list_nodes"
	ToolVRAMEstimate        = "vram_estimate"
	ToolGetBranding         = "get_branding"

	// Mutating tools — guarded by Options.DisableMutating and the
	// LLM-side safety prompt (see prompts/10_safety.md).
	ToolInstallModel      = "install_model"
	ToolImportModelURI    = "import_model_uri"
	ToolDeleteModel       = "delete_model"
	ToolEditModelConfig   = "edit_model_config"
	ToolReloadModels      = "reload_models"
	ToolInstallBackend    = "install_backend"
	ToolUpgradeBackend    = "upgrade_backend"
	ToolToggleModelState  = "toggle_model_state"
	ToolToggleModelPinned = "toggle_model_pinned"
	ToolSetBranding       = "set_branding"
)

// DefaultServerName is the MCP Implementation.Name surfaced when
// Options.ServerName is empty. Use the constant when you want a stable
// reference across packages (e.g. test fixtures, CLI defaults).
const DefaultServerName = "localai-admin"
