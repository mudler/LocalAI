package xsysinfo

import (
	sigar "github.com/cloudfoundry/gosigar"
	"github.com/rs/zerolog/log"
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
	total, used, free, err := getSystemRAM()
	if err != nil {
		return nil, err
	}

	usagePercent := 0.0
	if total > 0 {
		usagePercent = float64(used) / float64(total) * 100
	}
	log.Debug().Uint64("total", total).Uint64("used", used).Uint64("free", free).Float64("usage_percent", usagePercent).Msg("System RAM Info")
	return &SystemRAMInfo{
		Total:        total,
		Used:         used,
		Free:         free,
		Available:    total - used,
		UsagePercent: usagePercent,
	}, nil
}

// getSystemRAM returns system RAM information using ghw
func getSystemRAM() (total, used, free uint64, err error) {
	mem := sigar.Mem{}

	if err := mem.GetIgnoringCGroups(); err != nil {
		return 0, 0, 0, err
	}

	return mem.Total, mem.ActualUsed, mem.ActualFree, nil
}
