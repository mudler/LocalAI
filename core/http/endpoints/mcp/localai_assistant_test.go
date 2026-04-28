package mcp

import (
	"context"
	"sync"
	"testing"

	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
)

// stubClient is the minimum LocalAIClient impl needed to exercise the holder.
// It returns deterministic, non-zero values so we can assert tool dispatch.
type stubClient struct{}

func (stubClient) GallerySearch(_ context.Context, _ localaitools.GallerySearchQuery) ([]localaitools.GalleryModelHit, error) {
	return []localaitools.GalleryModelHit{{Name: "stub", Gallery: "stub-gallery"}}, nil
}
func (stubClient) ListInstalledModels(_ context.Context, _ localaitools.Capability) ([]localaitools.InstalledModel, error) {
	return []localaitools.InstalledModel{{Name: "stub"}}, nil
}
func (stubClient) ListGalleries(_ context.Context) ([]localaitools.Gallery, error) {
	return []localaitools.Gallery{{Name: "stub-gallery", URL: "http://example"}}, nil
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
func (stubClient) DeleteModel(_ context.Context, _ string) error  { return nil }
func (stubClient) EditModelConfig(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
func (stubClient) ReloadModels(_ context.Context) error { return nil }
func (stubClient) ListBackends(_ context.Context) ([]localaitools.Backend, error) {
	return []localaitools.Backend{{Name: "stub-backend", Installed: true}}, nil
}
func (stubClient) ListKnownBackends(_ context.Context) ([]localaitools.Backend, error) {
	return []localaitools.Backend{}, nil
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
func (stubClient) VRAMEstimate(_ context.Context, _ localaitools.VRAMEstimateRequest) (*localaitools.VRAMEstimate, error) {
	return &localaitools.VRAMEstimate{ModelName: "stub"}, nil
}
func (stubClient) ToggleModelState(_ context.Context, _, _ string) error  { return nil }
func (stubClient) ToggleModelPinned(_ context.Context, _, _ string) error { return nil }

func TestLocalAIAssistantHolder_HappyPath(t *testing.T) {
	h := NewLocalAIAssistantHolder()
	ctx := context.Background()

	if err := h.Initialize(ctx, stubClient{}, localaitools.Options{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if !h.HasTools() {
		t.Fatalf("HasTools() = false after init")
	}
	if h.SystemPrompt() == "" {
		t.Errorf("SystemPrompt() empty")
	}

	exec := h.Executor()
	if !exec.HasTools() {
		t.Fatalf("executor reports no tools")
	}

	out, err := exec.ExecuteTool(ctx, "list_installed_models", `{"capability":"chat"}`)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if out == "" {
		t.Errorf("expected non-empty result")
	}
}

func TestLocalAIAssistantHolder_InitializeIsOnce(t *testing.T) {
	h := NewLocalAIAssistantHolder()
	ctx := context.Background()

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

	if !h.HasTools() {
		t.Fatalf("HasTools() = false after concurrent init")
	}
}

func TestLocalAIAssistantHolder_NilSafe(t *testing.T) {
	var h *LocalAIAssistantHolder // nil
	if h.HasTools() {
		t.Errorf("nil holder should report HasTools() = false")
	}
	if h.SystemPrompt() != "" {
		t.Errorf("nil holder should report empty SystemPrompt()")
	}
	if !isEmptyExecutor(h.Executor()) {
		t.Errorf("nil holder should produce empty executor")
	}
}

func isEmptyExecutor(e ToolExecutor) bool {
	return e == nil || !e.HasTools()
}
