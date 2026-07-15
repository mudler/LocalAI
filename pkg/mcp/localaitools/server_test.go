package localaitools

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/gallery"
)

// connectInMemory wires an MCP server (built via NewServer) to a client over
// a paired in-memory transport (net.Pipe). Returns the client session along
// with a teardown closure suitable for DeferCleanup.
func connectInMemory(client LocalAIClient, opts Options) (context.Context, *mcp.ClientSession, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := NewServer(client, opts)
	t1, t2 := mcp.NewInMemoryTransports()

	serverSession, err := srv.Connect(ctx, t1, nil)
	Expect(err).ToNot(HaveOccurred(), "server connect")

	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	clientSession, err := c.Connect(ctx, t2, nil)
	Expect(err).ToNot(HaveOccurred(), "client connect")

	return ctx, clientSession, func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
		cancel()
	}
}

// listToolNames returns the sorted list of tool names exposed by the server.
func listToolNames(ctx context.Context, sess *mcp.ClientSession) []string {
	res, err := sess.ListTools(ctx, nil)
	Expect(err).ToNot(HaveOccurred(), "list tools")
	names := make([]string, 0, len(res.Tools))
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	return names
}

// callTool is a small wrapper to reduce boilerplate. CallToolParams.Arguments
// is declared as `any` and the SDK marshals it for the wire — passing a
// pre-marshalled []byte (or json.RawMessage) here would be double-encoded as
// a base64 string.
func callTool(ctx context.Context, sess *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	Expect(err).ToNot(HaveOccurred(), "call tool %s", name)
	return res
}

// resultText concatenates all TextContent items of a result.
func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// expectedReadOnlyCatalog is the tool set when DisableMutating=true. Sorted.
var expectedReadOnlyCatalog = sortedStrings(
	ToolGallerySearch,
	ToolGetBranding,
	ToolGetJobStatus,
	ToolGetMiddlewareStatus,
	ToolGetModelConfig,
	ToolGetPIIEvents,
	ToolGetRouterCorpusStats,
	ToolGetRouterDecisions,
	ToolGetUsageStats,
	ToolListBackends,
	ToolListGalleries,
	ToolListAliases,
	ToolListInstalledModels,
	ToolListKnownBackends,
	ToolListNodes,
	ToolListScheduling,
	ToolGetScheduling,
	ToolListVoiceProfiles,
	ToolSystemInfo,
	ToolVRAMEstimate,
)

// expectedFullCatalog derives from the read-only catalog plus the canonical
// mutating list used by the safety-prompt coverage test. Registering a new
// mutator now fails both catalog and prompt coverage until that list is updated.
var expectedFullCatalog = sortedStrings(append(append([]string(nil), expectedReadOnlyCatalog...), mutatingToolNames...)...)

func sortedStrings(in ...string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

var _ = Describe("Server tool catalog", func() {
	It("registers the full catalog when mutating tools are enabled", func() {
		ctx, sess, done := connectInMemory(&fakeClient{}, Options{})
		DeferCleanup(done)

		Expect(listToolNames(ctx, sess)).To(Equal(expectedFullCatalog))
	})

	It("skips mutating tools when DisableMutating is set", func() {
		ctx, sess, done := connectInMemory(&fakeClient{}, Options{DisableMutating: true})
		DeferCleanup(done)

		Expect(listToolNames(ctx, sess)).To(Equal(expectedReadOnlyCatalog))
	})
})

var _ = Describe("Tool dispatch", func() {
	type dispatchCase struct {
		tool       string
		args       any
		wantMethod string
	}

	cases := []dispatchCase{
		{ToolGallerySearch, GallerySearchQuery{Query: "qwen"}, "GallerySearch"},
		{ToolListInstalledModels, map[string]any{"capability": "chat"}, "ListInstalledModels"},
		{ToolListGalleries, struct{}{}, "ListGalleries"},
		{ToolListBackends, struct{}{}, "ListBackends"},
		{ToolListKnownBackends, struct{}{}, "ListKnownBackends"},
		{ToolSystemInfo, struct{}{}, "SystemInfo"},
		{ToolListNodes, struct{}{}, "ListNodes"},
		{ToolListScheduling, struct{}{}, "ListScheduling"},
		{ToolGetScheduling, DeleteSchedulingRequest{ModelName: "qwen"}, "GetScheduling"},
		{ToolSetScheduling, SetSchedulingRequest{ModelName: "qwen", MinReplicas: 1, MaxReplicas: 2}, "SetScheduling"},
		{ToolDeleteScheduling, DeleteSchedulingRequest{ModelName: "qwen"}, "DeleteScheduling"},
		{ToolListVoiceProfiles, struct{}{}, "ListVoiceProfiles"},
		{ToolInstallModel, InstallModelRequest{ModelName: "test/foo"}, "InstallModel"},
		{ToolImportModelURI, ImportModelURIRequest{URI: "Qwen/Qwen3-4B-GGUF"}, "ImportModelURI"},
		{ToolDeleteModel, map[string]any{"name": "foo"}, "DeleteModel"},
		{ToolInstallBackend, InstallBackendRequest{BackendName: "llama-cpp"}, "InstallBackend"},
		{ToolUpgradeBackend, map[string]any{"name": "llama-cpp"}, "UpgradeBackend"},
		{ToolEditModelConfig, map[string]any{"name": "foo", "patch": map[string]any{"context_size": 4096}}, "EditModelConfig"},
		{ToolReloadModels, struct{}{}, "ReloadModels"},
		{ToolLoadModel, map[string]any{"model": "test-model"}, "LoadModel"},
		{ToolToggleModelState, map[string]any{"name": "foo", "action": "enable"}, "ToggleModelState"},
		{ToolToggleModelPinned, map[string]any{"name": "foo", "action": "pin"}, "ToggleModelPinned"},
		{ToolSetAlias, map[string]any{"name": "gpt-4", "target": "real"}, "SetAlias"},
		{ToolListAliases, struct{}{}, "ListAliases"},
		{ToolCreateVoiceProfile, CreateVoiceProfileRequest{Name: "Narrator", Transcript: "Reference words", AudioBase64: "UklGRg==", ConsentConfirmed: true}, "CreateVoiceProfile"},
		{ToolDeleteVoiceProfile, DeleteVoiceProfileRequest{ID: "00000000-0000-0000-0000-000000000001"}, "DeleteVoiceProfile"},
	}

	for _, c := range cases {
		c := c
		It("routes "+c.tool+" to "+c.wantMethod, func() {
			fc := &fakeClient{
				installModel:   func(InstallModelRequest) (string, error) { return "job-1", nil },
				installBackend: func(InstallBackendRequest) (string, error) { return "job-2", nil },
				upgradeBackend: func(string) (string, error) { return "job-3", nil },
			}
			ctx, sess, done := connectInMemory(fc, Options{})
			DeferCleanup(done)

			res := callTool(ctx, sess, c.tool, c.args)
			Expect(res.IsError).To(BeFalse(), "tool %s returned error: %s", c.tool, resultText(res))

			calls := fc.recorded()
			Expect(calls).ToNot(BeEmpty(), "tool %s did not call the client", c.tool)
			Expect(calls[len(calls)-1].method).To(Equal(c.wantMethod))
		})
	}
})

var _ = Describe("Tool error surfacing", func() {
	It("propagates client errors verbatim via IsError + TextContent", func() {
		fc := &fakeClient{
			gallerySearch: func(GallerySearchQuery) ([]gallery.Metadata, error) {
				return nil, errors.New("backend on fire")
			},
		}
		ctx, sess, done := connectInMemory(fc, Options{})
		DeferCleanup(done)

		res := callTool(ctx, sess, ToolGallerySearch, GallerySearchQuery{Query: "x"})
		Expect(res.IsError).To(BeTrue(), "expected IsError, got: %s", resultText(res))
		Expect(resultText(res)).To(ContainSubstring("backend on fire"))
	})
})

var _ = Describe("Scheduling tool behavior", func() {
	It("round-trips list/get/set/delete over the MCP surface", func() {
		fc := &fakeClient{
			listScheduling: func() ([]ModelSchedulingConfig, error) {
				return []ModelSchedulingConfig{{ModelName: "qwen", MinReplicas: 1, MaxReplicas: 2}}, nil
			},
			getScheduling: func(modelName string) (*ModelSchedulingConfig, error) {
				return &ModelSchedulingConfig{ModelName: modelName, SpreadAll: true}, nil
			},
			setScheduling: func(req SetSchedulingRequest) (*ModelSchedulingConfig, error) {
				return &ModelSchedulingConfig{ModelName: req.ModelName, MinReplicas: req.MinReplicas, MaxReplicas: req.MaxReplicas}, nil
			},
		}
		ctx, sess, done := connectInMemory(fc, Options{})
		DeferCleanup(done)

		listRes := callTool(ctx, sess, ToolListScheduling, struct{}{})
		Expect(listRes.IsError).To(BeFalse(), resultText(listRes))
		Expect(resultText(listRes)).To(ContainSubstring(`"model_name": "qwen"`))

		getRes := callTool(ctx, sess, ToolGetScheduling, DeleteSchedulingRequest{ModelName: "qwen"})
		Expect(getRes.IsError).To(BeFalse(), resultText(getRes))
		Expect(resultText(getRes)).To(ContainSubstring(`"spread_all": true`))

		setRes := callTool(ctx, sess, ToolSetScheduling, SetSchedulingRequest{ModelName: "qwen", MinReplicas: 1, MaxReplicas: 2})
		Expect(setRes.IsError).To(BeFalse(), resultText(setRes))
		Expect(resultText(setRes)).To(ContainSubstring(`"max_replicas": 2`))

		deleteRes := callTool(ctx, sess, ToolDeleteScheduling, DeleteSchedulingRequest{ModelName: "qwen"})
		Expect(deleteRes.IsError).To(BeFalse(), resultText(deleteRes))
		Expect(resultText(deleteRes)).To(ContainSubstring(`"deleted": "qwen"`))
	})
})

var _ = Describe("Argument validation", func() {
	type validationCase struct {
		desc string
		tool string
		args any
		want string
	}

	// Required-field misses go through the SDK schema validator (the
	// generated input schema marks name as required), not our handler.
	cases := []validationCase{
		{"install_model rejects empty model_name", ToolInstallModel, InstallModelRequest{}, "model_name is required"},
		{"delete_model rejects missing name (schema)", ToolDeleteModel, map[string]any{}, "missing properties"},
		{"toggle_model_state rejects unknown action", ToolToggleModelState, map[string]any{"name": "foo", "action": "noop"}, "action must be one of"},
		{"edit_model_config rejects empty patch", ToolEditModelConfig, map[string]any{"name": "foo", "patch": map[string]any{}}, "patch is required"},
		{"create_voice_profile requires consent", ToolCreateVoiceProfile, CreateVoiceProfileRequest{Name: "Voice", Transcript: "words", AudioBase64: "UklGRg=="}, "consent_confirmed must be true"},
		{"set_scheduling requires model_name", ToolSetScheduling, SetSchedulingRequest{}, "model_name is required"},
		{"set_scheduling rejects invalid replica range", ToolSetScheduling, SetSchedulingRequest{ModelName: "qwen", MinReplicas: 3, MaxReplicas: 1}, "min_replicas must be <= max_replicas"},
		{"delete_scheduling requires model_name", ToolDeleteScheduling, DeleteSchedulingRequest{}, "model_name is required"},
	}

	for _, c := range cases {
		c := c
		It(c.desc, func() {
			ctx, sess, done := connectInMemory(&fakeClient{}, Options{})
			DeferCleanup(done)

			res := callTool(ctx, sess, c.tool, c.args)
			Expect(res.IsError).To(BeTrue(), "expected validation error; got %s", resultText(res))
			Expect(resultText(res)).To(ContainSubstring(c.want))
		})
	}
})

var _ = Describe("Concurrent tool calls", func() {
	It("handles 20 parallel CallTool requests against one session without a race", func() {
		fc := &fakeClient{}
		ctx, sess, done := connectInMemory(fc, Options{})
		DeferCleanup(done)

		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				callTool(ctx, sess, ToolListGalleries, struct{}{})
			}()
		}
		wg.Wait()

		Expect(fc.recorded()).To(HaveLen(20))
	})
})
