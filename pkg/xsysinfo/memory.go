package xsysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"github.com/mudler/memory"
)

// SystemRAMInfo contains system RAM usage information
type SystemRAMInfo struct {
	Total        uint64  `json:"total"`
	Used         uint64  `json:"used"`
	Free         uint64  `json:"free"`
	Available    uint64  `json:"available"`
	UsagePercent float64 `json:"usage_percent"`
}

// readMemInfo reads a specific memory value from /proc/meminfo
// This gives container-aware memory values in containers (LXD, Docker, etc.)
func readMemInfo(key string) (uint64, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, key+":") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				val, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil {
					return 0, false
				}
				// Value in /proc/meminfo is in kB, convert to bytes
				return val * 1024, true
			}
		}
	}
	return 0, false
}

// getTotalMemory reads total memory from /proc/meminfo
// This is container-aware and respects cgroup limits
func getTotalMemory() uint64 {
	if val, ok := readMemInfo("MemTotal"); ok {
		return val
	}
	// Fallback to the memory library
	return memory.TotalMemory()
}

// getAvailableMemory reads available memory from /proc/meminfo
// This is container-aware and respects cgroup limits
func getAvailableMemory() uint64 {
	if val, ok := readMemInfo("MemAvailable"); ok {
		return val
	}
	// Fallback to the memory library
	return memory.AvailableMemory()
}

// GetSystemRAMInfo returns real-time system RAM usage
// Uses /proc/meminfo for container-aware memory detection
func GetSystemRAMInfo() (*SystemRAMInfo, error) {
	// Use /proc/meminfo for container-aware values
	total := getTotalMemory()
	free := memory.FreeMemory() // Free memory is typically the same
	available := getAvailableMemory()

	used := total - available

	usagePercent := 0.0
	if total > 0 {
		usagePercent = float64(used) / float64(total) * 100
	}

	return &SystemRAMInfo{
		Total:        total,
		Used:         used,
		Free:         free,
		Available:    available,
		UsagePercent: usagePercent,
	}, nil
}
