package xsysinfo

import (
	"strconv"
	"strings"
)

// This file holds the platform-neutral part of the Windows GPU detection, so
// it can be tested on any host. gpu_windows.go fills in the adapter list from
// DXGI and the memory usage from the "GPU Adapter Memory" performance
// counters.

// osAdapter is one display adapter as the operating system reports it.
type osAdapter struct {
	Name     string
	VendorID uint32
	// LUID identifies the adapter in the performance counters.
	LUID uint64
	// DedicatedVideoMemory is the VRAM of the adapter, in bytes.
	DedicatedVideoMemory uint64
	// SharedSystemMemory is the system RAM the adapter can use, in bytes.
	SharedSystemMemory uint64
	// Software marks the Microsoft Basic Render Driver and other software
	// adapters, which are not GPUs.
	Software bool
}

// osAdapterUsage is the memory in use on one adapter, in bytes.
type osAdapterUsage struct {
	Dedicated uint64
	Shared    uint64
}

// integratedDedicatedLimit is the dedicated memory size below which an
// adapter counts as integrated. An integrated GPU reports a small carve-out
// (often 128 MiB) as dedicated memory and does its real work in shared
// system memory, like a unified-memory device.
const integratedDedicatedLimit = 512 * 1024 * 1024

// pciVendor maps a PCI vendor ID to the vendor names of this package. It
// returns "" for an unknown vendor and for Microsoft (0x1414), whose adapters
// are software renderers.
func pciVendor(vendorID uint32) string {
	switch vendorID {
	case 0x10de:
		return VendorNVIDIA
	case 0x1002, 0x1022:
		return VendorAMD
	case 0x8086:
		return VendorIntel
	}
	return ""
}

// adapterGPUInfos turns OS adapters and their memory usage into
// GPUMemoryInfo values. usage may be nil when the counters are not
// available, or miss an adapter. The memory in use is then unknown: the
// adapter reports its total with UsageUnknown set, and no used or free
// memory. Free as total would tell the scheduler that a busy GPU is empty.
func adapterGPUInfos(adapters []osAdapter, usage map[uint64]osAdapterUsage) []GPUMemoryInfo {
	var out []GPUMemoryInfo
	for _, a := range adapters {
		vendor := pciVendor(a.VendorID)
		if a.Software || vendor == "" {
			continue
		}
		u, known := usage[a.LUID]
		total := a.DedicatedVideoMemory
		used := u.Dedicated
		if total < integratedDedicatedLimit && a.SharedSystemMemory > total {
			total = a.SharedSystemMemory
			used = u.Shared
		}
		if total == 0 {
			continue
		}
		if !known {
			out = append(out, GPUMemoryInfo{
				Index:        len(out),
				Name:         a.Name,
				Vendor:       vendor,
				TotalVRAM:    total,
				UsageUnknown: true,
			})
			continue
		}
		if used > total {
			used = total
		}
		out = append(out, GPUMemoryInfo{
			Index:        len(out),
			Name:         a.Name,
			Vendor:       vendor,
			TotalVRAM:    total,
			UsedVRAM:     used,
			FreeVRAM:     total - used,
			UsagePercent: float64(used) / float64(total) * 100,
		})
	}
	return out
}

// parseAdapterCounterInstance reads the adapter LUID from an instance name of
// the "GPU Adapter Memory" counter set, for example
// "luid_0x00000000_0x0000D1B5_phys_0". The first number is the high part of
// the LUID and the second the low part.
func parseAdapterCounterInstance(name string) (uint64, bool) {
	parts := strings.Split(strings.ToLower(name), "_")
	if len(parts) < 3 || parts[0] != "luid" {
		return 0, false
	}
	high, err := strconv.ParseUint(strings.TrimPrefix(parts[1], "0x"), 16, 32)
	if err != nil {
		return 0, false
	}
	low, err := strconv.ParseUint(strings.TrimPrefix(parts[2], "0x"), 16, 32)
	if err != nil {
		return 0, false
	}
	return high<<32 | low, true
}

// luidKey builds the key parseAdapterCounterInstance returns from the two
// halves of a Windows LUID.
func luidKey(low uint32, high int32) uint64 {
	return uint64(uint32(high))<<32 | uint64(low)
}

// mergeOSAdapters adds the OS-reported GPUs of vendors that the vendor tools
// did not report. A vendor tool (nvidia-smi) gives better figures, so its
// entries win.
func mergeOSAdapters(gpus, osGPUs []GPUMemoryInfo) []GPUMemoryInfo {
	for _, g := range osGPUs {
		if gpuListHasVendor(gpus, g.Vendor) {
			continue
		}
		g.Index = len(gpus)
		gpus = append(gpus, g)
	}
	return gpus
}

func gpuListHasVendor(gpus []GPUMemoryInfo, vendor string) bool {
	for _, g := range gpus {
		if g.Vendor == vendor {
			return true
		}
	}
	return false
}

// vendorFromAdapterInfos picks the vendor with the same priority as
// DetectGPUVendor: NVIDIA, then AMD, then Intel.
func vendorFromAdapterInfos(gpus []GPUMemoryInfo) string {
	for _, v := range []string{VendorNVIDIA, VendorAMD, VendorIntel} {
		if gpuListHasVendor(gpus, v) {
			return v
		}
	}
	return ""
}
