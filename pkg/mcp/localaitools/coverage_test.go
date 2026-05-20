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
// approximate via the welcome JSON; the test still requires an entry so
// the contributor explicitly acknowledges the asymmetry.
var toolToHTTPRoute = map[string]string{
	// Read-only tools.
	ToolGallerySearch:       "GET /models/available",
	ToolListInstalledModels: "GET / (welcome JSON, ModelsConfig field)",
	ToolListGalleries:       "GET /models/galleries",
	ToolGetJobStatus:        "GET /models/jobs/:uuid",
	ToolGetModelConfig:      "(none) — no JSON-only REST yet; httpapi.Client returns a documented stub",
	ToolListBackends:        "GET /backends",
	ToolListKnownBackends:   "GET /backends/known",
	ToolSystemInfo:          "GET / (welcome JSON)",
	ToolListNodes:           "GET /api/nodes",
	ToolVRAMEstimate:        "POST /api/models/vram-estimate",
	ToolGetBranding:         "GET /api/branding",

	// Mutating tools.
	ToolInstallModel:      "POST /models/apply",
	ToolImportModelURI:    "POST /models/import-uri",
	ToolDeleteModel:       "POST /models/delete/:name",
	ToolEditModelConfig:   "PATCH /api/models/config-json/:name",
	ToolReloadModels:      "POST /models/reload",
	ToolInstallBackend:    "POST /backends/apply",
	ToolUpgradeBackend:    "POST /backends/upgrade/:name",
	ToolToggleModelState:  "PUT /models/toggle-state/:name/:action",
	ToolToggleModelPinned: "PUT /models/toggle-pinned/:name/:action",
	ToolSetBranding:       "POST /api/settings (instance_name, instance_tagline)",
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

	// Deliberate non-test: we don't enumerate admin REST routes here. That
	// would require booting Application or parsing core/http/routes/localai.go,
	// both of which are brittle. The contract for "new admin REST endpoint
	// → MCP tool" is enforced by the PR checklist in
	// .agents/api-endpoints-and-auth.md, not by this test.
})
