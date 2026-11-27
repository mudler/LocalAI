package xsysinfo

import (
	"os"
	"regexp"
	"strings"
)

// Memory kinds of a host, as the model editor's quick setup names them
// (pkg/enginesettings MemoryKind has the same values).
const (
	// MemoryKindDiscrete: a GPU with its own memory.
	MemoryKindDiscrete = "discrete"
	// MemoryKindUnified: a GPU that shares the system memory.
	MemoryKindUnified = "unified"
	// MemoryKindSystem: no GPU; models run on the CPU.
	MemoryKindSystem = "system"
)

// HostMemory says how the GPU of this host reaches its memory.
type HostMemory struct {
	// Kind is one of the MemoryKind constants.
	Kind string `json:"kind"`
	// Device is the name of the GPU the kind was read from. Empty without one.
	Device string `json:"device,omitempty"`
	// Total is the memory a model can use, in bytes: the smallest GPU's
	// memory on a discrete host, the system memory otherwise. 0 when unknown.
	Total uint64 `json:"total,omitempty"`
}

// unifiedNamePatterns match GPU names that share the system memory and do
// not say so through their driver. Matched without case.
var unifiedNamePatterns = []*regexp.Regexp{
	// NVIDIA: GB10 (DGX Spark), Thor, and the Jetson modules, which report
	// their SoC name.
	regexp.MustCompile(`(?i)\bGB10\b|\bthor\b|\borin\b|\bxavier\b|tegra|jetson`),
	// AMD APUs: the Radeon iGPUs of Ryzen (Strix Halo 8060S/8050S, Strix
	// 890M/880M, Phoenix 780M/760M/740M, Rembrandt 680M/660M) and the names
	// some drivers report for them.
	regexp.MustCompile(`(?i)radeon\s+(8060S|8050S|8040S|890M|880M|860M|840M|780M|760M|740M|680M|660M|610M)\b|ryzen ai|strix|\bgfx115[01]\b|radeon\(tm\) graphics|radeon graphics`),
	// Intel integrated graphics. A discrete Arc card has a model number
	// (Arc A770, Arc B580), the integrated Arc of Meteor Lake and later
	// does not.
	regexp.MustCompile(`(?i)uhd graphics|iris|intel\(r\) graphics|^intel graphics|arc\(tm\) graphics|arc graphics`),
}

// isSharedMemoryGPU reports whether a GPU shares the system memory.
func isSharedMemoryGPU(g GPUMemoryInfo) bool {
	if g.Vendor == VendorApple || g.SharedMemory || isUnifiedMemoryDevice(g.Name) {
		return true
	}
	for _, re := range unifiedNamePatterns {
		if re.MatchString(g.Name) {
			return true
		}
	}
	return false
}

// ClassifyMemory returns the memory kind of a host from its GPUs, and the
// GPU it was read from. tegra is true on an NVIDIA Jetson, whose GPU may
// not report a name. A discrete GPU wins over an integrated one: llama.cpp
// uses an integrated GPU only when there is no discrete one.
func ClassifyMemory(gpus []GPUMemoryInfo, tegra bool) (kind, device string) {
	var shared *GPUMemoryInfo
	for i := range gpus {
		if isSharedMemoryGPU(gpus[i]) {
			if shared == nil {
				shared = &gpus[i]
			}
			continue
		}
		return MemoryKindDiscrete, gpus[i].Name
	}
	switch {
	case shared != nil:
		return MemoryKindUnified, shared.Name
	case tegra:
		return MemoryKindUnified, "NVIDIA Jetson"
	}
	return MemoryKindSystem, ""
}

// isTegra reports whether this is an NVIDIA Jetson: L4T writes
// /etc/nv_tegra_release, and the device tree names the board.
func isTegra() bool {
	if _, err := os.Stat("/etc/nv_tegra_release"); err == nil {
		return true
	}
	model := readFileBestEffort("/proc/device-tree/model")
	return strings.Contains(strings.ToLower(model), "jetson")
}

// DetectHostMemory reads the memory kind of this host. A unified or CPU
// host's total is the system memory; a discrete host's the smallest GPU's.
func DetectHostMemory() HostMemory { return HostMemoryOf(GetGPUMemoryUsage()) }

// HostMemoryOf is DetectHostMemory for GPUs already read, so a caller that
// has the list does not run the vendor tools again.
func HostMemoryOf(gpus []GPUMemoryInfo) HostMemory {
	kind, device := ClassifyMemory(gpus, isTegra())
	out := HostMemory{Kind: kind, Device: device}
	if kind == MemoryKindDiscrete {
		var discrete []GPUMemoryInfo
		for _, g := range gpus {
			if !isSharedMemoryGPU(g) {
				discrete = append(discrete, g)
			}
		}
		out.Total = minNonZeroVRAM(discrete)
		return out
	}
	if ram, err := GetSystemRAMInfo(); err == nil && ram != nil {
		out.Total = ram.Total
	}
	return out
}
