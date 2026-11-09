package localaitools

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// toolToHTTPRoute is the canonical mapping between MCP tools and the
// LocalAI admin REST endpoints they wrap. The httpapi.Client MUST hit the
// listed route for the tool; the inproc.Client may bypass HTTP and call
// services directly, but the on-the-wire shape is documented here so the
// two sides stay aligned.
//
// Updating the map is REQUIRED when:
//   - You add a Tool* constant (tools.go).
//   - You change which REST endpoint the httpapi.Client calls.
//
// The TestToolHTTPRouteMappingComplete spec below FAILS until every Tool*
// is in the map. That is the drift detector — see
// .agents/localai-assistant-mcp.md for the contributor contract.
//
// "(none)" is a deliberate sentinel for tools whose data is not exposed
// over a single REST endpoint (e.g. system_info aggregates data the
// inproc client picks up directly from services). The httpapi.Client may
// compose it from several routes; the test still requires an entry so
// the contributor explicitly acknowledges the asymmetry.
var toolToHTTPRoute = map[string]string{
	// Read-only tools.
	ToolGallerySearch:             "GET /models/available",
	ToolListInstalledModels:       "GET /api/models/capabilities",
	ToolListGalleries:             "GET /models/galleries",
	ToolGetJobStatus:              "GET /models/jobs/:uuid",
	ToolGetModelConfig:            "(none) — no JSON-only REST yet; httpapi.Client returns a documented stub",
	ToolListBackends:              "GET /backends",
	ToolListKnownBackends:         "GET /backends/known",
	ToolListBackendLibraryUpdates: "GET /backends/library/updates",
	ToolBackendUpdates:            "GET /backends/updates (action list), POST /backends/updates/check (action check)",
	ToolSystemInfo:                "GET /version + GET /system (composed)",
	ToolListNodes:                 "GET /api/nodes",
	ToolListScheduling:            "GET /api/nodes/scheduling",
	ToolGetScheduling:             "GET /api/nodes/scheduling/:model",
	ToolVRAMEstimate:              "POST /api/models/vram-estimate",
	ToolGetBranding:               "GET /api/branding",
	ToolGetUsageStats:             "GET /api/usage (or /api/usage/all when all=true)",
	ToolGetPIIEvents:              "GET /api/pii/events",
	ToolGetMiddlewareStatus:       "GET /api/middleware/status",
	ToolGetRouterDecisions:        "GET /api/router/decisions",
	ToolGetRouterCorpusStats:      "GET /api/router/:name/corpus/stats",
	ToolListAliases:               "GET /api/aliases",
	ToolListSharedFolders:         "GET /api/chats/admin/shared-folders",
	ToolListSharedFolderEvents:    "GET /api/chats/admin/shared-folders/events",
	ToolListChatTools:             "GET /api/chats/admin/tools",
	ToolListHostExecEvents:        "GET /api/chats/admin/host-exec/events",
	ToolGetChatEgress:             "GET /api/chats/admin/egress",
	ToolListEgressEvents:          "GET /api/chats/admin/egress/events",
	ToolListFailoverChains:        "GET /api/failover",

	// Mutating tools.
	ToolInstallModel:             "POST /models/apply",
	ToolImportModelURI:           "POST /models/import-uri",
	ToolDeleteModel:              "POST /models/delete/:name",
	ToolEditModelConfig:          "PATCH /api/models/config-json/:name",
	ToolReloadModels:             "POST /models/reload",
	ToolCancelModelLoad:          "POST /api/models/:id/load-cancel",
	ToolLoadModel:                "POST /backend/load",
	ToolInstallBackend:           "POST /backends/apply",
	ToolInstallLibraryBackend:    "POST /backends/library/install",
	ToolUpgradeBackend:           "POST /backends/upgrade/:name",
	ToolToggleModelState:         "PUT /models/toggle-state/:name/:action",
	ToolToggleModelPinned:        "PUT /models/toggle-pinned/:name/:action",
	ToolSetBranding:              "POST /api/settings (instance_name, instance_tagline)",
	ToolSetAlias:                 "PATCH /api/models/config-json/:name (swap) or POST /models/import (create)",
	ToolSeedRouterCorpus:         "POST /api/router/:name/corpus",
	ToolClearRouterCorpus:        "DELETE /api/router/:name/corpus",
	ToolCreateSharedFolder:       "POST /api/chats/admin/shared-folders",
	ToolRenameSharedFolder:       "PATCH /api/chats/admin/shared-folders/:id",
	ToolDeleteSharedFolder:       "DELETE /api/chats/admin/shared-folders/:id",
	ToolRestoreSharedFolder:      "POST /api/chats/admin/shared-folders/restore",
	ToolSetSharedFolderMember:    "PUT /api/chats/admin/shared-folders/:id/members/:principal",
	ToolRemoveSharedFolderMember: "DELETE /api/chats/admin/shared-folders/:id/members/:principal",
	ToolSetNodeVRAMBudget:        "PUT /api/nodes/:id/vram-budget",
	ToolSetScheduling:            "POST /api/nodes/scheduling",
	ToolDeleteScheduling:         "DELETE /api/nodes/scheduling/:model",
	ToolPinFailoverTarget:        "POST /api/failover/:chain/pin",
	ToolUnpinFailoverTarget:      "DELETE /api/failover/:chain/pin",
	// chats_egress_allow_private is deliberately not a tool (uiOnly): see
	// adminRoutesWithoutTool.
	ToolSetChatEgressCeiling: "POST /api/settings (chats_egress_ceiling)",
	// DELETE /api/chats/files/trash and DELETE /api/chats/files/trash/:rev are
	// deliberately not tools. They delete a user's own files for good and
	// touch only that user's Trash, so they are the user's act, not
	// administration, and an assistant must not be able to make a deletion
	// permanent. The file tools of a chat stop at the Trash for the same
	// reason.
}

// The client tools wrap the inference routes. Their routes come from
// ClientTools, which clienttools_test.go checks against the API's own route
// and feature table, so the two cannot drift.
func init() {
	for _, s := range ClientTools {
		route := s.Method + " " + s.Route
		if prev, ok := toolToHTTPRoute[s.Tool]; ok {
			// One tool, several routes (an action parameter, or an optional
			// input that picks another route).
			route = prev + ", " + route
		}
		toolToHTTPRoute[s.Tool] = route
	}
}

// adminRoutesWithoutTool lists the administrator routes and settings that
// deliberately have no MCP tool, each with the reason. A route listed here
// must not be wrapped by a tool too (the spec below checks it); to add a tool
// for one, move it to toolToHTTPRoute.
var adminRoutesWithoutTool = map[string]string{
	// The administrator's oversight of other users' saved agents, triggers
	// and connectors. Each acts on another person's private automation
	// (disable or delete it). It stays a deliberate click on the
	// administrator's page, where the owner and the item are on screen, and
	// not something a model proposes from a chat about the instance.
	"GET /api/chats/admin/agents":                  "oversight of other users' agents: a person's act on the admin page",
	"DELETE /api/chats/admin/agents/:id":           "deletes another user's agent: a person's act on the admin page",
	"GET /api/chats/admin/triggers":                "oversight of other users' triggers: a person's act on the admin page",
	"GET /api/chats/admin/triggers/:id/runs":       "the runs of another user's trigger: read on the admin page only",
	"POST /api/chats/admin/triggers/:id/disable":   "stops another user's trigger: a person's act on the admin page",
	"DELETE /api/chats/admin/triggers/:id":         "deletes another user's trigger: a person's act on the admin page",
	"GET /api/chats/admin/connectors":              "oversight of other users' connectors: a person's act on the admin page",
	"POST /api/chats/admin/connectors/:id/disable": "stops another user's connector: a person's act on the admin page",
	"DELETE /api/chats/admin/connectors/:id":       "deletes another user's connector: a person's act on the admin page",
	// The OAuth client of an add-on: its client ID and secret, and the
	// approval of the login the add-on declares. A secret must not pass
	// through a chat, and saving is the approval, a person's act on the
	// add-on's settings form. See connector_oauth_unmapped_test.go.
	"GET /api/chats/admin/oauth-clients/:addonId": "shows the client and the approval of an add-on's login: read on the add-on's settings form",
	"PUT /api/chats/admin/oauth-clients/:addonId": "takes a client secret and approves an add-on's login: a secret must not pass through a chat",
	// A diagnostic button of Settings > Tools, not a setting to manage.
	"POST /api/chats/admin/web/test": "a diagnostic button: it runs one search and changes nothing",
	// Settings of the chats that change what commands may do on this machine
	// or which add-on serves a tool. They are read with list_chat_tools; the
	// change is a person's act in Settings > Tools.
	"POST /api/settings (chats_host_exec)":            "lets chats run commands on the server with no isolation: a person's act in Settings > Tools",
	"POST /api/settings (chats_tool_providers)":       "chooses which add-on serves a tool or skill: set in Settings > Tools",
	"POST /api/settings (chats_egress_allow_private)": "needs the typed phrase in chats_egress_private_confirm, a person's act",
	"POST /api/settings (chats_web_*)":                "the web tools settings: set in Settings > Tools, with Test search",
	// Read by get_chat_egress. Opening a host or a registry to every chat
	// is a decision made on the Settings > Tools list editor, like the
	// private switch.
	"POST /api/settings (chats_egress_extra_registries)": "opens registries to every chat: set on the Settings > Tools list editor",
	"POST /api/settings (chats_egress_allowed_hosts)":    "opens hosts to every chat: set on the Settings > Tools list editor",
	// Settings of every user's chats. A chat about the instance has no need
	// to change them, and the first one is a limit a model must not raise
	// for itself. A person sets them in Settings.
	"POST /api/settings (chats_max_iterations)":       "the step limit of every chat turn: a model must not raise its own limit; set in Settings > Tools",
	"POST /api/settings (chats_work_folder)":          "where every chat keeps its helper files: set in Settings > Tools",
	"POST /api/settings (chats_index_files)":          "reads the text of every user's documents into the server's store: a person's choice in Settings > Tools",
	"POST /api/settings (chats_index_max_text_bytes)": "how much text the index keeps per document, which sizes the database: set in Settings > Tools",
	"POST /api/settings (chats_extract_max_bytes)":    "a limit of the Files page Extract action that protects the server: a model must not raise it; set in Settings > Tools",
	"POST /api/settings (chats_extract_max_entries)":  "a limit of the Files page Extract action that protects the server: a model must not raise it; set in Settings > Tools",
	"POST /api/settings (chats_extract_7z_rar)":       "turns on parsers of untrusted 7z and RAR files inside the server: a person's choice in Settings > Tools",
	"POST /api/settings (chats_saved_agents)":         "lets every chat start the user's saved agents: set in Settings > Tools",
	"POST /api/settings (chats_suggestions)":          "the default of each user's own choice of assistant offers: set in Settings > Agents",
	"POST /api/settings (chats_vision_model)":         "the helper model of describe_image: set in Settings > Agents, with the model picker",
	"POST /api/settings (chats_transcription_model)":  "the helper model of transcribe_audio: set in Settings > Agents, with the model picker",
}

// allKnownTools is the union of expectedFullCatalog (defined in
// server_test.go). Keeping a single source of truth — the slice from
// server_test — and asserting the route map covers every entry catches
// the case "you added a Tool* but forgot to register it as MCP" indirectly
// (it'd be missing from expectedFullCatalog, which has its own assertion
// in TestServerRegistersExpectedToolCatalog).
var _ = Describe("Tool ↔ HTTP route coverage map", func() {
	It("has an entry for every Tool* in the published catalog", func() {
		for _, name := range expectedFullCatalog {
			_, ok := toolToHTTPRoute[name]
			Expect(ok).To(BeTrue(),
				"Tool %q is in expectedFullCatalog but not in toolToHTTPRoute. "+
					"When adding an MCP tool, update toolToHTTPRoute in coverage_test.go "+
					"with the REST endpoint the httpapi.Client calls (or '(none)' with a reason).",
				name)
		}
	})

	It("does not document tools that no longer exist in the catalog", func() {
		catalog := map[string]struct{}{}
		for _, name := range expectedFullCatalog {
			catalog[name] = struct{}{}
		}
		for name := range toolToHTTPRoute {
			_, ok := catalog[name]
			Expect(ok).To(BeTrue(),
				"toolToHTTPRoute documents %q but the tool is not registered. "+
					"Remove the stale entry.",
				name)
		}
	})

	It("gives a reason for every administrator route without a tool, and wraps none of them", func() {
		wrapped := map[string]bool{}
		for _, route := range toolToHTTPRoute {
			wrapped[route] = true
		}
		for route, why := range adminRoutesWithoutTool {
			Expect(why).NotTo(BeEmpty(), route)
			Expect(wrapped[route]).To(BeFalse(), "%s has a tool and a skip reason; keep one", route)
		}
	})

	// Deliberate non-test: we don't enumerate admin REST routes here. That
	// would require booting Application or parsing core/http/routes/localai.go,
	// both of which are brittle. The contract for "new admin REST endpoint
	// → MCP tool" is enforced by the PR checklist in
	// .agents/api-endpoints-and-auth.md, not by this test.
})
