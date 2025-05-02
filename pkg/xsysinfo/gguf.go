package xsysinfo

import (
	"errors"

	gguf "github.com/gpustack/gguf-parser-go"
)

type VRAMEstimate struct {
	TotalVRAM       uint64
	AvailableVRAM   uint64
	ModelSize       uint64
	EstimatedLayers int
	EstimatedVRAM   uint64
	IsFullOffload   bool
}

func EstimateGGUFVRAMUsage(f *gguf.GGUFFile, availableVRAM uint64) (*VRAMEstimate, error) {
	// Get model metadata
	m := f.Metadata()
	a := f.Architecture()

	// Calculate base model size
	modelSize := uint64(m.Size)

	if a.BlockCount == 0 {
		return nil, errors.New("block count is 0")
	}

	// Estimate number of layers that can fit in VRAM
	// Each layer typically requires about 1/32 of the model size
	layerSize := modelSize / uint64(a.BlockCount)
	estimatedLayers := int(availableVRAM / layerSize)

	// If we can't fit even one layer, we need to do full offload
	isFullOffload := estimatedLayers <= 0
	if isFullOffload {
		estimatedLayers = 0
	}

	// Calculate estimated VRAM usage
	estimatedVRAM := uint64(estimatedLayers) * layerSize

	return &VRAMEstimate{
		TotalVRAM:       availableVRAM,
		AvailableVRAM:   availableVRAM,
		ModelSize:       modelSize,
		EstimatedLayers: estimatedLayers,
		EstimatedVRAM:   estimatedVRAM,
		IsFullOffload:   isFullOffload,
	}, nil
}
