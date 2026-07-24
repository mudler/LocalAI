package mcp

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
	"github.com/mudler/LocalAI/pkg/vram"
)

// stubClient is the minimum LocalAIClient impl needed to exercise the holder.
// It returns deterministic, non-zero values so we can assert tool dispatch.
type stubClient struct{}

func (stubClient) GallerySearch(_ context.Context, _ localaitools.GallerySearchQuery) ([]gallery.Metadata, error) {
	return []gallery.Metadata{{Name: "stub", Gallery: config.Gallery{Name: "stub-gallery"}}}, nil
}

func (stubClient) ListInstalledModels(_ context.Context, _ localaitools.Capability) ([]localaitools.InstalledModel, error) {
	return []localaitools.InstalledModel{{Name: "stub"}}, nil
}

func (stubClient) ListGalleries(_ context.Context) ([]config.Gallery, error) {
	return []config.Gallery{{Name: "stub-gallery", URL: "http://example"}}, nil
}

func (stubClient) GetJobStatus(_ context.Context, _ string) (*localaitools.JobStatus, error) {
	return &localaitools.JobStatus{ID: "stub", Processed: true}, nil
}

func (stubClient) GetModelConfig(_ context.Context, _ string) (*localaitools.ModelConfigView, error) {
	return &localaitools.ModelConfigView{Name: "stub"}, nil
}

func (stubClient) InstallModel(_ context.Context, _ localaitools.InstallModelRequest) (string, error) {
	return "stub-job", nil
}

func (stubClient) ImportModelURI(_ context.Context, _ localaitools.ImportModelURIRequest) (*localaitools.ImportModelURIResponse, error) {
	return &localaitools.ImportModelURIResponse{JobID: "stub-import"}, nil
}
func (stubClient) DeleteModel(_ context.Context, _ string) error { return nil }
func (stubClient) EditModelConfig(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
func (stubClient) ReloadModels(_ context.Context) error { return nil }
func (stubClient) LoadModel(_ context.Context, model string) ([]string, error) {
	return []string{model}, nil
}
func (stubClient) SetAlias(_ context.Context, _, _ string) error {
	return nil
}
func (stubClient) ListAliases(_ context.Context) ([]localaitools.AliasInfo, error) {
	return nil, nil
}
func (stubClient) ListBackends(_ context.Context) ([]localaitools.Backend, error) {
	return []localaitools.Backend{{Name: "stub-backend", Installed: true}}, nil
}

func (stubClient) ListKnownBackends(_ context.Context) ([]schema.KnownBackend, error) {
	return []schema.KnownBackend{}, nil
}

func (stubClient) InstallBackend(_ context.Context, _ localaitools.InstallBackendRequest) (string, error) {
	return "stub-backend-job", nil
}

func (stubClient) UpgradeBackend(_ context.Context, _ string) (string, error) {
	return "stub-upgrade-job", nil
}

func (stubClient) SystemInfo(_ context.Context) (*localaitools.SystemInfo, error) {
	return &localaitools.SystemInfo{Version: "stub"}, nil
}

func (stubClient) ListNodes(_ context.Context) ([]localaitools.Node, error) {
	return []localaitools.Node{}, nil
}

func (stubClient) SetNodeVRAMBudget(_ context.Context, _, _ string) error {
	return nil
}

func (stubClient) VRAMEstimate(_ context.Context, _ localaitools.VRAMEstimateRequest) (*vram.EstimateResult, error) {
	return &vram.EstimateResult{SizeDisplay: "stub"}, nil
}
func (stubClient) ToggleModelState(_ context.Context, _ string, _ modeladmin.Action) error {
	return nil
}
func (stubClient) ToggleModelPinned(_ context.Context, _ string, _ modeladmin.Action) error {
	return nil
}
func (stubClient) GetBranding(_ context.Context) (*localaitools.Branding, error) {
	return &localaitools.Branding{InstanceName: "LocalAI"}, nil
}

func (stubClient) SetBranding(_ context.Context, _ localaitools.SetBrandingRequest) (*localaitools.Branding, error) {
	return &localaitools.Branding{InstanceName: "LocalAI"}, nil
}

func (stubClient) ListVoiceProfiles(_ context.Context) ([]localaitools.VoiceProfile, error) {
	return []localaitools.VoiceProfile{}, nil
}

func (stubClient) CreateVoiceProfile(_ context.Context, _ localaitools.CreateVoiceProfileRequest) (*localaitools.VoiceProfile, error) {
	return &localaitools.VoiceProfile{Name: "stub-voice"}, nil
}

func (stubClient) DeleteVoiceProfile(_ context.Context, _ string) error {
	return nil
}

func (stubClient) GetUsageStats(_ context.Context, _ localaitools.UsageStatsQuery) (*localaitools.UsageStats, error) {
	return &localaitools.UsageStats{Viewer: localaitools.UsageViewer{ID: "stub", Name: "stub"}, Period: "month"}, nil
}

func (stubClient) GetPIIEvents(_ context.Context, _ localaitools.PIIEventsQuery) ([]localaitools.PIIEvent, error) {
	return nil, nil
}

func (stubClient) GetMiddlewareStatus(_ context.Context) (*localaitools.MiddlewareStatus, error) {
	return &localaitools.MiddlewareStatus{
		PII: localaitools.MiddlewarePIIStatus{
			EnabledGlobally: true,
			Models:          []localaitools.MiddlewarePIIModel{},
		},
	}, nil
}

func (stubClient) GetRouterDecisions(_ context.Context, _ localaitools.RouterDecisionsQuery) ([]localaitools.RouterDecision, error) {
	return []localaitools.RouterDecision{}, nil
}

var _ = Describe("LocalAIAssistantHolder", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("Initialize wires the in-memory server, exposes tools, and dispatches", func() {
		h := NewLocalAIAssistantHolder()
		Expect(h.Initialize(ctx, stubClient{}, localaitools.Options{})).To(Succeed())
		Expect(h.HasTools()).To(BeTrue())
		Expect(h.SystemPrompt()).ToNot(BeEmpty())

		exec := h.Executor()
		Expect(exec.HasTools()).To(BeTrue())

		out, err := exec.ExecuteTool(ctx, "list_installed_models", `{"capability":"chat"}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).ToNot(BeEmpty())
	})

	It("Initialize is exactly-once even under concurrent callers", func() {
		h := NewLocalAIAssistantHolder()

		// Concurrent Initialize calls — only one should actually wire the server.
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = h.Initialize(ctx, stubClient{}, localaitools.Options{})
			}()
		}
		wg.Wait()

		Expect(h.HasTools()).To(BeTrue())
	})

	It("methods are nil-safe on a nil holder", func() {
		var h *LocalAIAssistantHolder
		Expect(h.HasTools()).To(BeFalse())
		Expect(h.SystemPrompt()).To(BeEmpty())
		exec := h.Executor()
		// Nil-receiver Executor returns an empty LocalToolExecutor.
		Expect(exec).ToNot(BeNil())
		Expect(exec.HasTools()).To(BeFalse())
	})
})

func (stubClient) GetRouterCorpusStats(_ context.Context, routerModel string) (*localaitools.RouterCorpusStats, error) {
	return &localaitools.RouterCorpusStats{Router: routerModel, LabelCounts: map[string]int{}}, nil
}

func (stubClient) SeedRouterCorpus(_ context.Context, req localaitools.RouterCorpusSeedRequest) (*localaitools.RouterCorpusSeedResult, error) {
	return &localaitools.RouterCorpusSeedResult{Router: req.Router, LabelCounts: map[string]int{}}, nil
}

func (stubClient) ClearRouterCorpus(_ context.Context, routerModel string) (*localaitools.RouterCorpusClearResult, error) {
	return &localaitools.RouterCorpusClearResult{Router: routerModel}, nil
}
