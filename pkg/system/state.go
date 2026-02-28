package system

import (
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

type Backend struct {
	BackendsPath           string
	BackendsSystemPath     string
	BackendImagesReleaseTag string // Release tag for backend images
	BackendImagesBranchTag  string // Branch tag for backend images
	BackendDevSuffix        string // Development suffix for backend images
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
		s.Backend.BackendImagesReleaseTag = tag
	}
}

func WithBackendImagesBranchTag(tag string) SystemStateOptions {
	return func(s *SystemState) {
		s.Backend.BackendImagesBranchTag = tag
	}
}

func WithBackendDevSuffix(suffix string) SystemStateOptions {
	return func(s *SystemState) {
		s.Backend.BackendDevSuffix = suffix
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
