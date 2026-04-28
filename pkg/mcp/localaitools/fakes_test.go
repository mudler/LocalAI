package localaitools

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// fakeClient is a recording, configurable LocalAIClient for unit tests.
// Each method records the args it was called with and returns whatever the
// matching field on the struct is configured to return. Methods are guarded
// by a mutex so tests can run with -race.
type fakeClient struct {
	mu sync.Mutex

	// Recorded calls (in order).
	calls []fakeCall

	// Per-method overrides. Tests set these.
	gallerySearch       func(GallerySearchQuery) ([]GalleryModelHit, error)
	listInstalledModels func(string) ([]InstalledModel, error)
	listGalleries       func() ([]Gallery, error)
	getJobStatus        func(string) (*JobStatus, error)
	getModelConfig      func(string) (*ModelConfigView, error)
	installModel        func(InstallModelRequest) (string, error)
	importModelURI      func(ImportModelURIRequest) (*ImportModelURIResponse, error)
	deleteModel         func(string) error
	editModelConfig     func(string, map[string]any) error
	reloadModels        func() error
	listBackends        func() ([]Backend, error)
	listKnownBackends   func() ([]Backend, error)
	installBackend      func(InstallBackendRequest) (string, error)
	upgradeBackend      func(string) (string, error)
	systemInfo          func() (*SystemInfo, error)
	listNodes           func() ([]Node, error)
	vramEstimate        func(VRAMEstimateRequest) (*VRAMEstimate, error)
	toggleModelState    func(string, string) error
	toggleModelPinned   func(string, string) error
}

type fakeCall struct {
	method string
	args   any
}

func (f *fakeClient) record(method string, args any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{method: method, args: args})
}

func (f *fakeClient) recorded() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

var errNotConfigured = errors.New("fakeClient method not configured")

func (f *fakeClient) GallerySearch(_ context.Context, q GallerySearchQuery) ([]GalleryModelHit, error) {
	f.record("GallerySearch", q)
	if f.gallerySearch != nil {
		return f.gallerySearch(q)
	}
	return nil, nil
}

func (f *fakeClient) ListInstalledModels(_ context.Context, capability string) ([]InstalledModel, error) {
	f.record("ListInstalledModels", capability)
	if f.listInstalledModels != nil {
		return f.listInstalledModels(capability)
	}
	return nil, nil
}

func (f *fakeClient) ListGalleries(_ context.Context) ([]Gallery, error) {
	f.record("ListGalleries", nil)
	if f.listGalleries != nil {
		return f.listGalleries()
	}
	return nil, nil
}

func (f *fakeClient) GetJobStatus(_ context.Context, jobID string) (*JobStatus, error) {
	f.record("GetJobStatus", jobID)
	if f.getJobStatus != nil {
		return f.getJobStatus(jobID)
	}
	return nil, errNotConfigured
}

func (f *fakeClient) GetModelConfig(_ context.Context, name string) (*ModelConfigView, error) {
	f.record("GetModelConfig", name)
	if f.getModelConfig != nil {
		return f.getModelConfig(name)
	}
	return nil, errNotConfigured
}

func (f *fakeClient) InstallModel(_ context.Context, req InstallModelRequest) (string, error) {
	f.record("InstallModel", req)
	if f.installModel != nil {
		return f.installModel(req)
	}
	return "", errNotConfigured
}

func (f *fakeClient) DeleteModel(_ context.Context, name string) error {
	f.record("DeleteModel", name)
	if f.deleteModel != nil {
		return f.deleteModel(name)
	}
	return nil
}

func (f *fakeClient) ImportModelURI(_ context.Context, req ImportModelURIRequest) (*ImportModelURIResponse, error) {
	f.record("ImportModelURI", req)
	if f.importModelURI != nil {
		return f.importModelURI(req)
	}
	return &ImportModelURIResponse{JobID: "fake-import-job"}, nil
}

func (f *fakeClient) EditModelConfig(_ context.Context, name string, patch map[string]any) error {
	f.record("EditModelConfig", []any{name, patch})
	if f.editModelConfig != nil {
		return f.editModelConfig(name, patch)
	}
	return nil
}

func (f *fakeClient) ReloadModels(_ context.Context) error {
	f.record("ReloadModels", nil)
	if f.reloadModels != nil {
		return f.reloadModels()
	}
	return nil
}

func (f *fakeClient) ListBackends(_ context.Context) ([]Backend, error) {
	f.record("ListBackends", nil)
	if f.listBackends != nil {
		return f.listBackends()
	}
	return nil, nil
}

func (f *fakeClient) ListKnownBackends(_ context.Context) ([]Backend, error) {
	f.record("ListKnownBackends", nil)
	if f.listKnownBackends != nil {
		return f.listKnownBackends()
	}
	return nil, nil
}

func (f *fakeClient) InstallBackend(_ context.Context, req InstallBackendRequest) (string, error) {
	f.record("InstallBackend", req)
	if f.installBackend != nil {
		return f.installBackend(req)
	}
	return "", errNotConfigured
}

func (f *fakeClient) UpgradeBackend(_ context.Context, name string) (string, error) {
	f.record("UpgradeBackend", name)
	if f.upgradeBackend != nil {
		return f.upgradeBackend(name)
	}
	return "", errNotConfigured
}

func (f *fakeClient) SystemInfo(_ context.Context) (*SystemInfo, error) {
	f.record("SystemInfo", nil)
	if f.systemInfo != nil {
		return f.systemInfo()
	}
	return &SystemInfo{Version: "test"}, nil
}

func (f *fakeClient) ListNodes(_ context.Context) ([]Node, error) {
	f.record("ListNodes", nil)
	if f.listNodes != nil {
		return f.listNodes()
	}
	return nil, nil
}

func (f *fakeClient) VRAMEstimate(_ context.Context, req VRAMEstimateRequest) (*VRAMEstimate, error) {
	f.record("VRAMEstimate", req)
	if f.vramEstimate != nil {
		return f.vramEstimate(req)
	}
	return nil, errNotConfigured
}

func (f *fakeClient) ToggleModelState(_ context.Context, name, action string) error {
	f.record("ToggleModelState", []string{name, action})
	if f.toggleModelState != nil {
		return f.toggleModelState(name, action)
	}
	return nil
}

func (f *fakeClient) ToggleModelPinned(_ context.Context, name, action string) error {
	f.record("ToggleModelPinned", []string{name, action})
	if f.toggleModelPinned != nil {
		return f.toggleModelPinned(name, action)
	}
	return nil
}

// boom is a sentinel error used by tests that want a deterministic error string.
var boom = fmt.Errorf("boom")
