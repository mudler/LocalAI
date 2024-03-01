package schema

import (
	gopsutil "github.com/shirou/gopsutil/v3/process"
)

type BackendMonitorRequest struct {
	Model string `json:"model" yaml:"model"`
}

type BackendMonitorResponse struct {
	MemoryInfo    *gopsutil.MemoryInfoStat
	MemoryPercent float32
	CPUPercent    float64
}

type TTSRequest struct {
	Model   string `json:"model" yaml:"model"`
	Input   string `json:"input" yaml:"input"`
	Backend string `json:"backend" yaml:"backend"`
}
