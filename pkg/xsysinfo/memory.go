package xsysinfo

import (
	"os"

	"github.com/mudler/memory"
)

// cgroup/proc paths used to make the reported RAM total container-aware.
// They are variables (not consts) so tests could override them if needed.
var (
	cgroupV2MaxPath   = "/sys/fs/cgroup/memory.max"
	cgroupV1LimitPath = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	procMemInfoPath   = "/proc/meminfo"
)

// SystemRAMInfo contains system RAM usage information
type SystemRAMInfo struct {
	Total        uint64  `json:"total"`
	Used         uint64  `json:"used"`
	Free         uint64  `json:"free"`
	Available    uint64  `json:"available"`
	UsagePercent float64 `json:"usage_percent"`
}

// readFileBestEffort reads a file and returns its contents, or "" on any error.
// Missing cgroup/proc files (e.g. on non-Linux hosts) are expected and benign.
func readFileBestEffort(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// systemTotalMemory returns the container-aware total system RAM in bytes.
//
// memory.TotalMemory() reports the HOST kernel total (syscall.Sysinfo on
// Linux), which lxcfs/LXD does NOT virtualize. Inside a container that
// over-reports physical RAM and, combined with the virtualized MemAvailable,
// inflates the reported usage (see issue #8059). We instead derive the total
// from the minimum of all available container-aware candidates.
func systemTotalMemory() uint64 {
	return chooseTotalMemory(
		readFileBestEffort(cgroupV2MaxPath),
		readFileBestEffort(cgroupV1LimitPath),
		readFileBestEffort(procMemInfoPath),
		memory.TotalMemory(),
	)
}

// GetSystemRAMInfo returns real-time system RAM usage
func GetSystemRAMInfo() (*SystemRAMInfo, error) {
	total := systemTotalMemory()
	available := memory.AvailableMemory()

	// AvailableMemory (MemAvailable) is virtualized by lxcfs, so in edge
	// cases it can exceed our corrected total; clamp to avoid an unsigned
	// underflow when computing Used.
	if available > total {
		available = total
	}

	used := total - available

	usagePercent := 0.0
	if total > 0 {
		usagePercent = float64(used) / float64(total) * 100
	}
	return &SystemRAMInfo{
		Total:        total,
		Used:         used,
		Free:         available,
		Available:    available,
		UsagePercent: usagePercent,
	}, nil
}
