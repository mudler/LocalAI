package xsysinfo

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/jaypipes/ghw"
	"github.com/klauspost/cpuid/v2"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/load"
)

// CPUInfo describes host-wide CPU capacity and current utilization.
type CPUInfo struct {
	LogicalCores uint64
	UsagePercent float64
	Load1        float64
}

// GetCPUInfo samples host-wide CPU telemetry.
func GetCPUInfo() (*CPUInfo, error) {
	logicalCores, err := cpu.Counts(true)
	if err != nil {
		return nil, fmt.Errorf("counting logical CPU cores: %w", err)
	}
	if logicalCores < 1 {
		return nil, fmt.Errorf("counting logical CPU cores: no cores reported")
	}

	usage, err := cpu.Percent(0, false)
	if err != nil {
		return nil, fmt.Errorf("sampling CPU utilization: %w", err)
	}
	if len(usage) != 1 {
		return nil, fmt.Errorf("sampling CPU utilization: expected one aggregate reading, got %d", len(usage))
	}

	loadAverage, err := load.Avg()
	if err != nil {
		return nil, fmt.Errorf("sampling CPU load: %w", err)
	}

	return &CPUInfo{
		LogicalCores: uint64(logicalCores),
		UsagePercent: usage[0],
		Load1:        loadAverage.Load1,
	}, nil
}

func CPUCapabilities() ([]string, error) {
	cpu, err := ghw.CPU()
	if err != nil {
		return nil, err
	}

	caps := map[string]struct{}{}

	for _, proc := range cpu.Processors {
		for _, c := range proc.Capabilities {

			caps[c] = struct{}{}
		}

	}

	ret := slices.Collect(maps.Keys(caps))

	// order
	sort.Strings(ret)
	return ret, nil
}

func HasCPUCaps(ids ...cpuid.FeatureID) bool {
	return cpuid.CPU.Supports(ids...)
}

func CPUPhysicalCores() int {
	if cpuid.CPU.PhysicalCores == 0 {
		return 1
	}
	return cpuid.CPU.PhysicalCores
}
