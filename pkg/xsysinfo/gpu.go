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
