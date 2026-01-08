package xsysinfo

import (
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

	estimate := f.EstimateLLaMACppRun()

	lmes := estimate.SummarizeItem(true, 0, 0)
	estimatedVRAM := uint64(0)
	availableLayers := lmes.OffloadLayers // TODO: check if we can just use OffloadLayers here

	for _, vram := range lmes.VRAMs {
		estimatedVRAM += uint64(vram.NonUMA)
	}

	// Calculate base model size
	modelSize := uint64(m.Size)

	if availableLayers == 0 {
		availableLayers = 1
	}

	if estimatedVRAM == 0 {
		estimatedVRAM = 1
	}

	// Estimate number of layers that can fit in VRAM
	// Each layer typically requires about 1/32 of the model size
	layerSize := estimatedVRAM / availableLayers

	estimatedLayers := int(availableVRAM / layerSize)
	if availableVRAM > estimatedVRAM {
		estimatedLayers = int(availableLayers)
	}

	// Calculate estimated VRAM usage

	return &VRAMEstimate{
		TotalVRAM:       availableVRAM,
		AvailableVRAM:   availableVRAM,
		ModelSize:       modelSize,
		EstimatedLayers: estimatedLayers,
		EstimatedVRAM:   estimatedVRAM,
		IsFullOffload:   availableVRAM > estimatedVRAM,
	}, nil
}
