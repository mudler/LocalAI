package system

import (
	"os"
	"runtime"
	"strings"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"
)

type SystemState struct {
	GPUVendor string
}

const (
	defaultCapability = "default"
	nvidiaL4T         = "nvidia-l4t"
	darwinX86         = "darwin-x86"
	metal             = "metal"
)

func (s *SystemState) Capability(capMap map[string]string) string {
	reportedCapability := s.getSystemCapabilities()

	// Check if the reported capability is in the map
	if _, exists := capMap[reportedCapability]; exists {
		return reportedCapability
	}

	// Otherwise, return the default capability (catch-all)
	return defaultCapability
}

func (s *SystemState) getSystemCapabilities() string {
	if os.Getenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY") != "" {
		return os.Getenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY")
	}

	capabilityRunFile := "/run/localai/capability"
	if os.Getenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY_RUN_FILE") != "" {
		capabilityRunFile = os.Getenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY_RUN_FILE")
	}

	// Check if /run/localai/capability exists and use it
	// This might be used by e.g. container images to specify which
	// backends to pull in automatically when installing meta backends.
	if _, err := os.Stat(capabilityRunFile); err == nil {
		capability, err := os.ReadFile(capabilityRunFile)
		if err == nil {
			return string(capability)
		}
	}

	// If we are on mac and arm64, we will return metal
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return metal
	}

	// If we are on mac and x86, we will return darwin-x86
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		return darwinX86
	}

	// If arm64 on linux and a nvidia gpu is detected, we will return nvidia-l4t
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		if s.GPUVendor == "nvidia" {
			return nvidiaL4T
		}
	}

	if s.GPUVendor == "" {
		return defaultCapability
	}

	return s.GPUVendor
}

func GetSystemState() (*SystemState, error) {
	gpuVendor, _ := detectGPUVendor()
	log.Debug().Str("gpuVendor", gpuVendor).Msg("GPU vendor")

	return &SystemState{
		GPUVendor: gpuVendor,
	}, nil
}

func detectGPUVendor() (string, error) {
	gpus, err := xsysinfo.GPUs()
	if err != nil {
		return "", err
	}

	for _, gpu := range gpus {
		if gpu.DeviceInfo != nil {
			if gpu.DeviceInfo.Vendor != nil {
				gpuVendorName := strings.ToUpper(gpu.DeviceInfo.Vendor.Name)
				if strings.Contains(gpuVendorName, "NVIDIA") {
					return "nvidia", nil
				}
				if strings.Contains(gpuVendorName, "AMD") {
					return "amd", nil
				}
				if strings.Contains(gpuVendorName, "INTEL") {
					return "intel", nil
				}
				return "nvidia", nil
			}
		}

	}

	return "", nil
}
