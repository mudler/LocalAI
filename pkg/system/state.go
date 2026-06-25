package system

import (
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

type Backend struct {
	BackendsPath       string
	BackendsSystemPath string
}

type Model struct {
	ModelsPath string
}

type SystemState struct {
	GPUVendor string
	Backend   Backend
	Model     Model
	VRAM      uint64

	systemCapabilities string

	// Backend image fallback tag configuration
	BackendImagesReleaseTag string
	BackendImagesBranchTag  string
	BackendDevSuffix        string
	// PreferDevelopmentBackends installs the development image as the primary
	// backend URI (the released image becomes a fallback) rather than only using
	// development as a download fallback when the released image is missing.
	PreferDevelopmentBackends bool
}

type SystemStateOptions func(*SystemState)

func WithBackendPath(path string) SystemStateOptions {
	return func(s *SystemState) {
		s.Backend.BackendsPath = path
	}
}

func WithBackendSystemPath(path string) SystemStateOptions {
	return func(s *SystemState) {
		s.Backend.BackendsSystemPath = path
	}
}

func WithModelPath(path string) SystemStateOptions {
	return func(s *SystemState) {
		s.Model.ModelsPath = path
	}
}

func WithBackendImagesReleaseTag(tag string) SystemStateOptions {
	return func(s *SystemState) {
		s.BackendImagesReleaseTag = tag
	}
}

func WithBackendImagesBranchTag(tag string) SystemStateOptions {
	return func(s *SystemState) {
		s.BackendImagesBranchTag = tag
	}
}

func WithBackendDevSuffix(suffix string) SystemStateOptions {
	return func(s *SystemState) {
		s.BackendDevSuffix = suffix
	}
}

func WithPreferDevelopmentBackends(prefer bool) SystemStateOptions {
	return func(s *SystemState) {
		s.PreferDevelopmentBackends = prefer
	}
}

func GetSystemState(opts ...SystemStateOptions) (*SystemState, error) {
	state := &SystemState{}
	for _, opt := range opts {
		opt(state)
	}

	// Detection is best-effort here, we don't want to fail if it fails
	state.GPUVendor, _ = xsysinfo.DetectGPUVendor()
	xlog.Debug("GPU vendor", "gpuVendor", state.GPUVendor)
	state.VRAM, _ = xsysinfo.TotalAvailableVRAM()
	xlog.Debug("Total available VRAM", "vram", state.VRAM)

	state.getSystemCapabilities()

	return state, nil
}
