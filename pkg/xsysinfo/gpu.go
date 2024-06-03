package xsysinfo

import (
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
