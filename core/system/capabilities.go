package system

import (
	"os"
	"strings"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"
)

type SystemState struct {
	GPUVendor string
}

func (s *SystemState) Capability() string {
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
