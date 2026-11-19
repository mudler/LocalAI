//go:build !windows

package xsysinfo

// platformGPUMemory lists the GPUs the operating system itself reports. Only
// Windows has such a source (DXGI); elsewhere the vendor tools and sysfs in
// gpu.go cover detection.
func platformGPUMemory() []GPUMemoryInfo { return nil }
