package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
)

type SDGGML struct {
	base.SingleThread
	upscaleMu    sync.Mutex
	upscaler     uintptr
	upscale      upscaleSettings
	threads      int
	sampleMethod string
	cfgScale     float32
	vaeTiling    vaeTiling
}

var (
	UpscalerCreate  func(string, bool, int32, int32, string, string) uintptr
	UpscalerScale   func(uintptr) int32
	UpscalerRun     func(uintptr, string, string, int32) int32
	UpscalerDestroy func(uintptr)
	LoadModel       func(model, model_apth string, options []uintptr, threads int32, diff int) int
	GenImage        func(params uintptr, steps int, dst string, cfgScale float32, srcImage string, strength float32, maskImage string, refImages []uintptr, refImagesCount int) int
	GenVideo        func(params uintptr, steps int, dst string, cfgScale float32, fps int, initImage string, endImage string) int

	TilingParamsSetEnabled       func(params uintptr, enabled bool)
	TilingParamsSetTileSizes     func(params uintptr, tileSizeX int, tileSizeY int)
	TilingParamsSetRelSizes      func(params uintptr, relSizeX float32, relSizeY float32)
	TilingParamsSetTargetOverlap func(params uintptr, targetOverlap float32)

	ImgGenParamsNew                func() uintptr
	ImgGenParamsSetPrompts         func(params uintptr, prompt string, negativePrompt string)
	ImgGenParamsSetDimensions      func(params uintptr, width int, height int)
	ImgGenParamsSetSeed            func(params uintptr, seed int64)
	ImgGenParamsGetVaeTilingParams func(params uintptr) uintptr

	VidGenParamsNew            func() uintptr
	VidGenParamsSetPrompts     func(params uintptr, prompt string, negativePrompt string)
	VidGenParamsSetDimensions  func(params uintptr, width int, height int)
	VidGenParamsSetSeed        func(params uintptr, seed int64)
	VidGenParamsSetVideoFrames func(params uintptr, n int)
)

type vaeTiling struct {
	enabled       bool
	tileSizeX     int
	tileSizeY     int
	hasTileSize   bool
	targetOverlap float32
	hasOverlap    bool
}

func parseVAETiling(options []string) vaeTiling {
	var t vaeTiling
	for _, op := range options {
		name, value, hasValue := strings.Cut(op, ":")
		switch name {
		case "vae_tiling":
			// A bare flag reads as "on", matching "diffusion_model". The truthy
			// spellings are the ones load_model already accepts for its own
			// bool options, so an author does not have to remember two
			// conventions.
			t.enabled = !hasValue || value == "true" || value == "1"
		case "vae_tile_size":
			if x, y, ok := parseTileSize(value); ok {
				t.tileSizeX, t.tileSizeY, t.hasTileSize = x, y, true
			}
		case "vae_tile_overlap":
			if f, err := strconv.ParseFloat(value, 32); err == nil && f >= 0 {
				t.targetOverlap, t.hasOverlap = float32(f), true
			}
		}
	}
	return t
}

// parseTileSize accepts "512" for a square tile and "512x384" for a
// rectangular one.
//
// A value it cannot make sense of is reported as absent rather than as a zero.
// The caller only calls the upstream setter when a size was given, so a typo
// leaves the library's own default in place instead of installing a degenerate
// tiling that would fail at generation time.
func parseTileSize(value string) (int, int, bool) {
	xs, ys, split := strings.Cut(value, "x")
	if !split {
		ys = xs
	}
	x, err := strconv.Atoi(xs)
	if err != nil || x <= 0 {
		return 0, 0, false
	}
	y, err := strconv.Atoi(ys)
	if err != nil || y <= 0 {
		return 0, 0, false
	}
	return x, y, true
}

// Copied from Purego internal/strings
// TODO: We should upstream sending []string
func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func CString(name string) *byte {
	if hasSuffix(name, "\x00") {
		return &(*(*[]byte)(unsafe.Pointer(&name)))[0]
	}
	b := make([]byte, len(name)+1)
	copy(b, name)
	return &b[0]
}

func (sd *SDGGML) Load(opts *pb.ModelOptions) error {
	sd.upscaleMu.Lock()
	defer sd.upscaleMu.Unlock()
	if opts == nil {
		return fmt.Errorf("missing model options")
	}
	settings, err := parseUpscaleSettings(opts)
	if err != nil {
		return err
	}
	if sd.upscaler != 0 {
		return fmt.Errorf("free the existing upscaler before loading another model")
	}
	sd.upscale = settings
	if settings.enabled {
		return nil
	}

	sd.threads = int(opts.Threads)

	modelPath := opts.ModelPath

	modelFile := opts.ModelFile
	modelPathC := modelPath

	var diffusionModel int

	var oo []string
	for _, op := range opts.Options {
		if op == "diffusion_model" {
			diffusionModel = 1
			continue
		}

		// If it's an option path, we resolve absolute path from the model path
		if strings.Contains(op, ":") && strings.Contains(op, "path") {
			data := strings.Split(op, ":")
			if err := utils.VerifyPath(data[1], opts.ModelPath); err == nil {
				data[1] = filepath.Join(opts.ModelPath, data[1])
				oo = append(oo, strings.Join(data, ":"))
			}
		} else {
			oo = append(oo, op)
		}
	}

	fmt.Fprintf(os.Stderr, "Options: %+v\n", oo)

	// At the time of writing Purego doesn't recurse into slices and convert Go strings to pointers so we need to do that
	var keepAlive []any
	options := make([]uintptr, len(oo), len(oo)+1)
	for i, op := range oo {
		bytep := CString(op)
		options[i] = uintptr(unsafe.Pointer(bytep))
		keepAlive = append(keepAlive, bytep)
	}

	sd.cfgScale = opts.CFGScale
	// Read from the unfiltered list: none of the tiling options name a path, so
	// the resolution pass above neither rewrites nor drops them.
	sd.vaeTiling = parseVAETiling(opts.Options)

	ret := LoadModel(modelFile, modelPathC, options, opts.Threads, diffusionModel)
	runtime.KeepAlive(keepAlive)
	fmt.Fprintf(os.Stderr, "LoadModel: %d\n", ret)
	if ret != 0 {
		return fmt.Errorf("could not load model")
	}

	return nil
}

func (sd *SDGGML) GenerateImage(opts *pb.GenerateImageRequest) error {
	t := opts.PositivePrompt
	dst := opts.Dst
	negative := opts.NegativePrompt
	srcImage := opts.Src

	var maskImage string
	if opts.EnableParameters != "" {
		if strings.Contains(opts.EnableParameters, "mask:") {
			parts := strings.Split(opts.EnableParameters, "mask:")
			if len(parts) > 1 {
				maskPath := strings.TrimSpace(parts[1])
				if maskPath != "" {
					maskImage = maskPath
				}
			}
		}
	}

	// At the time of writing Purego doesn't recurse into slices and convert Go strings to pointers so we need to do that
	var keepAlive []any
	refImagesCount := len(opts.RefImages)
	refImages := make([]uintptr, refImagesCount, refImagesCount+1)
	for i, ri := range opts.RefImages {
		bytep := CString(ri)
		refImages[i] = uintptr(unsafe.Pointer(bytep))
		keepAlive = append(keepAlive, bytep)
	}

	// Default strength for img2img (0.75 is a good default)
	strength := float32(0.75)

	// free'd by GenImage
	p := ImgGenParamsNew()
	ImgGenParamsSetPrompts(p, t, negative)
	ImgGenParamsSetDimensions(p, int(opts.Width), int(opts.Height))
	ImgGenParamsSetSeed(p, int64(opts.Seed))
	// Tiling decodes the latent in overlapping tiles, so the VAE compute buffer
	// scales with the tile rather than with the image. That is the difference
	// between working and failing on any device that caps a single allocation
	// (RADV reports a 4GiB maxMemoryAllocationSize, for one) or that simply
	// does not have the VRAM for a full-frame decode at high resolution.
	//
	// Only the setters the operator configured are called, so an unset tile
	// size or overlap keeps the library's own default.
	vaep := ImgGenParamsGetVaeTilingParams(p)
	TilingParamsSetEnabled(vaep, sd.vaeTiling.enabled)
	if sd.vaeTiling.hasTileSize {
		TilingParamsSetTileSizes(vaep, sd.vaeTiling.tileSizeX, sd.vaeTiling.tileSizeY)
	}
	if sd.vaeTiling.hasOverlap {
		TilingParamsSetTargetOverlap(vaep, sd.vaeTiling.targetOverlap)
	}

	ret := GenImage(p, int(opts.Step), dst, sd.cfgScale, srcImage, strength, maskImage, refImages, refImagesCount)
	runtime.KeepAlive(keepAlive)
	fmt.Fprintf(os.Stderr, "GenImage: %d\n", ret)
	if ret != 0 {
		return fmt.Errorf("inference failed")
	}

	return nil
}

func (sd *SDGGML) GenerateVideo(opts *pb.GenerateVideoRequest) error {
	dst := opts.Dst
	if dst == "" {
		return fmt.Errorf("dst is empty")
	}

	width := int(opts.Width)
	height := int(opts.Height)
	if width == 0 {
		width = 512
	}
	if height == 0 {
		height = 512
	}

	numFrames := int(opts.NumFrames)
	if numFrames <= 0 {
		numFrames = 16
	}

	fps := int(opts.Fps)
	if fps <= 0 {
		fps = 16
	}

	steps := int(opts.Step)
	if steps <= 0 {
		steps = 20
	}

	cfg := opts.CfgScale
	if cfg == 0 {
		cfg = sd.cfgScale
	}
	if cfg == 0 {
		cfg = 5.0
	}

	// sd_vid_gen_params_new allocates; gen_video frees it after the generation call.
	p := VidGenParamsNew()
	VidGenParamsSetPrompts(p, opts.Prompt, opts.NegativePrompt)
	VidGenParamsSetDimensions(p, width, height)
	VidGenParamsSetSeed(p, int64(opts.Seed))
	VidGenParamsSetVideoFrames(p, numFrames)

	fmt.Fprintf(os.Stderr, "GenerateVideo: dst=%s size=%dx%d frames=%d fps=%d steps=%d cfg=%.2f\n",
		dst, width, height, numFrames, fps, steps, cfg)

	ret := GenVideo(p, steps, dst, cfg, fps, opts.StartImage, opts.EndImage)
	if ret != 0 {
		return fmt.Errorf("video inference failed (code %d)", ret)
	}
	return nil
}

// ModelOptions does not carry KnownUsecases or the typed upscale settings.
// Presence of upscale_scale in Options is the explicit upscale-only marker;
// callers must forward the typed setting through that existing option list.
// Also honor declared usecases if a caller forwards them in Options.
type upscaleSettings struct {
	enabled                bool
	path                   string
	scale, tile, threads   int32
	direct                 bool
	backend, paramsBackend string
}

func parseUpscaleSettings(opts *pb.ModelOptions) (upscaleSettings, error) {
	s := upscaleSettings{threads: opts.Threads}
	image := false
	for _, op := range opts.Options {
		k, v, _ := strings.Cut(op, ":")
		if k == "upscale_scale" {
			s.enabled = true
		}
		if k == "known_usecases" {
			for _, u := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '[' || r == ']' || r == ' ' }) {
				if u == "upscale" {
					s.enabled = true
				}
				if u == "image" {
					image = true
				}
			}
		}
		if k == "diffusion_model" {
			image = true
		}
	}
	if !s.enabled {
		return s, nil
	}
	if image {
		return s, fmt.Errorf("mixed image and upscale models are not supported")
	}
	seen := make(map[string]bool)
	for _, op := range opts.Options {
		k, v, _ := strings.Cut(op, ":")
		switch k {
		case "upscale_scale", "upscale_tile_size", "upscale_direct", "backend", "params_backend":
			if seen[k] {
				return s, fmt.Errorf("duplicate upscale setting %q", k)
			}
			seen[k] = true
		}
		switch k {
		case "upscale_scale", "upscale_tile_size":
			n, err := strconv.ParseInt(v, 10, 32)
			if err != nil || (k == "upscale_scale" && n <= 0) || (k == "upscale_tile_size" && n < 0) {
				if k == "upscale_tile_size" {
					return s, fmt.Errorf("%s must be a non-negative integer", k)
				}
				return s, fmt.Errorf("%s must be a positive integer", k)
			}
			if k == "upscale_scale" {
				s.scale = int32(n)
			} else {
				s.tile = int32(n)
			}
		case "upscale_direct":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return s, fmt.Errorf("invalid upscale_direct: %w", err)
			}
			s.direct = b
		case "backend":
			s.backend = v
		case "params_backend":
			s.paramsBackend = v
		}
	}
	if s.scale <= 0 {
		return s, fmt.Errorf("upscale_scale must be configured and positive")
	}
	modelFile := opts.ModelFile
	if modelFile == "" {
		return s, fmt.Errorf("upscale model path is empty")
	}
	if filepath.IsAbs(modelFile) {
		s.path = filepath.Clean(modelFile)
	} else {
		// Verify the untrusted relative name before joining, then verify the
		// resolved result as well. VerifyPath intentionally treats absolute
		// inputs as relative, so absolute model files use VerifyResolvedPath.
		if err := utils.VerifyPath(modelFile, opts.ModelPath); err != nil {
			return s, fmt.Errorf("upscale model path is outside model path: %w", err)
		}
		s.path = filepath.Join(opts.ModelPath, modelFile)
	}
	if err := utils.VerifyResolvedPath(s.path, opts.ModelPath); err != nil {
		return s, fmt.Errorf("upscale model path is outside model path: %w", err)
	}
	// Lexical containment alone does not prevent a model symlink (or a
	// symlinked parent directory) from escaping the trusted root. Resolve
	// both, requiring an existing target, and cache only the real model path.
	realRoot, err := filepath.EvalSymlinks(opts.ModelPath)
	if err != nil {
		return s, fmt.Errorf("resolve upscale model root: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(s.path)
	if err != nil {
		return s, fmt.Errorf("resolve upscale model file: %w", err)
	}
	if err := utils.VerifyResolvedPath(realPath, realRoot); err != nil {
		return s, fmt.Errorf("upscale model path is outside real model path: %w", err)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return s, fmt.Errorf("stat upscale model file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return s, fmt.Errorf("upscale model path must be a regular file")
	}
	s.path = realPath
	for _, v := range []string{s.path, s.backend, s.paramsBackend} {
		if strings.ContainsRune(v, 0) {
			return s, fmt.Errorf("upscale settings contain NUL")
		}
	}
	return s, nil
}

func (sd *SDGGML) UpscaleImage(opts *pb.UpscaleImageRequest) error {
	sd.upscaleMu.Lock()
	defer sd.upscaleMu.Unlock()
	s := sd.upscale
	if !s.enabled {
		return fmt.Errorf("no upscale-only model loaded (configure upscale_scale)")
	}
	if opts == nil || opts.Scale <= 0 || opts.Src == "" || opts.Dst == "" || strings.ContainsRune(opts.Src, 0) || strings.ContainsRune(opts.Dst, 0) {
		return fmt.Errorf("upscale requires source, destination and positive requested scale")
	}
	if sd.upscaler == 0 {
		h := UpscalerCreate(s.path, s.direct, s.threads, s.tile, s.backend, s.paramsBackend)
		runtime.KeepAlive(s)
		if h == 0 {
			return fmt.Errorf("could not initialize ESRGAN upscaler")
		}
		detected := UpscalerScale(h)
		if detected <= 0 || detected != s.scale {
			UpscalerDestroy(h)
			return fmt.Errorf("configured upscale scale %d differs from detected native scale %d", s.scale, detected)
		}
		sd.upscaler = h
	}
	if opts.Scale != s.scale {
		return fmt.Errorf("requested upscale scale %d differs from native scale %d", opts.Scale, s.scale)
	}
	code := UpscalerRun(sd.upscaler, opts.Src, opts.Dst, opts.Scale)
	runtime.KeepAlive(opts)
	switch code {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("invalid native upscale arguments")
	case 2:
		return fmt.Errorf("upscale source decode failed")
	case 3:
		return fmt.Errorf("upscale inference failed")
	case 4:
		return fmt.Errorf("upscale output validation failed")
	case 5:
		return fmt.Errorf("upscale PNG write failed")
	default:
		return fmt.Errorf("upscale failed (native code %d)", code)
	}
}

func (sd *SDGGML) Free() error {
	sd.upscaleMu.Lock()
	defer sd.upscaleMu.Unlock()
	if sd.upscaler != 0 {
		UpscalerDestroy(sd.upscaler)
		sd.upscaler = 0
	}
	sd.upscale = upscaleSettings{}
	// Deliberately leave the existing diffusion sd_c lifecycle unchanged.
	return nil
}
