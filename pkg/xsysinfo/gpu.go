package xsysinfo

import (
	"strings"

	"github.com/jaypipes/ghw"
	"github.com/jaypipes/ghw/pkg/gpu"
)

func GPUs() ([]*gpu.GraphicsCard, error) {
	gpu, err := ghw.GPU()
	if err != nil {
		return nil, err
	}

	return gpu.GraphicsCards, nil
}

func TotalAvailableVRAM() (uint64, error) {
	gpus, err := GPUs()
	if err != nil {
		return 0, err
	}

	var totalVRAM uint64
	for _, gpu := range gpus {
		if gpu.Node.Memory.TotalUsableBytes > 0 {
			totalVRAM += uint64(gpu.Node.Memory.TotalUsableBytes)
		}
	}

	return totalVRAM, nil
}

func HasGPU(vendor string) bool {
	gpus, err := GPUs()
	if err != nil {
		return false
	}
	if vendor == "" {
		return len(gpus) > 0
	}
	for _, gpu := range gpus {
		if strings.Contains(gpu.String(), vendor) {
			return true
		}
	}
	return false
}
