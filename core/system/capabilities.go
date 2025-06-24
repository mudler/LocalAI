package system

import (
	"strings"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"
)

type SystemState struct {
	GPUVendor string
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
				if gpuVendorName == "NVIDIA" {
					return "nvidia", nil
				}
				if gpuVendorName == "AMD" {
					return "amd", nil
				}
				if gpuVendorName == "INTEL" {
					return "intel", nil
				}
				return "nvidia", nil
			}
		}

	}

	return "", nil
}
