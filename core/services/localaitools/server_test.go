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
	ToolSearchModels,
	ToolInspectRemoteModel,
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
	ToolListBackendLibraryUpdates,
	ToolBackendUpdates,
	ToolModelUpdates,
	ToolUpgradeRunStatus,
	ToolListNodes,
	ToolListScheduling,
	ToolGetScheduling,
	ToolListSharedFolders,
	ToolListSharedFolderEvents,
	ToolListChatTools,
	ToolListHostExecEvents,
	ToolGetChatEgress,
	ToolListEgressEvents,
	ToolSystemInfo,
	ToolVRAMEstimate,
	ToolParseEngineCommand,
	ToolPreviewEngineCommand,
	ToolListFailoverChains,
	ToolGetCarrierWeights,
	ToolGetP2PStatus,
	ToolListP2PDevices,
	ToolListP2PInvites,
	ToolListP2PRelays,
	ToolListFederation,
	ToolListP2PSplitWorkers,
	ToolGetP2PRelayLimits,
	ToolGetP2PUserPairing,
	ToolPlugins,
	// The read-only flavour keeps the client tools that only analyse or list.
	ToolTranscribeAudio, ToolDescribeImage, ToolDetect, ToolRerank,
	ToolVoice, ToolFace, ToolVoiceProfile, ToolListModels, ToolProjects,
)

// clientWriterTools are the client tools the read-only flavour leaves out: they
// write files or make new content.
var clientWriterTools = []string{ToolGenerateImage, ToolSpeak, ToolEmbed, ToolGenerateVideo, ToolGenerateSound, ToolUpscaleImage, ToolTransformAudio}

// expectedFullCatalog derives from the read-only catalog plus the canonical
// mutating list used by the safety-prompt coverage test. Registering a new
// mutator now fails both catalog and prompt coverage until that list is updated.
var expectedFullCatalog = sortedStrings(append(append(append([]string(nil), expectedReadOnlyCatalog...), mutatingToolNames...), clientWriterTools...)...)

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
		{ToolSearchModels, ModelSearchRequest{Query: "qwen"}, "SearchModels"},
		{ToolSearchModels, ModelSearchRequest{Sort: "latest"}, "SearchModels"},
		{ToolInspectRemoteModel, RemoteModelRequest{URI: "huggingface://a/b"}, "InspectRemoteModel"},
		{ToolListInstalledModels, map[string]any{"capability": "chat"}, "ListInstalledModels"},
		{ToolListModels, ListModelsRequest{Capability: "image"}, "ListModels"},
		{ToolListGalleries, struct{}{}, "ListGalleries"},
		{ToolListBackends, struct{}{}, "ListBackends"},
		{ToolListKnownBackends, struct{}{}, "ListKnownBackends"},
		{ToolListBackendLibraryUpdates, struct{}{}, "ListBackendLibraryUpdates"},
		{ToolBackendUpdates, map[string]any{"action": "list"}, "BackendUpdates"},
		{ToolBackendUpdates, map[string]any{"action": "check"}, "CheckBackendUpdates"},
		{ToolModelUpdates, map[string]any{"action": "list"}, "ModelUpdates"},
		{ToolModelUpdates, map[string]any{"action": "check"}, "CheckModelUpdates"},
		{ToolUpgradeModel, UpgradeModelRequest{Action: "upgrade", Name: "tiny", Choices: map[string]string{"context_size": "mine"}}, "UpgradeModel"},
		{ToolUpgradeModel, UpgradeModelRequest{Action: "config_only", Name: "tiny"}, "UpgradeModel"},
		{ToolUpgradeModel, UpgradeModelRequest{Action: "retry", Name: "tiny"}, "RetryModelUpgrade"},
		{ToolUpgradeModel, UpgradeModelRequest{Action: "remove_old", Name: "tiny"}, "RemoveOldModelVersion"},
		{ToolUpgradeModel, UpgradeModelRequest{Action: "hold", Name: "tiny"}, "SetModelHold"},
		{ToolUpgradeModel, UpgradeModelRequest{Action: "unhold", Name: "tiny"}, "SetModelHold"},
		{ToolUpgradeModel, UpgradeModelRequest{Action: "adopt", Name: "tiny", Gallery: "localai"}, "AdoptModel"},
		{ToolSystemInfo, struct{}{}, "SystemInfo"},
		{ToolListNodes, struct{}{}, "ListNodes"},
		{ToolListScheduling, struct{}{}, "ListScheduling"},
		{ToolGetScheduling, DeleteSchedulingRequest{ModelName: "qwen"}, "GetScheduling"},
		{ToolSetScheduling, SetSchedulingRequest{ModelName: "qwen", MinReplicas: 1, MaxReplicas: 2}, "SetScheduling"},
		{ToolDeleteScheduling, DeleteSchedulingRequest{ModelName: "qwen"}, "DeleteScheduling"},
		{ToolInstallModel, InstallModelRequest{ModelName: "test/foo"}, "InstallModel"},
		{ToolImportModelURI, ImportModelURIRequest{URI: "Qwen/Qwen3-4B-GGUF"}, "ImportModelURI"},
		{ToolDeleteModel, map[string]any{"name": "foo"}, "DeleteModel"},
		{ToolInstallBackend, InstallBackendRequest{BackendName: "llama-cpp"}, "InstallBackend"},
		{ToolInstallLibraryBackend, InstallLibraryBackendRequest{SourceID: "official", Name: "demo", Version: "1.0.0"}, "InstallLibraryBackend"},
		{ToolUpgradeBackend, map[string]any{"name": "llama-cpp"}, "UpgradeBackend"},
		{ToolUpgradeRunStart, UpgradeRunStartRequest{Items: []UpgradeRunItemRef{{Kind: "gallery", Name: "llama-cpp"}, {Kind: "model", Name: "tiny"}}}, "StartUpgradeRun"},
		{ToolUpgradeRunStatus, map[string]any{"id": "run-1"}, "UpgradeRuns"},
		{ToolUpgradeRunStatus, struct{}{}, "UpgradeRuns"},
		{ToolUpgradeRunCancel, map[string]any{"id": "run-1"}, "CancelUpgradeRun"},
		{ToolUpgradeRunCancel, map[string]any{"id": "run-1", "key": "model:tiny"}, "CancelUpgradeRun"},
		{ToolEditModelConfig, map[string]any{"name": "foo", "patch": map[string]any{"context_size": 4096}}, "EditModelConfig"},
		{ToolReloadModels, struct{}{}, "ReloadModels"},
		{ToolCancelModelLoad, map[string]any{"model": "test-model", "job_id": "generation"}, "CancelModelLoad"},
		{ToolLoadModel, map[string]any{"model": "test-model"}, "LoadModel"},
		{ToolToggleModelState, map[string]any{"name": "foo", "action": "enable"}, "ToggleModelState"},
		{ToolToggleModelPinned, map[string]any{"name": "foo", "action": "pin"}, "ToggleModelPinned"},
		{ToolSetAlias, map[string]any{"name": "gpt-4", "target": "real"}, "SetAlias"},
		{ToolListAliases, struct{}{}, "ListAliases"},
		{ToolParseEngineCommand, map[string]any{"engine": "llama-cpp", "command": "llama-server -c 4096"}, "ParseEngineCommand"},
		{ToolPreviewEngineCommand, map[string]any{"engine": "llama-cpp", "config": map[string]any{"context_size": 4096}}, "PreviewEngineCommand"},
		{ToolListSharedFolders, struct{}{}, "ListSharedFolders"},
		{ToolListSharedFolderEvents, SharedFolderEventsQuery{Limit: 10}, "ListSharedFolderEvents"},
		{ToolListChatTools, struct{}{}, "ListChatTools"},
		{ToolListHostExecEvents, HostExecEventsQuery{Limit: 10}, "ListHostExecEvents"},
		{ToolGetChatEgress, struct{}{}, "GetChatEgress"},
		{ToolListEgressEvents, EgressEventsQuery{Limit: 10, Verdict: "block"}, "ListEgressEvents"},
		{ToolSetChatEgressCeiling, SetChatEgressCeilingRequest{Ceiling: "registries"}, "SetChatEgressCeiling"},
		{ToolCreateSharedFolder, CreateSharedFolderRequest{Name: "Team"}, "CreateSharedFolder"},
		{ToolRenameSharedFolder, RenameSharedFolderRequest{ID: 3, Name: "Crew"}, "RenameSharedFolder"},
		{ToolDeleteSharedFolder, SharedFolderRef{ID: 3}, "DeleteSharedFolder"},
		{ToolRestoreSharedFolder, RestoreSharedFolderRequest{Rev: 9}, "RestoreSharedFolder"},
		{ToolSetSharedFolderMember, SetSharedFolderMemberRequest{ID: 3, Member: "@everyone", Mode: "read"}, "SetSharedFolderMember"},
		{ToolRemoveSharedFolderMember, RemoveSharedFolderMemberRequest{ID: 3, Member: "@everyone"}, "RemoveSharedFolderMember"},
		{ToolListFailoverChains, map[string]any{}, "ListFailoverChains"},
		{ToolGetCarrierWeights, map[string]any{}, "GetCarrierWeights"},
		{ToolSetCarrierWeights, map[string]any{"nats": 10}, "SetCarrierWeights"},
		{ToolPinFailoverTarget, map[string]any{"chain": "c", "target": "b"}, "PinFailoverTarget"},
		{ToolUnpinFailoverTarget, map[string]any{"chain": "c"}, "UnpinFailoverTarget"},
		{ToolGetP2PStatus, map[string]any{}, "GetP2PStatus"},
		{ToolListP2PDevices, map[string]any{}, "ListP2PDevices"},
		{ToolListP2PInvites, map[string]any{}, "ListP2PInvites"},
		{ToolListP2PRelays, map[string]any{}, "ListP2PRelays"},
		{ToolGetP2PRelayLimits, map[string]any{"id": "r1"}, "GetP2PRelayLimits"},
		{ToolSetP2PRelayLimits, map[string]any{"id": "r1", "rate_bps": 65536}, "SetP2PRelayLimits"},
		{ToolGetP2PUserPairing, map[string]any{}, "GetP2PUserPairing"},
		{ToolSetP2PUserPairing, map[string]any{"max_devices": 3}, "SetP2PUserPairing"},
		{ToolRevokeP2PDevice, map[string]any{"id": "d1"}, "RevokeP2PDevice"},
		{ToolCancelP2PInvite, map[string]any{"id": "i1"}, "CancelP2PInvite"},
		{ToolListFederation, map[string]any{}, "GetFederation"},
		{ToolManageFederationGroup, map[string]any{"action": "drain", "name": "g", "model": "a"}, "ManageFederationGroup"},
		{ToolListP2PSplitWorkers, map[string]any{}, "ListP2PSplitWorkers"},
		{ToolSetP2PSplitSlowPath, map[string]any{"id": "w1", "allow": true}, "SetP2PSplitSlowPath"},
		{ToolSetP2PSplitNode, map[string]any{"id": "w1", "node_id": "n1"}, "SetP2PSplitNode"},
		{ToolSetNodeSplitServer, map[string]any{"node_id": "n1", "split_server": true}, "SetNodeSplitServer"},
		{ToolSetNodeLimits, map[string]any{"node_id": "n1", "model_swap": false}, "SetNodeLimits"},
		{ToolDrainNode, map[string]any{"node_id": "n1", "action": "drain", "timeout": "5m"}, "DrainNode"},
		{ToolSetModelMemoryPolicy, map[string]any{"model_swap": false, "max_active_backends": 2}, "SetModelMemoryPolicy"},
		{ToolPlugins, PluginsRequest{Action: "list_marketplaces"}, "ListPluginMarketplaces"},
		{ToolPlugins, PluginsRequest{Action: "add_marketplace", Source: "acme/plugins"}, "AddPluginMarketplace"},
		{ToolPlugins, PluginsRequest{Action: "refresh_marketplace", Marketplace: "acme"}, "RefreshPluginMarketplace"},
		{ToolPlugins, PluginsRequest{Action: "browse", Query: "fmt"}, "BrowsePlugins"},
		{ToolPlugins, PluginsRequest{Action: "list_installed"}, "ListPluginInstalls"},
		{ToolPlugins, PluginsRequest{Action: "install", Plugin: "fmt@acme"}, "InstallPlugin"},
		{ToolPlugins, PluginsRequest{Action: "check_updates", ID: "i1"}, "CheckPluginUpdate"},
		{ToolPlugins, PluginsRequest{Action: "check_updates"}, "ListPluginInstalls"},
		{ToolPlugins, PluginsRequest{Action: "enable", ID: "i1"}, "SetPluginEnabled"},
		{ToolPlugins, PluginsRequest{Action: "disable", ID: "i1"}, "SetPluginEnabled"},
		{ToolPlugins, PluginsRequest{Action: "remove", ID: "i1"}, "RemovePlugin"},
		{ToolGenerateImage, GenerateImageRequest{Prompt: "a fox"}, "GenerateImage"},
		{ToolGenerateImage, GenerateImageToolInput{Prompt: "a hat", Image: "a.png", Mask: "m.png"}, "EditImage"},
		{ToolSpeak, SpeakRequest{Text: "hello"}, "Speak"},
		{ToolTranscribeAudio, TranscribeAudioRequest{Audio: "a.wav"}, "TranscribeAudio"},
		{ToolDescribeImage, DescribeImageRequest{Image: "a.png"}, "DescribeImage"},
		{ToolEmbed, EmbedRequest{Input: []string{"x"}}, "Embed"},
		{ToolDetect, DetectRequest{Image: "a.png"}, "Detect"},
		{ToolRerank, RerankRequest{Query: "q", Documents: []string{"d"}}, "Rerank"},
		{ToolGenerateVideo, GenerateVideoRequest{Prompt: "waves"}, "GenerateVideo"},
		{ToolGenerateSound, GenerateSoundRequest{Text: "rain"}, "GenerateSound"},
		{ToolUpscaleImage, UpscaleImageRequest{Image: "a.png", Scale: 4}, "UpscaleImage"},
		{ToolTransformAudio, TransformAudioRequest{Audio: "a.wav"}, "TransformAudio"},
		{ToolVoice, VoiceToolInput{Action: "verify", Audio: "a.wav", Audio2: "b.wav"}, "VerifyVoice"},
		{ToolVoice, VoiceToolInput{Action: "identify", Audio: "a.wav"}, "IdentifyVoice"},
		{ToolFace, FaceToolInput{Action: "verify", Image: "a.png", Image2: "b.png"}, "VerifyFace"},
		{ToolFace, FaceToolInput{Action: "identify", Image: "a.png"}, "IdentifyFace"},
	}

	for _, c := range cases {
		c := c
		It("routes "+c.tool+" to "+c.wantMethod, func() {
			fc := &fakeClient{
				installModel:   func(InstallModelRequest) (string, error) { return "job-1", nil },
				installBackend: func(InstallBackendRequest) (string, error) { return "job-2", nil },
				upgradeBackend: func(string) (string, error) { return "job-3", nil },
				installLibraryBackend: func(InstallLibraryBackendRequest) (*InstallLibraryBackendResult, error) {
					return &InstallLibraryBackendResult{Status: "installed"}, nil
				},
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
		{"set_scheduling requires model_name", ToolSetScheduling, SetSchedulingRequest{}, "model_name is required"},
		{"set_scheduling rejects invalid replica range", ToolSetScheduling, SetSchedulingRequest{ModelName: "qwen", MinReplicas: 3, MaxReplicas: 1}, "min_replicas must be <= max_replicas"},
		{"set_scheduling rejects an unknown upgrade strategy", ToolSetScheduling, SetSchedulingRequest{ModelName: "qwen", UpgradeStrategy: new("rolling")}, "upgrade_strategy must be"},
		{"delete_scheduling requires model_name", ToolDeleteScheduling, DeleteSchedulingRequest{}, "model_name is required"},
		{"model_updates rejects an unknown action", ToolModelUpdates, map[string]any{"action": "apply"}, "action must be list or check"},
		{"upgrade_run_start refuses an add-on item", ToolUpgradeRunStart, UpgradeRunStartRequest{Items: []UpgradeRunItemRef{{Kind: "gallery", Name: "llama-cpp"}, {Kind: "addon", Name: "notes"}}}, "add-ons are not updated by the assistant"},
		{"upgrade_run_start requires items", ToolUpgradeRunStart, UpgradeRunStartRequest{}, "items is required"},
		{"upgrade_run_start rejects an unknown kind", ToolUpgradeRunStart, UpgradeRunStartRequest{Items: []UpgradeRunItemRef{{Kind: "plugin", Name: "x"}}}, "kind must be gallery, library or model"},
		{"upgrade_run_cancel requires an id", ToolUpgradeRunCancel, map[string]any{"id": ""}, "id is required"},
		{"upgrade_model requires a name", ToolUpgradeModel, UpgradeModelRequest{Action: "upgrade"}, "name is required"},
		{"upgrade_model rejects an unknown action", ToolUpgradeModel, UpgradeModelRequest{Action: "downgrade", Name: "tiny"}, "action must be one of"},
		{"upgrade_model takes choices only for an upgrade", ToolUpgradeModel, UpgradeModelRequest{Action: "hold", Name: "tiny", Choices: map[string]string{"k": "mine"}}, "choices apply only to upgrade and config_only"},
		{"upgrade_model takes a gallery only for adopt", ToolUpgradeModel, UpgradeModelRequest{Action: "upgrade", Name: "tiny", Gallery: "localai"}, "gallery and entry apply only to adopt"},
		{"create_shared_folder requires a name", ToolCreateSharedFolder, CreateSharedFolderRequest{Name: " "}, "name is required"},
		{"set_shared_folder_member rejects an unknown mode", ToolSetSharedFolderMember, SetSharedFolderMemberRequest{ID: 3, Member: "marta", Mode: "owner"}, "mode must be read or write"},
		{"remove_shared_folder_member requires a member", ToolRemoveSharedFolderMember, RemoveSharedFolderMemberRequest{ID: 3}, "id and member are required"},
		{"search_models needs a query unless sort is latest", ToolSearchModels, ModelSearchRequest{Sort: "likes"}, "query is required unless sort is latest"},
		{"plugins rejects an unknown action", ToolPlugins, PluginsRequest{Action: "update", ID: "i1"}, "action must be list_marketplaces"},
		{"plugins install needs the plugin", ToolPlugins, PluginsRequest{Action: "install"}, "action install needs plugin"},
		{"plugins install rejects an unknown scope", ToolPlugins, PluginsRequest{Action: "install", Plugin: "fmt@acme", Scope: "team"}, "scope must be user or instance"},
		{"plugins enable needs the install id", ToolPlugins, PluginsRequest{Action: "enable"}, "action enable needs id"},
		{"plugins add_marketplace needs the source", ToolPlugins, PluginsRequest{Action: "add_marketplace"}, "action add_marketplace needs source"},
		{"restore_shared_folder requires the rev", ToolRestoreSharedFolder, RestoreSharedFolderRequest{}, "rev is required"},
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
