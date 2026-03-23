package vram

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/mudler/LocalAI/pkg/downloader"
)

var weightExts = map[string]bool{
	".gguf": true, ".safetensors": true, ".bin": true, ".pt": true,
}

func isWeightFile(nameOrURI string) bool {
	ext := strings.ToLower(path.Ext(path.Base(nameOrURI)))
	return weightExts[ext]
}

func isGGUF(nameOrURI string) bool {
	return strings.ToLower(path.Ext(path.Base(nameOrURI))) == ".gguf"
}

func Estimate(ctx context.Context, files []FileInput, opts EstimateOptions, sizeResolver SizeResolver, ggufReader GGUFMetadataReader) (EstimateResult, error) {
	if opts.ContextLength == 0 {
		opts.ContextLength = 8192
	}
	if opts.KVQuantBits == 0 {
		opts.KVQuantBits = 16
	}

	var sizeBytes uint64
	var ggufSize uint64
	var firstGGUFURI string
	for i := range files {
		f := &files[i]
		if !isWeightFile(f.URI) {
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
		sizeBytes += uint64(sz)
		if isGGUF(f.URI) {
			ggufSize += uint64(sz)
			if firstGGUFURI == "" {
				firstGGUFURI = f.URI
			}
		}
	}

	sizeDisplay := FormatBytes(sizeBytes)

	var vramBytes uint64
	if ggufSize > 0 {
		var meta *GGUFMeta
		if ggufReader != nil && firstGGUFURI != "" {
			meta, _ = ggufReader.ReadMetadata(ctx, firstGGUFURI)
		}
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
			ctxLen := opts.ContextLength
			bKV := uint32(opts.KVQuantBits / 8)
			if bKV == 0 {
				bKV = 4
			}
			M_model := ggufSize
			M_KV := uint64(bKV) * uint64(dModel) * uint64(nLayers) * uint64(ctxLen)
			if headCountKV > 0 && meta.HeadCount > 0 {
				M_KV = uint64(bKV) * uint64(dModel) * uint64(headCountKV) * uint64(ctxLen)
			}
			P := M_model * 2
			M_overhead := uint64(0.02*float64(P) + 0.15*1e9)
			vramBytes = M_model + M_KV + M_overhead
			if nLayers > 0 && gpuLayers < int(nLayers) {
				layerRatio := float64(gpuLayers) / float64(nLayers)
				vramBytes = uint64(layerRatio*float64(M_model)) + M_KV + M_overhead
			}
		} else {
			vramBytes = sizeOnlyVRAM(ggufSize, opts.ContextLength)
		}
	} else if sizeBytes > 0 {
		vramBytes = sizeOnlyVRAM(sizeBytes, opts.ContextLength)
	}

	return EstimateResult{
		SizeBytes:   sizeBytes,
		SizeDisplay: sizeDisplay,
		VRAMBytes:   vramBytes,
		VRAMDisplay: FormatBytes(vramBytes),
	}, nil
}

func sizeOnlyVRAM(sizeOnDisk uint64, ctxLen uint32) uint64 {
	k := uint64(1024)
	vram := sizeOnDisk + k*uint64(ctxLen)*2
	if vram < sizeOnDisk {
		vram = sizeOnDisk
	}
	return vram
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

	// Find where the numeric part ends
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

// EstimateFromSize builds an EstimateResult from a raw byte count.
func EstimateFromSize(sizeBytes uint64) EstimateResult {
	vramBytes := sizeOnlyVRAM(sizeBytes, 8192)
	return EstimateResult{
		SizeBytes:   sizeBytes,
		SizeDisplay: FormatBytes(sizeBytes),
		VRAMBytes:   vramBytes,
		VRAMDisplay: FormatBytes(vramBytes),
	}
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
// The estimator cascades through available data: files → size string → HF repo → zero.
type ModelEstimateInput struct {
	Files   []FileInput     // weight files with optional pre-known sizes
	Size    string          // gallery hardcoded size (e.g. "14.5GB")
	HFRepo  string          // HF repo ID or URL
	Options EstimateOptions // context length, GPU layers, KV quant bits
}

// EstimateModel provides a unified VRAM estimation entry point.
// It tries (in order):
//  1. Direct file-based estimation (GGUF metadata or file size heuristic)
//  2. ParseSizeString from Size field
//  3. EstimateFromHFRepo
//  4. Zero result
func EstimateModel(ctx context.Context, input ModelEstimateInput) (EstimateResult, error) {
	// 1. Try direct file estimation
	if len(input.Files) > 0 {
		result, err := Estimate(ctx, input.Files, input.Options, DefaultCachedSizeResolver(), DefaultCachedGGUFReader())
		if err == nil && result.SizeBytes > 0 {
			return result, nil
		}
	}

	// 2. Try size string
	if input.Size != "" {
		if sizeBytes, err := ParseSizeString(input.Size); err == nil && sizeBytes > 0 {
			return EstimateFromSize(sizeBytes), nil
		}
	}

	// 3. Try HF repo
	hfRepo := input.HFRepo
	if repoID, ok := ExtractHFRepoID(hfRepo); ok {
		hfRepo = repoID
	}
	if hfRepo != "" {
		result, err := EstimateFromHFRepo(ctx, hfRepo)
		if err == nil && result.SizeBytes > 0 {
			return result, nil
		}
	}

	// 4. No estimation possible
	return EstimateResult{}, nil
}
