package xsysinfo

import (
	"github.com/mudler/memory"
	"github.com/mudler/xlog"
)

// SystemRAMInfo contains system RAM usage information
type SystemRAMInfo struct {
	Total        uint64  `json:"total"`
	Used         uint64  `json:"used"`
	Free         uint64  `json:"free"`
	Available    uint64  `json:"available"`
	UsagePercent float64 `json:"usage_percent"`
}

// GetSystemRAMInfo returns real-time system RAM usage
func GetSystemRAMInfo() (*SystemRAMInfo, error) {
	total := memory.TotalMemory()
	free := memory.AvailableMemory()

	used := total - free

	usagePercent := 0.0
	if total > 0 {
		usagePercent = float64(used) / float64(total) * 100
	}
	xlog.Debug("System RAM Info", "total", total, "used", used, "free", free, "usage_percent", usagePercent)
	return &SystemRAMInfo{
		Total:        total,
		Used:         used,
		Free:         free,
		Available:    total - used,
		UsagePercent: usagePercent,
	}, nil
}
