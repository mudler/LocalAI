//go:build !arm && !arm64
// +build !arm,!arm64

package xsysinfo

import (
	"fmt"
	"log"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

type GPUInfo struct {
	DeviceName        string  `json:"device_name"`
	TotalMemory       uint64  `json:"total_memory"`
	FreeMemory        uint64  `json:"free_memory"`
	UsedMemory        uint64  `json:"used_memory"`
	MemoryUtilization float32 `json:"memory_utilization"`
}

// GetNvidiaGpuInfo uses pkg nvml is a go binding around C API provided by libnvidia-ml.so
// to fetch GPU stats
func GetNvidiaGpuInfo() ([]GPUInfo, error) {
	// Initialize NVML
	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		log.Fatalf("Failed to initialize NVML: %v", nvml.ErrorString(ret))
		return nil, fmt.Errorf("failed to Initialzie device: %v", nvml.ErrorString(ret))
	}
	defer nvml.Shutdown()

	// Get the number of devices (GPUs)
	deviceCount, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		log.Fatalf("Failed to get device count: %v", nvml.ErrorString(ret))
	}

	var gpus []GPUInfo
	// Loop over each device (GPU) and print its VRAM information
	for i := 0; i < deviceCount; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			log.Fatalf("Failed to get device at index %d: %v", i, nvml.ErrorString(ret))
		}

		// Get device name
		deviceName, result := device.GetName()
		if result != nvml.SUCCESS {
			return nil, fmt.Errorf("failed to get device name at index %d: %v", i, nvml.ErrorString(result))
		}

		// Get memory information
		memoryInfo, result := device.GetMemoryInfo_v2()
		if result != nvml.SUCCESS {
			return nil, fmt.Errorf("failed to get memory info for device %s: %v", deviceName, nvml.ErrorString(result))
		}

		// Get memory utilization
		utilization, result := device.GetUtilizationRates()
		if result != nvml.SUCCESS {
			return nil, fmt.Errorf("failed to get utilization info for device %s: %v", deviceName, nvml.ErrorString(result))
		}

		// Append GPU information to the list
		gpus = append(gpus, GPUInfo{
			DeviceName:        deviceName,
			TotalMemory:       memoryInfo.Total / (1024 * 1024), // Convert bytes to MiB
			FreeMemory:        memoryInfo.Free / (1024 * 1024),  // Convert bytes to MiB
			UsedMemory:        memoryInfo.Used / (1024 * 1024),  // Convert bytes to MiB
			MemoryUtilization: float32(utilization.Memory),      // Memory utilization in percentage
		})
	}

	return gpus, nil
}
