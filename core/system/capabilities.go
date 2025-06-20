package system

import (
	"strings"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
)

type SystemState struct {
	GPUVendor string
}

func GetSystemState() (*SystemState, error) {
	gpuVendor, err := detectGPUVendor()
	if err != nil {
		return nil, err
	}
	return &SystemState{GPUVendor: gpuVendor}, nil
}

func detectGPUVendor() (string, error) {
	gpus, err := xsysinfo.GPUs()
	if err != nil {
		return "", err
	}

	for _, gpu := range gpus {
		if strings.ToUpper(gpu.DeviceInfo.Vendor.Name) == "NVIDIA" {
			return "nvidia", nil
		}
		if strings.ToUpper(gpu.DeviceInfo.Vendor.Name) == "AMD" {
			return "amd", nil
		}
		if strings.ToUpper(gpu.DeviceInfo.Vendor.Name) == "INTEL" {
			return "intel", nil
		}
	}

	return "", nil
}
