package xsysinfo

import (
	"strings"
	"sync"

	"github.com/jaypipes/ghw"
	"github.com/jaypipes/ghw/pkg/gpu"
)

var (
	gpuCache     []*gpu.GraphicsCard
	gpuCacheOnce sync.Once
	gpuCacheErr  error
)

func GPUs() ([]*gpu.GraphicsCard, error) {
	gpuCacheOnce.Do(func() {
		gpu, err := ghw.GPU()
		if err != nil {
			gpuCacheErr = err
			return
		}
		gpuCache = gpu.GraphicsCards
	})

	return gpuCache, gpuCacheErr
}

func TotalAvailableVRAM() (uint64, error) {
	gpus, err := GPUs()
	if err != nil {
		return 0, err
	}

	var totalVRAM uint64
	for _, gpu := range gpus {
		if gpu != nil && gpu.Node != nil && gpu.Node.Memory != nil {
			if gpu.Node.Memory.TotalUsableBytes > 0 {
				totalVRAM += uint64(gpu.Node.Memory.TotalUsableBytes)
			}
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
