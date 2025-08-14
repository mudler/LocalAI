package system

import (
	"github.com/jaypipes/ghw/pkg/gpu"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"
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
	gpus      []*gpu.GraphicsCard
	VRAM      uint64
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

func GetSystemState(opts ...SystemStateOptions) (*SystemState, error) {
	state := &SystemState{}
	for _, opt := range opts {
		opt(state)
	}

	// Detection is best-effort here, we don't want to fail if it fails
	state.gpus, _ = xsysinfo.GPUs()
	log.Debug().Any("gpus", state.gpus).Msg("GPUs")
	state.GPUVendor, _ = detectGPUVendor(state.gpus)
	log.Debug().Str("gpuVendor", state.GPUVendor).Msg("GPU vendor")
	state.VRAM, _ = xsysinfo.TotalAvailableVRAM()
	log.Debug().Any("vram", state.VRAM).Msg("Total available VRAM")

	return state, nil
}
