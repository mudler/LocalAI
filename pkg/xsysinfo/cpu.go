package xsysinfo

import (
	"sort"

	"github.com/jaypipes/ghw"
	"github.com/klauspost/cpuid/v2"
)

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

	ret := []string{}
	for c := range caps {
		ret = append(ret, c)
	}

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
