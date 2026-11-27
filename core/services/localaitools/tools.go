package localaitools

// Tool names exposed by the LocalAI Assistant MCP server. Use these
// constants — never bare strings — when registering tools, asserting the
// catalog in tests, or referencing tool names from other packages. The
// embedded skill prompts under prompts/ keep the bare strings because
// go:embed-ed markdown can't reference Go constants; prompts_test.go guards
// that the mutating names stay aligned with the confirmation rule.
const (
	// Read-only tools.
	ToolGallerySearch             = "gallery_search"
	ToolSearchModels              = "search_models"
	ToolInspectRemoteModel        = "inspect_remote_model"
	ToolListInstalledModels       = "list_installed_models"
	ToolListGalleries             = "list_galleries"
	ToolGetJobStatus              = "get_job_status"
	ToolGetModelConfig            = "get_model_config"
	ToolListBackends              = "list_backends"
	ToolListKnownBackends         = "list_known_backends"
	ToolListBackendLibraryUpdates = "list_backend_library_updates"
	ToolBackendUpdates            = "backend_updates"
	ToolModelUpdates              = "model_updates"
	ToolUpgradeRunStatus          = "upgrade_run_status"
	ToolSystemInfo                = "system_info"
	ToolListNodes                 = "list_nodes"
	ToolListScheduling            = "list_scheduling"
	ToolGetScheduling             = "get_scheduling"
	ToolVRAMEstimate              = "vram_estimate"
	ToolParseEngineCommand        = "parse_engine_command"
	ToolPreviewEngineCommand      = "preview_engine_command"
	ToolGetBranding               = "get_branding"
	ToolGetUsageStats             = "get_usage_stats"
	ToolGetPIIEvents              = "get_pii_events"
	ToolGetMiddlewareStatus       = "get_middleware_status"
	ToolGetRouterDecisions        = "get_router_decisions"
	ToolGetRouterCorpusStats      = "get_router_corpus_stats"
	ToolListSharedFolders         = "list_shared_folders"
	ToolListSharedFolderEvents    = "list_shared_folder_events"
	ToolListChatTools             = "list_chat_tools"
	ToolListHostExecEvents        = "list_host_exec_events"
	ToolGetChatEgress             = "get_chat_egress"
	ToolListEgressEvents          = "list_egress_events"

	// Mutating tools — guarded by Options.DisableMutating and the
	// LLM-side safety prompt (see prompts/10_safety.md).
	ToolInstallModel             = "install_model"
	ToolImportModelURI           = "import_model_uri"
	ToolDeleteModel              = "delete_model"
	ToolEditModelConfig          = "edit_model_config"
	ToolReloadModels             = "reload_models"
	ToolCancelModelLoad          = "cancel_model_load"
	ToolLoadModel                = "load_model"
	ToolInstallBackend           = "install_backend"
	ToolUpgradeBackend           = "upgrade_backend"
	ToolUpgradeModel             = "upgrade_model"
	ToolUpgradeRunStart          = "upgrade_run_start"
	ToolUpgradeRunCancel         = "upgrade_run_cancel"
	ToolInstallLibraryBackend    = "install_library_backend"
	ToolToggleModelState         = "toggle_model_state"
	ToolToggleModelPinned        = "toggle_model_pinned"
	ToolSetBranding              = "set_branding"
	ToolSetAlias                 = "set_alias"
	ToolSeedRouterCorpus         = "seed_router_corpus"
	ToolClearRouterCorpus        = "clear_router_corpus"
	ToolCreateSharedFolder       = "create_shared_folder"
	ToolRenameSharedFolder       = "rename_shared_folder"
	ToolDeleteSharedFolder       = "delete_shared_folder"
	ToolRestoreSharedFolder      = "restore_shared_folder"
	ToolSetSharedFolderMember    = "set_shared_folder_member"
	ToolRemoveSharedFolderMember = "remove_shared_folder_member"
	ToolSetNodeVRAMBudget        = "set_node_vram_budget"
	// ToolSetNodeLimits sets or clears the per-node overrides of the memory
	// settings (models kept loaded, VRAM budget, threshold, model swap).
	ToolSetNodeLimits = "set_node_limits"
	// ToolSetModelMemoryPolicy changes Settings > Models in memory: the
	// model limit, model swap, eviction, idle unload and the reclaimer.
	ToolSetModelMemoryPolicy = "set_model_memory_policy"
	ToolSetScheduling        = "set_scheduling"
	ToolDeleteScheduling     = "delete_scheduling"
	// ToolPinFailoverTarget and ToolUnpinFailoverTarget live here (rather
	// than grouped with ToolListFailoverChains below) so mutatingToolNames
	// stays a contiguous scan of this block.
	ToolPinFailoverTarget    = "pin_failover_target"
	ToolUnpinFailoverTarget  = "unpin_failover_target"
	ToolSetChatEgressCeiling = "set_chat_egress_ceiling"
	ToolRevokeP2PDevice      = "revoke_p2p_device"
	ToolCancelP2PInvite      = "cancel_p2p_invite"
	// ToolManageFederationGroup creates, updates, deletes, drains and
	// undrains federation groups (an action parameter).
	ToolManageFederationGroup = "manage_federation_group"
	ToolSetP2PSplitSlowPath   = "set_p2p_split_slow_path"
	ToolSetP2PSplitNode       = "set_p2p_split_node"
	ToolSetNodeSplitServer    = "set_node_split_server"
	// ToolDrainNode drains a node or resumes a draining one (an action
	// parameter).
	ToolDrainNode         = "drain_node"
	ToolSetP2PRelayLimits = "set_p2p_relay_limits"
	ToolSetP2PUserPairing = "set_p2p_user_pairing"
	// ToolSetCarrierWeights changes the carrier weights of a distributed cluster.
	ToolSetCarrierWeights = "set_carrier_weights"

	// ToolListAliases is read-only but lives here so the alias tools stay
	// grouped; the catalog tests assert its read-only placement.
	ToolListAliases = "list_aliases"

	// ToolListFailoverChains is read-only but lives here so the failover
	// tools stay grouped; the catalog tests assert its read-only placement.
	ToolListFailoverChains = "list_failover_chains"

	// ToolGetCarrierWeights is read-only; set_carrier_weights is in the
	// mutating block above.
	ToolGetCarrierWeights = "get_carrier_weights"

	// The read-only P2P tools; revoke_p2p_device and cancel_p2p_invite are
	// in the mutating block above.
	ToolGetP2PStatus      = "get_p2p_status"
	ToolListP2PDevices    = "list_p2p_devices"
	ToolListP2PInvites    = "list_p2p_invites"
	ToolListP2PRelays     = "list_p2p_relays"
	ToolGetP2PRelayLimits = "get_p2p_relay_limits"
	// ToolGetP2PUserPairing reads p2p_user_pairing; set_p2p_user_pairing is
	// in the mutating block above.
	ToolGetP2PUserPairing = "get_p2p_user_pairing"
	// ToolListP2PSplitWorkers lists the model-split workers;
	// set_p2p_split_slow_path and set_p2p_split_node are in the mutating
	// block above.
	ToolListP2PSplitWorkers = "list_p2p_split_workers"

	// ToolListFederation is read-only: the instances and the federation
	// groups; manage_federation_group is in the mutating block above.
	ToolListFederation = "list_federation"

	// ToolPlugins manages the plugins in the Claude Code plugin format with
	// an action parameter. It is in the read-only catalog because its read
	// actions stay in the read-only flavour; that flavour refuses the
	// mutating actions in the handler, and the safety prompt names the tool.
	ToolPlugins = "plugins"
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
	ToolCancelModelLoad,
	ToolLoadModel,
	ToolInstallBackend,
	ToolUpgradeBackend,
	ToolUpgradeModel,
	ToolUpgradeRunStart,
	ToolUpgradeRunCancel,
	ToolInstallLibraryBackend,
	ToolToggleModelState,
	ToolToggleModelPinned,
	ToolSetBranding,
	ToolSetAlias,
	ToolSeedRouterCorpus,
	ToolClearRouterCorpus,
	ToolCreateSharedFolder,
	ToolRenameSharedFolder,
	ToolDeleteSharedFolder,
	ToolRestoreSharedFolder,
	ToolSetSharedFolderMember,
	ToolRemoveSharedFolderMember,
	ToolSetNodeVRAMBudget,
	ToolSetNodeLimits,
	ToolSetModelMemoryPolicy,
	ToolSetScheduling,
	ToolDeleteScheduling,
	ToolPinFailoverTarget,
	ToolUnpinFailoverTarget,
	ToolSetChatEgressCeiling,
	ToolRevokeP2PDevice,
	ToolCancelP2PInvite,
	ToolManageFederationGroup,
	ToolSetP2PSplitSlowPath,
	ToolSetP2PSplitNode,
	ToolSetNodeSplitServer,
	ToolDrainNode,
	ToolSetP2PRelayLimits,
	ToolSetP2PUserPairing,
	ToolSetCarrierWeights,
}
