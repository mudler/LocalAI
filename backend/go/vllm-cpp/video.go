package main

// MiniMax-H3 video+audio generation over the vllm.cpp C ABI (v12).
//
// Two things make this different from the text path, and both come from the
// engine's own shape rather than from LocalAI:
//
//  1. A video engine is loaded from a checkpoint SET - the DiT, the text
//     encoder and two VAEs are separate artifacts - so it is its own handle
//     (vllm_video_engine) and its own Load branch. The two loaders refuse each
//     other's checkpoints on purpose.
//  2. libvllm writes frames + a WAV and COMPOSES the ffmpeg argv, but spawns
//     nothing. That process boundary is deliberate upstream, so the mux lives
//     here: we take the composed argv, substitute argv[0], and exec it. ffmpeg
//     comes from PATH the same way the vibevoice-cpp backend takes it.
//
// Generation is SLOW - roughly 176 s per denoise step at 1344x768 on a 20-SM
// device, so a default 50-step render is hours, not seconds. Nothing here
// imposes a deadline: GenerateVideo blocks for as long as the engine needs and
// the gRPC call carries LocalAI's application context.

import (
	"fmt"
	"image"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unsafe"

	// Registered for image.DecodeConfig only: a staged keyframe arrives as
	// whatever the caller uploaded, and we need its geometry to size the canvas.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// vllm_video_model_params.device (vllm.h): no auto slot, unlike the text
// engine's v14 device field.
const (
	videoDeviceCPU  int32 = 0
	videoDeviceCUDA int32 = 1
)

// H3's shipped geometry. The canvas is truncated onto a 32-pixel grid and the
// frame count onto the 17n+5 grid by the engine itself
// (MiniMaxH3ResolveShape / MiniMaxH3AlignFrameCount in
// src/vllm/model_executor/models/minimax_h3_planner.cpp); mirrored here only so
// a keyframe can be resampled to the exact canvas the engine will render at.
const (
	h3CanvasMultiple int32 = 32
	h3FrameGrid      int32 = 17
	h3FrameOffset    int32 = 5
	h3ShortEdge      int32 = 768
)

// videoPartitions are the two DECLARED partitions of the H3 release. The FL2VA
// checkpoint serves t2va and fl2va; ref2va is a different checkpoint. Passing
// reference conditioning against an fl2va DiT is a partition mismatch that
// renders a coloured lattice over the frame rather than failing cleanly, which
// is why it is refused here before the engine is ever called.
const (
	partitionFL2VA  = "fl2va"
	partitionRef2VA = "ref2va"
)

// videoRequestParams are the per-request `params` keys this backend accepts.
// Unknown keys are an error rather than a silent drop: a misspelled reference
// path would otherwise produce a perfectly successful render of the wrong
// thing, hours later.
var videoRequestParams = []string{"noise_aug", "ref_image", "ref_video", "crf"}

// loadVideo opens the H3 checkpoint set. `dit` is the model config's
// parameters.model; every other artifact comes from the options.
func (v *VllmCpp) loadVideo(opts *pb.ModelOptions, dit string) error {
	vo := &v.opts.video

	// Relative option paths resolve against LocalAI's models directory, which
	// is where the gallery lands the five H3 files.
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) || opts.ModelPath == "" {
			return p
		}
		return filepath.Join(opts.ModelPath, p)
	}
	vo.encoderPath = resolve(vo.encoderPath)
	vo.tokenizerPath = resolve(vo.tokenizerPath)
	vo.videoVaePath = resolve(vo.videoVaePath)
	vo.videoVaeConfig = resolve(vo.videoVaeConfig)
	vo.audioVaePath = resolve(vo.audioVaePath)
	vo.audioVaeConfig = resolve(vo.audioVaeConfig)
	vo.promptEmbedsPath = resolve(vo.promptEmbedsPath)
	vo.workdir = resolve(vo.workdir)

	// A VAE config carries the per-channel latents_mean/latents_std and the
	// temporal clip_length/token_drop; decode is wrong without it. The release
	// ships it beside the weights, so default to that rather than making every
	// config repeat it.
	if vo.videoVaeConfig == "" && vo.videoVaePath != "" {
		vo.videoVaeConfig = siblingConfigJSON(vo.videoVaePath)
	}
	if vo.audioVaeConfig == "" && vo.audioVaePath != "" {
		vo.audioVaeConfig = siblingConfigJSON(vo.audioVaePath)
	}

	if vo.partition == "" {
		// The community GGUF/NVFP4 quantisations strip the release metadata and
		// the two DiTs are byte-structurally identical, so the engine cannot
		// infer this and refuses every generate until it is declared. The
		// shipped FL2VA checkpoint is the one the gallery entry installs.
		vo.partition = partitionFL2VA
		xlog.Warn("[vllm-cpp] video partition not declared, assuming the FL2VA checkpoint",
			"hint", "set options: [video_partition:fl2va] or [video_partition:ref2va] to match the DiT you installed")
	}
	if vo.partition != partitionFL2VA && vo.partition != partitionRef2VA {
		return fmt.Errorf("vllm-cpp: video_partition must be %q or %q, got %q",
			partitionFL2VA, partitionRef2VA, vo.partition)
	}
	if vo.videoVaePath == "" || vo.audioVaePath == "" {
		return fmt.Errorf("vllm-cpp: MiniMax-H3 needs both VAEs: set options: " +
			"[video_vae:<video vae .safetensors>, audio_vae:<audio vae .safetensors>]")
	}
	if vo.encoderPath == "" && vo.promptEmbedsPath == "" {
		return fmt.Errorf("vllm-cpp: MiniMax-H3 needs text conditioning: set options: " +
			"[video_encoder:<encoder .gguf>, video_tokenizer:<tokenizer.json>] " +
			"or [video_prompt_embeds:<f32 embeddings>]")
	}
	if !vo.deviceSet && opts.GetCUDA() {
		vo.device = videoDeviceCUDA
	}

	mp := cVideoModelParams{
		Device:      vo.device,
		DequantBf16: vo.dequantBf16,
		Fp4Resident: vo.fp4Resident,
	}
	var keep [][]byte
	setStr := func(dst *uintptr, s string) {
		if s == "" {
			return
		}
		b := cString(s)
		keep = append(keep, b)
		*dst = uintptr(unsafe.Pointer(&b[0])) // #nosec G103 -- borrowed by C for the load call only
	}
	setStr(&mp.DitPath, dit)
	setStr(&mp.EncoderPath, vo.encoderPath)
	setStr(&mp.TokenizerPath, vo.tokenizerPath)
	setStr(&mp.VideoVaePath, vo.videoVaePath)
	setStr(&mp.VideoVaeConfigPath, vo.videoVaeConfig)
	setStr(&mp.AudioVaePath, vo.audioVaePath)
	setStr(&mp.AudioVaeConfigPath, vo.audioVaeConfig)
	setStr(&mp.PromptEmbedsPath, vo.promptEmbedsPath)
	setStr(&mp.Partition, vo.partition)

	xlog.Info("[vllm-cpp] Load (MiniMax-H3 video)", "dit", dit, "engine", vllmVersion(),
		"encoder", vo.encoderPath, "tokenizer", vo.tokenizerPath,
		"videoVae", vo.videoVaePath, "audioVae", vo.audioVaePath,
		"partition", vo.partition, "device", videoDeviceName(vo.device),
		"dequantBf16", vo.dequantBf16 == 1, "fp4Resident", vo.fp4Resident == 1)

	var engine uintptr
	rc := vllmVideoEngineLoad(unsafe.Pointer(&mp), unsafe.Pointer(&engine)) // #nosec G103 -- POD out-params
	runtime.KeepAlive(keep)
	if rc != vllmOK {
		return fmt.Errorf("vllm-cpp: video engine load failed: %s", vllmLastError())
	}
	v.videoEngine = engine
	return nil
}

// GenerateVideo renders one clip and muxes it to opts.Dst as an MP4 carrying
// H3's jointly generated AAC audio track. It blocks for the whole render.
func (v *VllmCpp) GenerateVideo(opts *pb.GenerateVideoRequest) error {
	if v.videoEngine == 0 {
		return fmt.Errorf("vllm-cpp: this model is not a MiniMax-H3 video engine " +
			"(load it with the video_vae / audio_vae / video_encoder options)")
	}
	if strings.TrimSpace(opts.GetPrompt()) == "" {
		return fmt.Errorf("vllm-cpp: video generation needs a prompt")
	}
	dst := opts.GetDst()
	if dst == "" {
		return fmt.Errorf("vllm-cpp: video generation needs an output path")
	}
	vo := v.opts.video

	extra, err := parseVideoRequestParams(opts.GetParams())
	if err != nil {
		return err
	}
	if err := checkPartitionConditioning(vo.partition, opts, extra); err != nil {
		return err
	}
	if opts.GetNegativePrompt() != "" {
		xlog.Warn("[vllm-cpp] MiniMax-H3 has no negative prompt; ignoring it")
	}
	if opts.GetCfgScale() != 0 {
		xlog.Warn("[vllm-cpp] MiniMax-H3 has no classifier-free guidance scale; ignoring cfg_scale")
	}

	workdir, cleanup, err := v.videoWorkdir(dst)
	if err != nil {
		return err
	}
	defer cleanup()

	width, height := firstPositive(opts.GetWidth(), vo.width), firstPositive(opts.GetHeight(), vo.height)
	frames := firstPositive(opts.GetNumFrames(), vo.numFrames)
	steps := firstPositive(opts.GetStep(), vo.steps)

	vp := cVideoParams{
		NumFrames: frames,
		Steps:     steps,
		NoiseAug:  extra.noiseAug,
	}
	if opts.GetSeed() > 0 {
		vp.Seed = uint64(opts.GetSeed())
		vp.HasSeed = 1
	}
	if aligned := alignFrameCount(frames); aligned != frames {
		xlog.Warn("[vllm-cpp] frame count is not on H3's 17n+5 grid; the engine rounds up",
			"requested", frames, "rendered", aligned)
	}

	// Keyframes must be binary PPM (P6) at the exact output canvas: no image
	// codec and no resampler is vendored in libvllm. Resolve the canvas first,
	// then stage the frames through ffmpeg into it.
	//
	// The REQUEST's geometry is what is honoured here, not the model-level
	// default: that default is a t2va canvas, and applying it to a keyframe
	// would stretch a portrait photo into a 1344x768 letterbox. With no
	// requested geometry the canvas comes from the keyframe's own aspect, which
	// is the rule the engine itself applies (MiniMaxH3ResolveShape).
	first, last := opts.GetStartImage(), opts.GetEndImage()
	if first != "" || last != "" {
		width, height, err = resolveCanvas(opts.GetWidth(), opts.GetHeight(), first, last)
		if err != nil {
			return err
		}
		if first, err = stageKeyframe(vo.ffmpeg, first, width, height, workdir, "first"); err != nil {
			return err
		}
		if last, err = stageKeyframe(vo.ffmpeg, last, width, height, workdir, "last"); err != nil {
			return err
		}
	}
	vp.Width, vp.Height = truncateToGrid(width), truncateToGrid(height)

	var keep [][]byte
	setStr := func(dst *uintptr, s string) {
		if s == "" {
			return
		}
		b := cString(s)
		keep = append(keep, b)
		*dst = uintptr(unsafe.Pointer(&b[0])) // #nosec G103 -- borrowed by C for the call only
	}
	setStr(&vp.Prompt, opts.GetPrompt())
	setStr(&vp.OutputDir, workdir)
	setStr(&vp.FirstFrame, first)
	setStr(&vp.LastFrame, last)
	setStr(&vp.RefImage, extra.refImage)
	setStr(&vp.RefVideo, extra.refVideo)
	setStr(&vp.RefAudio, opts.GetAudio())

	xlog.Info("[vllm-cpp] GenerateVideo", "dst", dst, "workdir", workdir,
		"width", vp.Width, "height", vp.Height, "frames", vp.NumFrames,
		"steps", vp.Steps, "seeded", vp.HasSeed == 1, "partition", vo.partition)

	var out cVideoResult
	rc := vllmVideoGenerate(v.videoEngine, unsafe.Pointer(&vp), unsafe.Pointer(&out)) // #nosec G103 -- POD in/out params
	runtime.KeepAlive(keep)
	if rc != vllmOK {
		return fmt.Errorf("vllm-cpp: video generation failed: %s", vllmLastError())
	}
	defer vllmVideoResultFree(unsafe.Pointer(&out)) // #nosec G103 -- frees the library-owned members

	frameDir, audioPath := goString(out.FrameDir), goString(out.AudioPath)
	xlog.Info("[vllm-cpp] rendered", "frames", out.FrameCount,
		"width", out.Width, "height", out.Height, "fps", out.Fps,
		"audio", audioPath, "sampleRate", out.SampleRate)
	if opts.GetFps() > 0 && opts.GetFps() != out.Fps {
		// Muxing at any other rate desynchronises the jointly generated audio.
		xlog.Warn("[vllm-cpp] MiniMax-H3 renders at a fixed frame rate; ignoring the requested fps",
			"requested", opts.GetFps(), "rendered", out.Fps)
	}

	return v.muxVideo(frameDir, audioPath, dst, out.Fps, extra.crf)
}

// muxVideo execs the argv libvllm composed. The encoding contract (h264 /
// yuv420p + AAC, -shortest, +faststart) belongs to the library; only the spawn
// is ours.
func (v *VllmCpp) muxVideo(frameDir, audioPath, dst string, fps, crf int32) error {
	mx := cVideoMuxParams{Fps: fps, Crf: crf}
	var keep [][]byte
	setStr := func(dst *uintptr, s string) {
		if s == "" {
			return
		}
		b := cString(s)
		keep = append(keep, b)
		*dst = uintptr(unsafe.Pointer(&b[0])) // #nosec G103 -- borrowed by C for the call only
	}
	setStr(&mx.Frames, filepath.Join(frameDir, "frame_%06d.ppm"))
	setStr(&mx.AudioPath, audioPath)
	setStr(&mx.OutputPath, dst)

	var argvPtr uintptr
	var argc int32
	rc := vllmVideoMuxArgv(unsafe.Pointer(&mx), unsafe.Pointer(&argvPtr), unsafe.Pointer(&argc)) // #nosec G103 -- POD out-params
	runtime.KeepAlive(keep)
	if rc != vllmOK {
		return fmt.Errorf("vllm-cpp: composing the mux command failed: %s", vllmLastError())
	}
	argv := goStringSlice(argvPtr, argc)
	vllmVideoMuxArgvFre(argvPtr, argc)
	if len(argv) == 0 {
		return fmt.Errorf("vllm-cpp: the library composed an empty mux command")
	}

	ffmpegBin, err := resolveFfmpeg(v.opts.video.ffmpeg)
	if err != nil {
		return err
	}
	argv[0] = ffmpegBin

	xlog.Debug("[vllm-cpp] muxing", "argv", argv)
	output, err := exec.Command(argv[0], argv[1:]...).CombinedOutput() // #nosec G204 -- argv is composed by libvllm, argv[0] is a resolved binary
	if err != nil {
		return fmt.Errorf("vllm-cpp: ffmpeg mux failed: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// resolveFfmpeg locates the mux binary. The backend image is FROM scratch and
// carries no ffmpeg, exactly like vibevoice-cpp's transcode path: the host must
// provide one, and saying so plainly beats a bare "exec: not found" after an
// hours-long render.
func resolveFfmpeg(configured string) (string, error) {
	name := configured
	if name == "" {
		name = "ffmpeg"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("vllm-cpp: %q not found: MiniMax-H3 output is muxed with ffmpeg, "+
			"install it on the host or point options: [ffmpeg:<path>] at a binary: %w", name, err)
	}
	return path, nil
}

// videoWorkdir returns the directory the engine writes frame_%06d.ppm and
// audio.wav into, plus its cleanup.
//
// It is ALWAYS a fresh directory. Reusing one would leave a longer previous
// run's trailing frames in place for the mux to pick up, silently splicing two
// renders together. With video_workdir set the run is kept (its frames are what
// ref2va's ref_video consumes); otherwise it is removed once the mux succeeds.
func (v *VllmCpp) videoWorkdir(dst string) (string, func(), error) {
	parent := v.opts.video.workdir
	keep := parent != ""
	if parent == "" {
		parent = filepath.Dir(dst)
	}
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return "", nil, fmt.Errorf("vllm-cpp: creating the video work directory: %w", err)
	}
	dir, err := os.MkdirTemp(parent, "vllm-cpp-h3-")
	if err != nil {
		return "", nil, fmt.Errorf("vllm-cpp: creating the video work directory: %w", err)
	}
	if keep {
		return dir, func() {}, nil
	}
	return dir, func() {
		if err := os.RemoveAll(dir); err != nil {
			xlog.Warn("[vllm-cpp] could not remove the video work directory", "dir", dir, "error", err)
		}
	}, nil
}

// videoExtraParams holds the per-request knobs that have no proto field.
type videoExtraParams struct {
	noiseAug float32
	refImage string
	refVideo string
	crf      int32
}

func parseVideoRequestParams(params map[string]string) (videoExtraParams, error) {
	var extra videoExtraParams
	for k, raw := range params {
		v := strings.TrimSpace(raw)
		switch k {
		case "noise_aug":
			f, err := strconv.ParseFloat(v, 32)
			if err != nil {
				return extra, fmt.Errorf("vllm-cpp: params.noise_aug must be a number, got %q", raw)
			}
			extra.noiseAug = float32(f)
		case "ref_image":
			extra.refImage = v
		case "ref_video":
			extra.refVideo = v
		case "crf":
			n, err := strconv.ParseInt(v, 10, 32)
			if err != nil {
				return extra, fmt.Errorf("vllm-cpp: params.crf must be an integer, got %q", raw)
			}
			extra.crf = int32(n)
		default:
			return extra, fmt.Errorf("vllm-cpp: unknown params key %q (accepted: %s)",
				k, strings.Join(videoRequestParams, ", "))
		}
	}
	return extra, nil
}

// checkPartitionConditioning refuses conditioning the loaded checkpoint cannot
// serve.
//
// This is the failure this backend most needs to catch early. The FL2VA
// partition serves t2va and fl2va; handing it a reference image or audio is a
// partition mismatch, and H3 does not fail cleanly on one - it renders, for
// hours, and returns a coloured lattice over the frame. The engine's own #77
// guard covers a missing declaration; this covers a declaration that does not
// match the request.
func checkPartitionConditioning(partition string, opts *pb.GenerateVideoRequest, extra videoExtraParams) error {
	hasKeyframe := opts.GetStartImage() != "" || opts.GetEndImage() != ""
	hasReference := extra.refImage != "" || extra.refVideo != "" || opts.GetAudio() != ""

	if hasKeyframe && hasReference {
		return fmt.Errorf("vllm-cpp: fl2va keyframes (start_image/end_image) and ref2va reference " +
			"conditioning (params.ref_image/params.ref_video/audio) are exclusive in the H3 pipeline")
	}
	switch partition {
	case partitionFL2VA:
		if hasReference {
			return fmt.Errorf("vllm-cpp: the FL2VA checkpoint serves t2va and fl2va only - " +
				"reference conditioning (params.ref_image/params.ref_video/audio) needs a ref2va DiT. " +
				"Use start_image for first-frame conditioning instead")
		}
	case partitionRef2VA:
		if hasKeyframe {
			return fmt.Errorf("vllm-cpp: the Ref2VA checkpoint does not serve fl2va keyframes - " +
				"pass the image as params.ref_image, or install the FL2VA checkpoint")
		}
	}
	return nil
}

// resolveCanvas settles the output geometry BEFORE a keyframe is resampled,
// because the two have to agree exactly: the engine refuses a keyframe that is
// not already at the output resolution, and when no geometry is requested it
// derives one from the keyframe's own aspect. Mirrors _resolve_shape
// (src/vllm/model_executor/models/minimax_h3_planner.cpp:264-308).
func resolveCanvas(width, height int32, keyframes ...string) (int32, int32, error) {
	if width > 0 && height > 0 {
		return width, height, nil
	}
	for _, k := range keyframes {
		if k == "" {
			continue
		}
		w, h, err := imageDimensions(k)
		if err != nil {
			return 0, 0, err
		}
		if w <= 0 || h <= 0 {
			continue
		}
		// A 768 short edge, the long edge snapped onto the 32 grid.
		if w >= h {
			return alignMultiple(float64(h3ShortEdge)*float64(w)/float64(h), h3CanvasMultiple), h3ShortEdge, nil
		}
		return h3ShortEdge, alignMultiple(float64(h3ShortEdge)*float64(h)/float64(w), h3CanvasMultiple), nil
	}
	// The shipped canvas.
	return 1344, h3ShortEdge, nil
}

// stageKeyframe converts a staged upload into the binary PPM (P6) at exactly
// width x height that the engine requires. libvllm vendors no image codec and
// no resampler, so ffmpeg does both; a P6 already at the canvas passes through
// untouched.
func stageKeyframe(ffmpegPath, src string, width, height int32, workdir, name string) (string, error) {
	if src == "" {
		return "", nil
	}
	if w, h, err := ppmDimensions(src); err == nil && w == width && h == height {
		return src, nil
	}
	ffmpegBin, err := resolveFfmpeg(ffmpegPath)
	if err != nil {
		return "", fmt.Errorf("converting the %s keyframe to PPM: %w", name, err)
	}
	out := filepath.Join(workdir, name+"_frame.ppm")
	// -frames:v 1 because an animated upload (GIF) would otherwise write a
	// sequence; -pix_fmt rgb24 is what the image2/ppm muxer needs for P6.
	cmd := exec.Command(ffmpegBin, "-y", "-loglevel", "error", "-i", src, // #nosec G204 -- the binary is resolved, the rest are literals and staged paths
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d", width, height),
		"-pix_fmt", "rgb24", "-f", "image2", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("vllm-cpp: converting the %s keyframe to PPM failed: %w (output: %s)",
			name, err, strings.TrimSpace(string(output)))
	}
	return out, nil
}

// imageDimensions reads geometry from a staged upload, PPM included (the Go
// standard library has no netpbm decoder).
func imageDimensions(path string) (int32, int32, error) {
	if w, h, err := ppmDimensions(path); err == nil {
		return w, h, nil
	}
	f, err := os.Open(path) // #nosec G304 -- a path staged by LocalAI for this request
	if err != nil {
		return 0, 0, fmt.Errorf("vllm-cpp: reading the keyframe %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, fmt.Errorf("vllm-cpp: the keyframe %q is not a PNG, JPEG, GIF or binary PPM: %w", path, err)
	}
	return int32(cfg.Width), int32(cfg.Height), nil
}

// ppmDimensions parses a binary PPM (P6) header: magic, then width, height and
// maxval as ASCII decimals separated by whitespace, with # comments allowed.
func ppmDimensions(path string) (int32, int32, error) {
	f, err := os.Open(path) // #nosec G304 -- a path staged by LocalAI for this request
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()

	// A P6 header is a handful of bytes; 512 covers any sane comment run.
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if n < 2 || (err != nil && n == 0) {
		return 0, 0, fmt.Errorf("not a PPM")
	}
	if buf[0] != 'P' || buf[1] != '6' {
		return 0, 0, fmt.Errorf("not a binary PPM (P6)")
	}
	fields := make([]int32, 0, 2)
	for i := 2; i < n && len(fields) < 2; {
		switch {
		case buf[i] == '#':
			for i < n && buf[i] != '\n' {
				i++
			}
		case buf[i] >= '0' && buf[i] <= '9':
			value := int32(0)
			for i < n && buf[i] >= '0' && buf[i] <= '9' {
				value = value*10 + int32(buf[i]-'0')
				i++
			}
			fields = append(fields, value)
		default:
			i++
		}
	}
	if len(fields) < 2 {
		return 0, 0, fmt.Errorf("truncated PPM header")
	}
	return fields[0], fields[1], nil
}

// alignMultiple mirrors MiniMaxH3AlignMultiple: round-half-to-even onto the
// multiple, floored at one multiple. Half-to-even, not half-away-from-zero,
// because the reference pipeline uses Python's round().
func alignMultiple(value float64, multiple int32) int32 {
	snapped := int32(math.RoundToEven(value/float64(multiple))) * multiple
	if snapped < multiple {
		return multiple
	}
	return snapped
}

// truncateToGrid mirrors the engine's canvas snap: truncation, not rounding.
func truncateToGrid(v int32) int32 {
	if v <= 0 {
		return 0
	}
	return v / h3CanvasMultiple * h3CanvasMultiple
}

// alignFrameCount mirrors MiniMaxH3AlignFrameCount: the next value on the
// 17n+5 grid. Used only to warn - the engine does the real alignment.
func alignFrameCount(frames int32) int32 {
	if frames <= 0 {
		return frames
	}
	for frames%h3FrameGrid != h3FrameOffset {
		frames++
	}
	return frames
}

func firstPositive(values ...int32) int32 {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func videoDeviceName(device int32) string {
	if device == videoDeviceCUDA {
		return "cuda"
	}
	return "cpu"
}

// siblingConfigJSON is the release layout: each VAE ships its config.json in
// the directory holding its weights.
func siblingConfigJSON(weights string) string {
	candidate := filepath.Join(filepath.Dir(weights), "config.json")
	if _, err := os.Stat(candidate); err != nil {
		return ""
	}
	return candidate
}
