package system

import (
	"os"
	"runtime"
	"strings"

	"github.com/jaypipes/ghw/pkg/gpu"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"
)

type SystemState struct {
	GPUVendor string
	gpus      []*gpu.GraphicsCard
	VRAM      uint64
}

const (
	defaultCapability = "default"
	nvidiaL4T         = "nvidia-l4t"
	darwinX86         = "darwin-x86"
	metal             = "metal"
	nvidia            = "nvidia"
	amd               = "amd"
	intel             = "intel"

	capabilityEnv        = "LOCALAI_FORCE_META_BACKEND_CAPABILITY"
	capabilityRunFileEnv = "LOCALAI_FORCE_META_BACKEND_CAPABILITY_RUN_FILE"
	defaultRunFile       = "/run/localai/capability"
)

func (s *SystemState) Capability(capMap map[string]string) string {
	reportedCapability := s.getSystemCapabilities()

	// Check if the reported capability is in the map
	if _, exists := capMap[reportedCapability]; exists {
		log.Debug().Str("reportedCapability", reportedCapability).Any("capMap", capMap).Msg("Using reported capability")
		return reportedCapability
	}

	log.Debug().Str("reportedCapability", reportedCapability).Any("capMap", capMap).Msg("The requested capability was not found, using default capability")
	// Otherwise, return the default capability (catch-all)
	return defaultCapability
}

func (s *SystemState) getSystemCapabilities() string {
	capability := os.Getenv(capabilityEnv)
	if capability != "" {
		log.Info().Str("capability", capability).Msgf("Using forced capability from environment variable (%s)", capabilityEnv)
		return capability
	}

	capabilityRunFile := defaultRunFile
	capabilityRunFileEnv := os.Getenv(capabilityRunFileEnv)
	if capabilityRunFileEnv != "" {
		capabilityRunFile = capabilityRunFileEnv
	}

	// Check if /run/localai/capability exists and use it
	// This might be used by e.g. container images to specify which
	// backends to pull in automatically when installing meta backends.
	if _, err := os.Stat(capabilityRunFile); err == nil {
		capability, err := os.ReadFile(capabilityRunFile)
		if err == nil {
			log.Info().Str("capabilityRunFile", capabilityRunFile).Str("capability", string(capability)).Msgf("Using forced capability run file (%s)", capabilityRunFileEnv)
			return strings.Trim(strings.TrimSpace(string(capability)), "\n")
		}
	}

	// If we are on mac and arm64, we will return metal
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		log.Info().Msgf("Using metal capability (arm64 on mac), set %s to override", capabilityEnv)
		return metal
	}

	// If we are on mac and x86, we will return darwin-x86
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		log.Info().Msgf("Using darwin-x86 capability (amd64 on mac), set %s to override", capabilityEnv)
		return darwinX86
	}

	// If arm64 on linux and a nvidia gpu is detected, we will return nvidia-l4t
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		if s.GPUVendor == "nvidia" {
			log.Info().Msgf("Using nvidia-l4t capability (arm64 on linux), set %s to override", capabilityEnv)
			return nvidiaL4T
		}
	}

	if s.GPUVendor == "" {
		log.Info().Msgf("Default capability (no GPU detected), set %s to override", capabilityEnv)
		return defaultCapability
	}

	log.Info().Str("Capability", s.GPUVendor).Msgf("Capability automatically detected, set %s to override", capabilityEnv)
	// If vram is less than 4GB, let's default to CPU but warn the user that they can override that via env
	if s.VRAM <= 4*1024*1024*1024 {
		log.Warn().Msgf("VRAM is less than 4GB, defaulting to CPU. Set %s to override", capabilityEnv)
		return defaultCapability
	}

	return s.GPUVendor
}

func GetSystemState() (*SystemState, error) {
	// Detection is best-effort here, we don't want to fail if it fails
	gpus, _ := xsysinfo.GPUs()
	log.Debug().Any("gpus", gpus).Msg("GPUs")
	gpuVendor, _ := detectGPUVendor(gpus)
	log.Debug().Str("gpuVendor", gpuVendor).Msg("GPU vendor")
	vram, _ := xsysinfo.TotalAvailableVRAM()
	log.Debug().Any("vram", vram).Msg("Total available VRAM")

	return &SystemState{
		GPUVendor: gpuVendor,
		gpus:      gpus,
		VRAM:      vram,
	}, nil
}

func detectGPUVendor(gpus []*gpu.GraphicsCard) (string, error) {
	for _, gpu := range gpus {
		if gpu.DeviceInfo != nil {
			if gpu.DeviceInfo.Vendor != nil {
				gpuVendorName := strings.ToUpper(gpu.DeviceInfo.Vendor.Name)
				if strings.Contains(gpuVendorName, "NVIDIA") {
					return nvidia, nil
				}
				if strings.Contains(gpuVendorName, "AMD") {
					return amd, nil
				}
				if strings.Contains(gpuVendorName, "INTEL") {
					return intel, nil
				}
			}
		}
	}

	return "", nil
}
