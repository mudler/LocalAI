package main

// A talking-head video backend over the avatarforcing.cpp C API: a portrait
// image and speech audio give a lip-synced mp4 with the speech as its audio
// track.
//
// The library writes the mp4 itself (avatarforcing_generate_mp4): it streams
// the frames to ffmpeg block by block and muxes the input audio, so no frame
// buffer is held in memory. ffmpeg is also how the inputs are made readable to
// the library, which reads only WAV audio and a 512x512 PNG, JPEG or BMP
// portrait. The LocalAI image ships ffmpeg; the backend checks for it at load
// so that a missing ffmpeg fails at once and by name.

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	// Registered for image.DecodeConfig: a portrait that is already a PNG or
	// JPEG at the model size goes to the library unchanged.
	_ "image/jpeg"
	_ "image/png"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// abiVersion is the AVATARFORCING_ABI_VERSION this backend is written for.
const abiVersion = 1

// Functions of libavatarforcing, bound in main.go. Returned strings that the
// caller must release are uintptr, so that the original pointer can go back
// to avatarforcing_free_string after the copy.
var (
	CppAbiVersion  func() int32
	CppLoad        func(ggufPath, optionsJSON string, errorOut *uintptr) uintptr
	CppFree        func(ctx uintptr)
	CppLastError   func(ctx uintptr) string
	CppFreeString  func(s uintptr)
	CppInfoJSON    func(ctx uintptr) uintptr
	CppGenerateMp4 func(ctx uintptr, portraitPath, wavPath, outputPath, requestJSON string) int32
)

// modelInfo is the part of avatarforcing_info_json that the backend uses.
type modelInfo struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Fps    int    `json:"fps"`
	Device string `json:"device"`
}

// AvatarForcing owns one loaded context. The library serializes the calls on
// a context with its own lock; base.SingleThread makes LocalAI do the same, so
// a second request waits instead of failing.
type AvatarForcing struct {
	base.SingleThread
	ctx    uintptr
	ffmpeg string
	info   modelInfo
}

// Load loads the GGUF in opts.ModelFile with the options of the model config.
func (a *AvatarForcing) Load(opts *pb.ModelOptions) error {
	if opts.ModelFile == "" {
		return errors.New("avatarforcing-cpp: the model file is not set")
	}
	if _, err := os.Stat(opts.ModelFile); err != nil {
		return fmt.Errorf("avatarforcing-cpp: model file: %w", err)
	}
	if v := CppAbiVersion(); v != abiVersion {
		return fmt.Errorf("avatarforcing-cpp: libavatarforcing has ABI version %d, this backend needs %d", v, abiVersion)
	}

	co, err := parseModelOptions(opts.GetOptions(), opts.GetThreads())
	if err != nil {
		return err
	}
	ffmpeg, err := resolveFfmpeg(co.ffmpeg)
	if err != nil {
		return err
	}
	// The library starts ffmpeg by this path, not by a PATH lookup of its own.
	co.ffmpeg = ffmpeg

	ctx, err := loadContext(opts.ModelFile, co)
	if err != nil {
		return err
	}
	info, err := contextInfo(ctx)
	if err != nil {
		CppFree(ctx)
		return err
	}
	a.ctx, a.ffmpeg, a.info = ctx, ffmpeg, info
	xlog.Info("[avatarforcing-cpp] loaded", "model", opts.ModelFile, "device", info.Device,
		"width", info.Width, "height", info.Height, "fps", info.Fps)
	return nil
}

// loadContext calls avatarforcing_load. With no backend option it tries the
// GPU first and falls back to the CPU, so one model config works with the CPU
// and the CUDA images.
func loadContext(gguf string, co contextOptions) (uintptr, error) {
	if co.backend != "" {
		return callLoad(gguf, co.loadJSON(co.backend))
	}
	ctx, err := callLoad(gguf, co.loadJSON("gpu"))
	if err == nil {
		return ctx, nil
	}
	xlog.Info("[avatarforcing-cpp] no GPU context, using the CPU", "reason", err.Error())
	return callLoad(gguf, co.loadJSON("cpu"))
}

func callLoad(gguf, optionsJSON string) (uintptr, error) {
	var errPtr uintptr
	ctx := CppLoad(gguf, optionsJSON, &errPtr)
	if ctx != 0 {
		return ctx, nil
	}
	// The load error comes back through error_out, not last_error: there is
	// no context yet, and a goroutine can change OS threads between calls.
	msg := "unknown error"
	if errPtr != 0 {
		msg = goStringFromCPtr(errPtr)
		CppFreeString(errPtr)
	}
	return 0, fmt.Errorf("avatarforcing-cpp: load %q: %s", gguf, msg)
}

func contextInfo(ctx uintptr) (modelInfo, error) {
	var info modelInfo
	p := CppInfoJSON(ctx)
	if p == 0 {
		return info, fmt.Errorf("avatarforcing-cpp: model info: %s", CppLastError(ctx))
	}
	s := goStringFromCPtr(p)
	CppFreeString(p)
	if err := json.Unmarshal([]byte(s), &info); err != nil {
		return info, fmt.Errorf("avatarforcing-cpp: model info %q: %w", s, err)
	}
	if info.Width <= 0 || info.Height <= 0 {
		return info, fmt.Errorf("avatarforcing-cpp: model info has no frame size: %s", s)
	}
	return info, nil
}

// GenerateVideo writes an mp4 to req.Dst from the portrait in req.StartImage
// and the speech in req.Audio. The video lasts as long as the speech (at most
// max_seconds, 30 s by default) and runs at the frame rate of the model.
func (a *AvatarForcing) GenerateVideo(req *pb.GenerateVideoRequest) error {
	if a.ctx == 0 {
		return grpcerrors.ModelNotLoaded("avatarforcing-cpp")
	}
	if err := checkVideoRequest(req); err != nil {
		return err
	}
	ro, err := requestFromProto(req)
	if err != nil {
		return err
	}
	a.warnIgnored(req)

	dst := req.GetDst()
	work, err := os.MkdirTemp(filepath.Dir(dst), "avatarforcing-")
	if err != nil {
		return fmt.Errorf("avatarforcing-cpp: creating the work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	portrait, err := preparePortrait(a.ffmpeg, req.GetStartImage(), a.info.Width, a.info.Height, work)
	if err != nil {
		return err
	}
	wav, err := prepareAudio(a.ffmpeg, req.GetAudio(), work)
	if err != nil {
		return err
	}

	xlog.Info("[avatarforcing-cpp] GenerateVideo", "dst", dst, "request", ro.json())
	if CppGenerateMp4(a.ctx, portrait, wav, dst, ro.json()) != 0 {
		return fmt.Errorf("avatarforcing-cpp: generation failed: %s", CppLastError(a.ctx))
	}
	return nil
}

// checkVideoRequest checks the inputs that every generation needs.
func checkVideoRequest(req *pb.GenerateVideoRequest) error {
	var missing []string
	if req.GetStartImage() == "" {
		missing = append(missing, "a portrait (start_image)")
	}
	if req.GetAudio() == "" {
		missing = append(missing, "speech audio (audio)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("avatarforcing-cpp: a talking-head video needs %s", strings.Join(missing, " and "))
	}
	if req.GetDst() == "" {
		return errors.New("avatarforcing-cpp: the output path (dst) is not set")
	}
	if req.GetEndImage() != "" || len(req.GetRefImages()) > 0 || len(req.GetRefVideos()) > 0 || len(req.GetRefAudios()) > 0 {
		return errors.New("avatarforcing-cpp: end_image and reference inputs are not supported; " +
			"send one portrait as start_image and the speech as audio")
	}
	return nil
}

// warnIgnored logs the request fields that have no meaning for this model.
func (a *AvatarForcing) warnIgnored(req *pb.GenerateVideoRequest) {
	if req.GetPrompt() != "" || req.GetNegativePrompt() != "" {
		xlog.Debug("[avatarforcing-cpp] the model is driven by audio; ignoring the prompt")
	}
	if w, h := req.GetWidth(), req.GetHeight(); (w > 0 && int(w) != a.info.Width) || (h > 0 && int(h) != a.info.Height) {
		xlog.Warn("[avatarforcing-cpp] the model renders at a fixed size; ignoring width and height",
			"width", a.info.Width, "height", a.info.Height)
	}
	if f := req.GetFps(); f > 0 && int(f) != a.info.Fps {
		xlog.Warn("[avatarforcing-cpp] the model renders at a fixed frame rate; ignoring fps", "fps", a.info.Fps)
	}
	if req.GetNumFrames() > 0 {
		xlog.Warn("[avatarforcing-cpp] the length of the video follows the audio; ignoring num_frames")
	}
}

// resolveFfmpeg finds the ffmpeg program. name empty means "ffmpeg" on PATH.
func resolveFfmpeg(name string) (string, error) {
	if name == "" {
		name = "ffmpeg"
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("avatarforcing-cpp: ffmpeg %q not found: the backend needs ffmpeg to read the inputs "+
			"and to write the mp4; install it or set options: [ffmpeg:<path>]: %w", name, err)
	}
	return p, nil
}

// preparePortrait gives a path to a portrait the library can read: a PNG or
// JPEG at width x height is used as it is; any other image is cropped to a
// centred square and scaled to the model size. No face detection is done, so
// the face must be near the centre of the image.
func preparePortrait(ffmpeg, src string, width, height int, work string) (string, error) {
	if w, h, err := imageSize(src); err == nil && w == width && h == height {
		return src, nil
	}
	out := filepath.Join(work, "portrait.png")
	vf := fmt.Sprintf("crop='min(iw,ih)':'min(iw,ih)',scale=%d:%d:flags=lanczos", width, height)
	if err := runFfmpeg(ffmpeg, "-i", src, "-frames:v", "1", "-vf", vf, "-pix_fmt", "rgb24", out); err != nil {
		return "", fmt.Errorf("avatarforcing-cpp: reading the portrait (start_image): %w", err)
	}
	return out, nil
}

// prepareAudio converts the speech to a 16-bit PCM WAV file. The sample rate
// and the channels stay as they are: the library resamples to 16 kHz mono for
// the model, and the mp4 gets the audio at its own quality.
func prepareAudio(ffmpeg, src, work string) (string, error) {
	out := filepath.Join(work, "speech.wav")
	if err := runFfmpeg(ffmpeg, "-i", src, "-vn", "-acodec", "pcm_s16le", out); err != nil {
		return "", fmt.Errorf("avatarforcing-cpp: reading the audio: %w", err)
	}
	return out, nil
}

func imageSize(path string) (int, int, error) {
	f, err := os.Open(path) // #nosec G304 -- a path staged by LocalAI for this request
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

func runFfmpeg(ffmpeg string, args ...string) error {
	args = append([]string{"-y", "-loglevel", "error", "-nostdin"}, args...)
	out, err := exec.Command(ffmpeg, args...).CombinedOutput() // #nosec G204 -- a resolved binary, literal flags and staged paths
	if err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Free releases the context when LocalAI unloads the model.
func (a *AvatarForcing) Free() error {
	if a.ctx != 0 {
		CppFree(a.ctx)
		a.ctx = 0
	}
	return nil
}

// goStringFromCPtr copies a NUL-terminated C string into Go memory. The
// pointer addresses a malloc'd buffer of the library that the Go GC does not
// track or move; the caller frees it after the copy.
func goStringFromCPtr(cptr uintptr) string {
	if cptr == 0 {
		return ""
	}
	p := unsafe.Pointer(cptr) //nolint:govet // C-owned malloc'd buffer, not Go-GC memory
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}
