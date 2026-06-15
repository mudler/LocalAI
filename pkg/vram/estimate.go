package vram

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/xlog"
)

var weightExts = map[string]bool{
	".gguf": true, ".safetensors": true, ".bin": true, ".pt": true,
}

func IsWeightFile(nameOrURI string) bool {
	ext := strings.ToLower(path.Ext(path.Base(nameOrURI)))
	return weightExts[ext]
}

func IsGGUF(nameOrURI string) bool {
	return strings.ToLower(path.Ext(path.Base(nameOrURI))) == ".gguf"
}

// modelProfile captures the "fixed" properties of a model after I/O.
// Everything except context length is constant for a given model.
type modelProfile struct {
	sizeBytes    uint64    // total weight file size
	ggufSize     uint64    // GGUF file size (subset of sizeBytes)
	meta         *GGUFMeta // nil if no GGUF metadata available
}

// resolveProfile does all I/O: iterates files, fetches sizes and GGUF metadata.
func resolveProfile(ctx context.Context, files []FileInput, sizeResolver SizeResolver, ggufReader GGUFMetadataReader) modelProfile {
	var p modelProfile
	var firstGGUFURI string

	for i := range files {
		f := &files[i]
		if !IsWeightFile(f.URI) {
			continue
		}
		sz := f.Size
		if sz <= 0 && sizeResolver != nil {
			var err error
			sz, err = sizeResolver.ContentLength(ctx, f.URI)
			if err != nil {
				continue
			}
		}
		p.sizeBytes += uint64(sz)
		if IsGGUF(f.URI) {
			p.ggufSize += uint64(sz)
			if firstGGUFURI == "" {
				firstGGUFURI = f.URI
			}
		}
	}

	if p.ggufSize > 0 && ggufReader != nil && firstGGUFURI != "" {
		p.meta, _ = ggufReader.ReadMetadata(ctx, firstGGUFURI)
	}

	return p
}

// computeVRAM is pure arithmetic — no I/O. Returns VRAM bytes for a given
// model profile and context length.
func computeVRAM(p modelProfile, ctxLen uint32, opts EstimateOptions) uint64 {
	kvQuantBits := opts.KVQuantBits
	if kvQuantBits == 0 {
		kvQuantBits = 16
	}

	if p.ggufSize > 0 {
		meta := p.meta
		if meta != nil && (meta.BlockCount > 0 || meta.EmbeddingLength > 0) {
			nLayers := meta.BlockCount
			if nLayers == 0 {
				nLayers = 32
			}
			dModel := meta.EmbeddingLength
			if dModel == 0 {
				dModel = 4096
			}
			headCountKV := meta.HeadCountKV
			if headCountKV == 0 {
				headCountKV = meta.HeadCount
			}
			if headCountKV == 0 {
				headCountKV = 8
			}
			gpuLayers := opts.GPULayers
			if gpuLayers <= 0 {
				gpuLayers = int(nLayers)
			}
			bKV := uint32(kvQuantBits / 8)
			if bKV == 0 {
				bKV = 4
			}

			M_model := p.ggufSize
			M_KV := uint64(bKV) * uint64(dModel) * uint64(headCountKV) * uint64(ctxLen)
			P := M_model * 2
			M_overhead := uint64(0.02*float64(P) + 0.15*1e9)
			vramBytes := M_model + M_KV + M_overhead
			if nLayers > 0 && gpuLayers < int(nLayers) {
				layerRatio := float64(gpuLayers) / float64(nLayers)
				vramBytes = uint64(layerRatio*float64(M_model)) + M_KV + M_overhead
			}
			return vramBytes
		}
		return sizeOnlyVRAM(p.ggufSize, ctxLen)
	}

	if p.sizeBytes > 0 {
		return sizeOnlyVRAM(p.sizeBytes, ctxLen)
	}
	return 0
}

func sizeOnlyVRAM(sizeOnDisk uint64, ctxLen uint32) uint64 {
	k := uint64(1024)
	vram := sizeOnDisk + k*uint64(ctxLen)*2
	if vram < sizeOnDisk {
		vram = sizeOnDisk
	}
	return vram
}

// buildEstimates computes VRAMAt entries for each context size from a profile.
func buildEstimates(p modelProfile, contextSizes []uint32, opts EstimateOptions) map[string]VRAMAt {
	m := make(map[string]VRAMAt, len(contextSizes))
	for _, ctxLen := range contextSizes {
		vramBytes := computeVRAM(p, ctxLen, opts)
		m[fmt.Sprint(ctxLen)] = VRAMAt{
			ContextLength: ctxLen,
			VRAMBytes:     vramBytes,
			VRAMDisplay:   FormatBytes(vramBytes),
		}
	}
	return m
}


// EstimateMultiContext estimates model size and VRAM at multiple context sizes.
// It performs I/O once (resolveProfile) then computes VRAM for each context size.
func EstimateMultiContext(ctx context.Context, files []FileInput, contextSizes []uint32,
	opts EstimateOptions, sizeResolver SizeResolver, ggufReader GGUFMetadataReader) (MultiContextEstimate, error) {

	if len(contextSizes) == 0 {
		contextSizes = []uint32{8192}
	}

	p := resolveProfile(ctx, files, sizeResolver, ggufReader)

	result := MultiContextEstimate{
		SizeBytes:   p.sizeBytes,
		SizeDisplay: FormatBytes(p.sizeBytes),
		Estimates:   buildEstimates(p, contextSizes, opts),
	}

	if p.meta != nil && p.meta.MaximumContextLength > 0 {
		result.ModelMaxContext = p.meta.MaximumContextLength
	}

	return result, nil
}

// ParseSizeString parses a human-readable size string (e.g. "500MB", "14.5 GB", "2tb")
// into bytes. Supports B, KB, MB, GB, TB, PB (case-insensitive, space optional).
// Uses SI units (1 KB = 1000 B).
func ParseSizeString(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size string")
	}

	s = strings.ToUpper(s)

	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("no numeric value in size string: %q", s)
	}

	numStr := s[:i]
	suffix := strings.TrimSpace(s[i:])

	var num float64
	if _, err := fmt.Sscanf(numStr, "%f", &num); err != nil {
		return 0, fmt.Errorf("invalid numeric value %q: %w", numStr, err)
	}
	if num < 0 {
		return 0, fmt.Errorf("negative size: %q", s)
	}

	multiplier := uint64(1)
	switch suffix {
	case "", "B":
		multiplier = 1
	case "K", "KB":
		multiplier = 1000
	case "M", "MB":
		multiplier = 1000 * 1000
	case "G", "GB":
		multiplier = 1000 * 1000 * 1000
	case "T", "TB":
		multiplier = 1000 * 1000 * 1000 * 1000
	case "P", "PB":
		multiplier = 1000 * 1000 * 1000 * 1000 * 1000
	default:
		return 0, fmt.Errorf("unknown size suffix: %q", suffix)
	}

	return uint64(num * float64(multiplier)), nil
}

func FormatBytes(n uint64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for u := n / unit; u >= unit; u /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

type defaultSizeResolver struct{}

func (defaultSizeResolver) ContentLength(ctx context.Context, uri string) (int64, error) {
	return downloader.URI(uri).ContentLength(ctx)
}

func DefaultSizeResolver() SizeResolver {
	return defaultSizeResolver{}
}

func DefaultGGUFReader() GGUFMetadataReader {
	return defaultGGUFReader{}
}

// ModelEstimateInput describes the inputs for a unified VRAM/size estimation.
// The estimator cascades through available data: files -> size string -> HF repo -> zero.
type ModelEstimateInput struct {
	Files   []FileInput     // weight files with optional pre-known sizes
	Size    string          // gallery hardcoded size (e.g. "14.5GB")
	HFRepo  string          // HF repo ID or URL
	Options EstimateOptions // GPU layers, KV quant bits
}

// EstimateModelMultiContext provides a unified VRAM estimation entry point
// that returns estimates at multiple context sizes.
// It tries (in order):
//  1. Direct file-based estimation (GGUF metadata or file size heuristic)
//  2. ParseSizeString from Size field
//  3. HuggingFace repo file listing
//  4. Zero result
func EstimateModelMultiContext(ctx context.Context, input ModelEstimateInput, contextSizes []uint32) (MultiContextEstimate, error) {
	if len(contextSizes) == 0 {
		contextSizes = []uint32{8192}
	}

	// 1. Try direct file estimation
	if len(input.Files) > 0 {
		result, err := EstimateMultiContext(ctx, input.Files, contextSizes, input.Options, DefaultCachedSizeResolver(), DefaultCachedGGUFReader())
		if err != nil {
			xlog.Debug("VRAM estimation from files failed", "error", err)
		}
		if err == nil && result.SizeBytes > 0 {
			return result, nil
		}
	}

	// 2. Try size string
	if input.Size != "" {
		if sizeBytes, err := ParseSizeString(input.Size); err != nil {
			xlog.Debug("VRAM estimation from size string failed", "error", err, "size", input.Size)
		} else if sizeBytes > 0 {
			return MultiContextEstimate{
				SizeBytes:   sizeBytes,
				SizeDisplay: FormatBytes(sizeBytes),
				Estimates:   buildEstimates(modelProfile{sizeBytes: sizeBytes}, contextSizes, EstimateOptions{}),
			}, nil
		}
	}

	// 3. Try HF repo
	hfRepo := input.HFRepo
	if repoID, ok := ExtractHFRepoID(hfRepo); ok {
		hfRepo = repoID
	}
	if hfRepo != "" {
		totalBytes, err := hfRepoWeightSize(ctx, hfRepo)
		if err != nil {
			xlog.Debug("VRAM estimation from HF repo failed", "error", err, "repo", hfRepo)
		}
		if err == nil && totalBytes > 0 {
			return MultiContextEstimate{
				SizeBytes:   totalBytes,
				SizeDisplay: FormatBytes(totalBytes),
				Estimates:   buildEstimates(modelProfile{sizeBytes: totalBytes}, contextSizes, EstimateOptions{}),
			}, nil
		}
	}

	// 4. No estimation possible
	return MultiContextEstimate{}, nil
}
