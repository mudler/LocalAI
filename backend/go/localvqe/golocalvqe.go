package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// localvqeSampleRate is the only sample rate currently supported by the
// upstream LocalVQE model. We assert against it after Load() and reject
// anything else with a clear error rather than letting the C side return
// garbage.
const localvqeSampleRate = 16000

// Param map keys understood by LocalVQE. Keep these strings in sync with
// schema.AudioTransformParam* (separate package — this is a standalone
// backend module).
const (
	paramNoiseGate          = "noise_gate"
	paramNoiseGateThreshold = "noise_gate_threshold_dbfs"
)

// Option keys read from ModelOptions.Options[] at Load() time. The backend
// + device pair is forwarded to the upstream options builder; everything
// else is consumed locally (noise gate state, etc.).
const (
	optionBackend = "backend"
	optionDevice  = "device"
)

// purego-bound entry points from liblocalvqe.
//
// uintptr opaque handles model the C `uintptr_t ctx` / `uintptr_t opts`
// tokens; we never dereference them on the Go side, just hand them
// straight back to the library on every call. Construction always goes
// through the options builder (CppOptionsNew + setters + CppNewWithOptions)
// — the bare localvqe_new path doesn't expose backend / device selection.
var (
	CppOptionsNew           func() uintptr
	CppOptionsFree          func(opts uintptr)
	CppOptionsSetModelPath  func(opts uintptr, modelPath string) int32
	CppOptionsSetBackend    func(opts uintptr, backend string) int32
	CppOptionsSetDevice     func(opts uintptr, device int32) int32
	CppNewWithOptions       func(opts uintptr) uintptr
	CppFree                 func(ctx uintptr)
	CppProcessF32           func(ctx uintptr, mic, ref uintptr, nSamples int32, out uintptr) int32
	CppProcessS16           func(ctx uintptr, mic, ref uintptr, nSamples int32, out uintptr) int32
	CppProcessFrameF32      func(ctx uintptr, mic, ref uintptr, hopSamples int32, out uintptr) int32
	CppProcessFrameS16      func(ctx uintptr, mic, ref uintptr, hopSamples int32, out uintptr) int32
	CppReset                func(ctx uintptr)
	CppLastError            func(ctx uintptr) string
	CppSampleRate           func(ctx uintptr) int32
	CppHopLength            func(ctx uintptr) int32
	CppFFTSize              func(ctx uintptr) int32
	CppSetNoiseGate         func(ctx uintptr, enabled int32, thresholdDBFS float32) int32
	CppGetNoiseGate         func(ctx uintptr, enabledOut, thresholdDBFSOut uintptr) int32
)

// LocalVQE speaks gRPC against LocalVQE's flat C ABI. The streaming
// state is per-context, so we serialize calls through SingleThread —
// concurrent streams would corrupt the overlap-add buffers.
type LocalVQE struct {
	base.SingleThread
	ctx        uintptr // 0 when unloaded
	sampleRate int
	hopLength  int
	fftSize    int

	// modelRoot resolves relative paths from Options[].
	modelRoot string

	// Cached gate config so we can re-apply on each AudioTransform call
	// without paying for a CGo round-trip every time. Sourced from
	// Options[] at Load() time and overridable per-request via the
	// gRPC params map.
	gateEnabled bool
	gateDbfs    float32

	// Backend / device picked via Options[]. Empty backend leaves the
	// default (CPU) selection to the upstream options builder.
	backend string
	device  int32
}

// parseOptions reads opts.Options[] for backend-specific tuning. Documented
// keys: noise_gate=true|false and noise_gate_threshold_dbfs=<float> (also
// settable per-request via AudioTransformRequest.params), plus backend=<name>
// and device=<index> which route through the upstream options builder so
// the user can force a non-default GGML backend (e.g. "Vulkan").
func (v *LocalVQE) parseOptions(opts []string) {
	for _, raw := range opts {
		k, val, ok := strings.Cut(raw, "=")
		if !ok {
			k, val, ok = strings.Cut(raw, ":")
			if !ok {
				continue
			}
		}
		key := strings.TrimSpace(strings.ToLower(k))
		val = strings.TrimSpace(val)
		switch key {
		case paramNoiseGate:
			if b, err := strconv.ParseBool(val); err == nil {
				v.gateEnabled = b
			}
		case paramNoiseGateThreshold:
			if f, err := strconv.ParseFloat(val, 32); err == nil {
				v.gateDbfs = float32(f)
			}
		case optionBackend:
			v.backend = val
		case optionDevice:
			if d, err := strconv.Atoi(val); err == nil && d >= 0 {
				v.device = int32(d)
			}
		}
	}
}

// newCtxWithOptions builds a context via the upstream options-builder so we
// can pass backend / device in addition to the model path. Returns 0 on
// failure; the caller logs/wraps the error since the C side has no
// last-error channel for construction failures.
func newCtxWithOptions(modelPath, backend string, device int32) uintptr {
	o := CppOptionsNew()
	if o == 0 {
		return 0
	}
	defer CppOptionsFree(o)
	if rc := CppOptionsSetModelPath(o, modelPath); rc != 0 {
		return 0
	}
	if backend != "" {
		if rc := CppOptionsSetBackend(o, backend); rc != 0 {
			return 0
		}
	}
	if device > 0 {
		if rc := CppOptionsSetDevice(o, device); rc != 0 {
			return 0
		}
	}
	return CppNewWithOptions(o)
}

func (v *LocalVQE) Load(opts *pb.ModelOptions) error {
	if opts.ModelFile == "" {
		return fmt.Errorf("localvqe: ModelFile is required")
	}

	modelFile := opts.ModelFile
	if !filepath.IsAbs(modelFile) && opts.ModelPath != "" {
		modelFile = filepath.Join(opts.ModelPath, modelFile)
	}
	v.modelRoot = opts.ModelPath
	if v.modelRoot == "" {
		v.modelRoot = filepath.Dir(modelFile)
	}

	// Defaults — gate off, threshold at -45 dBFS as a reasonable starting
	// point per the upstream localvqe_api.h documentation.
	v.gateEnabled = false
	v.gateDbfs = -45.0
	v.parseOptions(opts.Options)

	// localvqe_new reads GGML_NTHREADS at construction time; without it
	// the C side falls back to single-threaded compute (~1× realtime
	// instead of the documented ~9× on a multi-core CPU). Pass the
	// model config's Threads through, defaulting to min(NumCPU, 4).
	//
	// LocalVQE is 1.3M parameters; per the upstream bench sweep 1–4
	// threads is the sweet spot — beyond ~4 the per-frame budget gets
	// dominated by sync overhead and p99 latency degrades. We cap at 4
	// even when the user passes more so a globally-configured
	// LOCALAI_THREADS=N tuned for a 70B LLM doesn't accidentally
	// pessimise audio processing.
	const localvqeMaxThreads = 4
	threads := int(opts.Threads)
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	if threads > localvqeMaxThreads {
		threads = localvqeMaxThreads
	}
	if threads < 1 {
		threads = 1
	}
	if err := os.Setenv("GGML_NTHREADS", fmt.Sprintf("%d", threads)); err != nil {
		return fmt.Errorf("localvqe: setenv GGML_NTHREADS: %w", err)
	}

	xlog.Info("[localvqe] loading model", "path", modelFile, "threads", threads, "backend", v.backend, "device", v.device, "noise_gate", v.gateEnabled, "threshold_dbfs", v.gateDbfs)

	ctx := newCtxWithOptions(modelFile, v.backend, v.device)
	if ctx == 0 {
		return fmt.Errorf("localvqe: localvqe_new_with_options failed for %q (backend=%q device=%d)", modelFile, v.backend, v.device)
	}
	v.ctx = ctx

	v.sampleRate = int(CppSampleRate(ctx))
	v.hopLength = int(CppHopLength(ctx))
	v.fftSize = int(CppFFTSize(ctx))

	if v.sampleRate != localvqeSampleRate {
		CppFree(ctx)
		v.ctx = 0
		return fmt.Errorf("localvqe: unsupported sample rate %d (only %d Hz is supported)", v.sampleRate, localvqeSampleRate)
	}
	if v.hopLength <= 0 || v.fftSize <= 0 {
		CppFree(ctx)
		v.ctx = 0
		return fmt.Errorf("localvqe: model reports invalid hop=%d fft=%d", v.hopLength, v.fftSize)
	}

	if v.gateEnabled {
		if rc := CppSetNoiseGate(ctx, 1, v.gateDbfs); rc != 0 {
			err := fmt.Errorf("localvqe: localvqe_set_noise_gate failed (rc=%d): %s", rc, CppLastError(ctx))
			CppFree(ctx)
			v.ctx = 0
			return err
		}
	}

	return nil
}

func (v *LocalVQE) Free() error {
	if v.ctx != 0 {
		CppFree(v.ctx)
		v.ctx = 0
	}
	return nil
}

// applyParams forwards backend-specific tuning to the C side per call.
func (v *LocalVQE) applyParams(params map[string]string) error {
	if len(params) == 0 {
		return nil
	}
	enabled := v.gateEnabled
	threshold := v.gateDbfs
	updated := false

	if val, ok := params[paramNoiseGate]; ok {
		if b, err := strconv.ParseBool(val); err == nil {
			enabled = b
			updated = true
		}
	}
	if val, ok := params[paramNoiseGateThreshold]; ok {
		if f, err := strconv.ParseFloat(val, 32); err == nil {
			threshold = float32(f)
			updated = true
		}
	}
	if !updated {
		return nil
	}

	gateOn := int32(0)
	if enabled {
		gateOn = 1
	}
	if rc := CppSetNoiseGate(v.ctx, gateOn, threshold); rc != 0 {
		return fmt.Errorf("localvqe_set_noise_gate failed (rc=%d): %s", rc, CppLastError(v.ctx))
	}
	v.gateEnabled = enabled
	v.gateDbfs = threshold
	return nil
}

func (v *LocalVQE) AudioTransform(req *pb.AudioTransformRequest) (*pb.AudioTransformResult, error) {
	if v.ctx == 0 {
		return nil, fmt.Errorf("localvqe: no model loaded")
	}
	if req.AudioPath == "" || req.Dst == "" {
		return nil, fmt.Errorf("localvqe: audio_path and dst are required")
	}

	if err := v.applyParams(req.Params); err != nil {
		return nil, err
	}

	mic, micRate, err := readMonoWAVf32(req.AudioPath)
	if err != nil {
		return nil, fmt.Errorf("read audio: %w", err)
	}
	if micRate != v.sampleRate {
		return nil, fmt.Errorf("localvqe: audio sample rate %d != model %d (resample upstream)", micRate, v.sampleRate)
	}

	refProvided := req.ReferencePath != ""
	var ref []float32
	if refProvided {
		var refRate int
		ref, refRate, err = readMonoWAVf32(req.ReferencePath)
		if err != nil {
			return nil, fmt.Errorf("read reference: %w", err)
		}
		if refRate != v.sampleRate {
			return nil, fmt.Errorf("localvqe: reference sample rate %d != model %d", refRate, v.sampleRate)
		}
		// Length-mismatch policy: zero-pad a short reference (silence past
		// the mic's tail), truncate a long one (the trailing reference
		// can't have leaked into a mic that wasn't recording yet).
		switch {
		case len(ref) < len(mic):
			padded := make([]float32, len(mic))
			copy(padded, ref)
			ref = padded
		case len(ref) > len(mic):
			ref = ref[:len(mic)]
		}
	} else {
		ref = make([]float32, len(mic))
	}

	if len(mic) < v.fftSize {
		return nil, fmt.Errorf("localvqe: audio too short (%d samples, need ≥ %d)", len(mic), v.fftSize)
	}

	out := make([]float32, len(mic))
	rc := CppProcessF32(v.ctx,
		uintptr(unsafe.Pointer(&mic[0])),
		uintptr(unsafe.Pointer(&ref[0])),
		int32(len(mic)),
		uintptr(unsafe.Pointer(&out[0])))
	if rc != 0 {
		return nil, fmt.Errorf("localvqe_process_f32 failed (rc=%d): %s", rc, CppLastError(v.ctx))
	}

	if err := writeMonoWAVf32(req.Dst, out, v.sampleRate); err != nil {
		return nil, fmt.Errorf("write output: %w", err)
	}

	return &pb.AudioTransformResult{
		Dst:               req.Dst,
		SampleRate:        int32(v.sampleRate),
		Samples:           int32(len(out)),
		ReferenceProvided: refProvided,
	}, nil
}

// AudioTransformStream runs the bidirectional streaming path. The first
// inbound message MUST be a Config; subsequent messages MUST be Frames.
// A second Config mid-stream resets the streaming state.
func (v *LocalVQE) AudioTransformStream(in <-chan *pb.AudioTransformFrameRequest, out chan<- *pb.AudioTransformFrameResponse) error {
	defer close(out)

	if v.ctx == 0 {
		return fmt.Errorf("localvqe: no model loaded")
	}

	first, ok := <-in
	if !ok {
		return nil
	}
	cfg := first.GetConfig()
	if cfg == nil {
		return fmt.Errorf("localvqe: first stream message must be a Config")
	}
	if err := v.applyStreamConfig(cfg); err != nil {
		return err
	}

	hop := v.hopLength
	if cfg.FrameSamples != 0 && int(cfg.FrameSamples) != hop {
		return fmt.Errorf("localvqe: frame_samples=%d != hop_length=%d", cfg.FrameSamples, hop)
	}

	// Pre-allocated scratch buffers for the C-side process call. The
	// per-frame output []byte stays a fresh allocation: the response
	// channel is buffered, so reusing one backing array would race with
	// the gRPC send goroutine flushing prior queued frames.
	micF32 := make([]float32, hop)
	refF32 := make([]float32, hop)
	outF32 := make([]float32, hop)
	micS16 := make([]int16, hop)
	refS16 := make([]int16, hop)
	outS16 := make([]int16, hop)

	useS16 := cfg.SampleFormat == pb.AudioTransformStreamConfig_S16_LE
	frameSize := hop * 4
	if useS16 {
		frameSize = hop * 2
	}

	frameIndex := int64(0)
	for req := range in {
		switch payload := req.Payload.(type) {
		case *pb.AudioTransformFrameRequest_Config:
			if err := v.applyStreamConfig(payload.Config); err != nil {
				return err
			}
			if payload.Config.Reset_ {
				CppReset(v.ctx)
				frameIndex = 0
			}
			continue
		case *pb.AudioTransformFrameRequest_Frame:
			if len(payload.Frame.AudioPcm) != frameSize {
				return fmt.Errorf("localvqe: frame audio bytes=%d expected=%d", len(payload.Frame.AudioPcm), frameSize)
			}
			refBuf := payload.Frame.ReferencePcm
			if len(refBuf) != 0 && len(refBuf) != frameSize {
				return fmt.Errorf("localvqe: frame reference bytes=%d expected=%d (or 0)", len(refBuf), frameSize)
			}

			var outBytes []byte
			if useS16 {
				if err := decodeS16LE(payload.Frame.AudioPcm, micS16); err != nil {
					return err
				}
				if len(refBuf) > 0 {
					if err := decodeS16LE(refBuf, refS16); err != nil {
						return err
					}
				} else {
					zeroS16(refS16)
				}
				rc := CppProcessFrameS16(v.ctx,
					uintptr(unsafe.Pointer(&micS16[0])),
					uintptr(unsafe.Pointer(&refS16[0])),
					int32(hop),
					uintptr(unsafe.Pointer(&outS16[0])))
				if rc != 0 {
					return fmt.Errorf("localvqe_process_frame_s16 (rc=%d): %s", rc, CppLastError(v.ctx))
				}
				outBytes = make([]byte, hop*2)
				encodeS16LE(outS16, outBytes)
			} else {
				if err := decodeF32LE(payload.Frame.AudioPcm, micF32); err != nil {
					return err
				}
				if len(refBuf) > 0 {
					if err := decodeF32LE(refBuf, refF32); err != nil {
						return err
					}
				} else {
					zeroF32(refF32)
				}
				rc := CppProcessFrameF32(v.ctx,
					uintptr(unsafe.Pointer(&micF32[0])),
					uintptr(unsafe.Pointer(&refF32[0])),
					int32(hop),
					uintptr(unsafe.Pointer(&outF32[0])))
				if rc != 0 {
					return fmt.Errorf("localvqe_process_frame_f32 (rc=%d): %s", rc, CppLastError(v.ctx))
				}
				outBytes = make([]byte, hop*4)
				encodeF32LE(outF32, outBytes)
			}
			out <- &pb.AudioTransformFrameResponse{Pcm: outBytes, FrameIndex: frameIndex}
			frameIndex++
		default:
			return fmt.Errorf("localvqe: unexpected stream payload %T", payload)
		}
	}
	return nil
}

func zeroS16(s []int16) {
	for i := range s {
		s[i] = 0
	}
}

func zeroF32(s []float32) {
	for i := range s {
		s[i] = 0
	}
}

func (v *LocalVQE) applyStreamConfig(cfg *pb.AudioTransformStreamConfig) error {
	if cfg.SampleRate != 0 && int(cfg.SampleRate) != v.sampleRate {
		return fmt.Errorf("localvqe: sample_rate=%d != model %d", cfg.SampleRate, v.sampleRate)
	}
	return v.applyParams(cfg.Params)
}

// ---- WAV I/O ----------------------------------------------------------
//
// Minimal mono PCM WAV reader/writer. Only handles the subset LocalVQE
// cares about (mono, 16-bit signed, no extensible chunks). For broader
// audio support the HTTP layer's `audio.NormalizeAudioFile` already
// converts arbitrary input to a canonical WAV before we see it; this
// reader just decodes the canonical shape.

func readMonoWAVf32(path string) ([]float32, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	header := make([]byte, 44)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, 0, err
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("not a WAV file")
	}
	channels := binary.LittleEndian.Uint16(header[22:24])
	sampleRate := binary.LittleEndian.Uint32(header[24:28])
	bitsPerSample := binary.LittleEndian.Uint16(header[34:36])

	if channels != 1 {
		return nil, 0, fmt.Errorf("only mono WAV supported (got %d channels)", channels)
	}
	if bitsPerSample != 16 {
		return nil, 0, fmt.Errorf("only 16-bit PCM supported (got %d bits)", bitsPerSample)
	}

	rest, err := io.ReadAll(f)
	if err != nil {
		return nil, 0, err
	}
	n := len(rest) / 2
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		s := int16(binary.LittleEndian.Uint16(rest[i*2 : i*2+2]))
		out[i] = float32(s) / 32768.0
	}
	return out, int(sampleRate), nil
}

func writeMonoWAVf32(path string, samples []float32, sampleRate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	dataLen := uint32(len(samples) * 2)
	header := make([]byte, 44)
	copy(header[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(header[4:8], 36+dataLen)
	copy(header[8:12], []byte("WAVE"))
	copy(header[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(header[16:20], 16)        // fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], 1)         // PCM
	binary.LittleEndian.PutUint16(header[22:24], 1)         // mono
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(sampleRate*2)) // byte rate
	binary.LittleEndian.PutUint16(header[32:34], 2)         // block align
	binary.LittleEndian.PutUint16(header[34:36], 16)        // bits per sample
	copy(header[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(header[40:44], dataLen)
	if _, err := f.Write(header); err != nil {
		return err
	}

	body := make([]byte, len(samples)*2)
	for i, s := range samples {
		clamped := s * 32768.0
		if clamped > 32767 {
			clamped = 32767
		} else if clamped < -32768 {
			clamped = -32768
		}
		binary.LittleEndian.PutUint16(body[i*2:i*2+2], uint16(int16(clamped)))
	}
	_, err = f.Write(body)
	return err
}

// ---- PCM endec helpers ------------------------------------------------

func decodeS16LE(buf []byte, out []int16) error {
	if len(buf) != len(out)*2 {
		return fmt.Errorf("decodeS16LE: buf=%d out=%d", len(buf), len(out))
	}
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(buf[i*2 : i*2+2]))
	}
	return nil
}

func encodeS16LE(in []int16, out []byte) {
	for i, s := range in {
		binary.LittleEndian.PutUint16(out[i*2:i*2+2], uint16(s))
	}
}

func decodeF32LE(buf []byte, out []float32) error {
	if len(buf) != len(out)*4 {
		return fmt.Errorf("decodeF32LE: buf=%d out=%d", len(buf), len(out))
	}
	for i := range out {
		bits := binary.LittleEndian.Uint32(buf[i*4 : i*4+4])
		out[i] = *(*float32)(unsafe.Pointer(&bits))
	}
	return nil
}

func encodeF32LE(in []float32, out []byte) {
	for i, s := range in {
		bits := *(*uint32)(unsafe.Pointer(&s))
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], bits)
	}
}
