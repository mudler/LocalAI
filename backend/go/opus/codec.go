package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

const (
	ApplicationVoIP               = 2048
	ApplicationAudio              = 2049
	ApplicationRestrictedLowDelay = 2051
)

var (
	initOnce sync.Once
	initErr  error

	opusLib uintptr
	shimLib uintptr

	// libopus functions
	cEncoderCreate  func(fs int32, channels int32, application int32, errPtr *int32) uintptr
	cEncode         func(st uintptr, pcm *int16, frameSize int32, data *byte, maxBytes int32) int32
	cEncoderDestroy func(st uintptr)

	cDecoderCreate  func(fs int32, channels int32, errPtr *int32) uintptr
	cDecode         func(st uintptr, data *byte, dataLen int32, pcm *int16, frameSize int32, decodeFec int32) int32
	cDecoderDestroy func(st uintptr)

	// shim functions (non-variadic wrappers for opus_encoder_ctl)
	cSetBitrate    func(st uintptr, bitrate int32) int32
	cSetComplexity func(st uintptr, complexity int32) int32
)

func loadLib(names []string) (uintptr, error) {
	var firstErr error
	for _, name := range names {
		h, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			return h, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return 0, firstErr
}

func ensureInit() error {
	initOnce.Do(func() {
		initErr = doInit()
	})
	return initErr
}

const shimHint = "ensure libopus-dev is installed and rebuild, or set OPUS_LIBRARY / OPUS_SHIM_LIBRARY env vars"

func doInit() error {
	opusNames := opusSearchPaths()
	var err error
	opusLib, err = loadLib(opusNames)
	if err != nil {
		return fmt.Errorf("opus: failed to load libopus (%s): %w", shimHint, err)
	}

	purego.RegisterLibFunc(&cEncoderCreate, opusLib, "opus_encoder_create")
	purego.RegisterLibFunc(&cEncode, opusLib, "opus_encode")
	purego.RegisterLibFunc(&cEncoderDestroy, opusLib, "opus_encoder_destroy")
	purego.RegisterLibFunc(&cDecoderCreate, opusLib, "opus_decoder_create")
	purego.RegisterLibFunc(&cDecode, opusLib, "opus_decode")
	purego.RegisterLibFunc(&cDecoderDestroy, opusLib, "opus_decoder_destroy")

	shimNames := shimSearchPaths()
	shimLib, err = loadLib(shimNames)
	if err != nil {
		return fmt.Errorf("opus: failed to load libopusshim (%s): %w", shimHint, err)
	}

	purego.RegisterLibFunc(&cSetBitrate, shimLib, "opus_shim_encoder_set_bitrate")
	purego.RegisterLibFunc(&cSetComplexity, shimLib, "opus_shim_encoder_set_complexity")

	return nil
}

func opusSearchPaths() []string {
	var paths []string

	if env := os.Getenv("OPUS_LIBRARY"); env != "" {
		paths = append(paths, env)
	}

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		paths = append(paths, filepath.Join(dir, "libopus.so.0"), filepath.Join(dir, "libopus.so"))
		if runtime.GOOS == "darwin" {
			paths = append(paths, filepath.Join(dir, "libopus.dylib"))
		}
	}

	paths = append(paths, "libopus.so.0", "libopus.so", "libopus.dylib", "opus.dll")

	if runtime.GOOS == "darwin" {
		paths = append(paths,
			"/opt/homebrew/lib/libopus.dylib",
			"/usr/local/lib/libopus.dylib",
		)
	}

	return paths
}

func shimSearchPaths() []string {
	var paths []string

	if env := os.Getenv("OPUS_SHIM_LIBRARY"); env != "" {
		paths = append(paths, env)
	}

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		paths = append(paths, filepath.Join(dir, "libopusshim.so"))
		if runtime.GOOS == "darwin" {
			paths = append(paths, filepath.Join(dir, "libopusshim.dylib"))
		}
	}

	paths = append(paths, "./libopusshim.so", "libopusshim.so")
	if runtime.GOOS == "darwin" {
		paths = append(paths, "./libopusshim.dylib", "libopusshim.dylib")
	}
	return paths
}

// Encoder wraps a libopus OpusEncoder via purego.
type Encoder struct {
	st uintptr
}

func NewEncoder(sampleRate, channels, application int) (*Encoder, error) {
	if err := ensureInit(); err != nil {
		return nil, err
	}

	var opusErr int32
	st := cEncoderCreate(int32(sampleRate), int32(channels), int32(application), &opusErr)
	if opusErr != 0 || st == 0 {
		return nil, fmt.Errorf("opus_encoder_create failed: error %d", opusErr)
	}
	return &Encoder{st: st}, nil
}

// Encode encodes a frame of PCM int16 samples. It returns the number of bytes
// written to out, or a negative error code.
func (e *Encoder) Encode(pcm []int16, frameSize int, out []byte) (int, error) {
	if len(pcm) == 0 || len(out) == 0 {
		return 0, errors.New("opus encode: empty input or output buffer")
	}
	n := cEncode(e.st, &pcm[0], int32(frameSize), &out[0], int32(len(out)))
	if n < 0 {
		return 0, fmt.Errorf("opus_encode failed: error %d", n)
	}
	return int(n), nil
}

func (e *Encoder) SetBitrate(bitrate int) error {
	if ret := cSetBitrate(e.st, int32(bitrate)); ret != 0 {
		return fmt.Errorf("opus set bitrate: error %d", ret)
	}
	return nil
}

func (e *Encoder) SetComplexity(complexity int) error {
	if ret := cSetComplexity(e.st, int32(complexity)); ret != 0 {
		return fmt.Errorf("opus set complexity: error %d", ret)
	}
	return nil
}

func (e *Encoder) Close() {
	if e.st != 0 {
		cEncoderDestroy(e.st)
		e.st = 0
	}
}

// Decoder wraps a libopus OpusDecoder via purego.
type Decoder struct {
	st uintptr
}

func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	if err := ensureInit(); err != nil {
		return nil, err
	}

	var opusErr int32
	st := cDecoderCreate(int32(sampleRate), int32(channels), &opusErr)
	if opusErr != 0 || st == 0 {
		return nil, fmt.Errorf("opus_decoder_create failed: error %d", opusErr)
	}
	return &Decoder{st: st}, nil
}

// Decode decodes an Opus packet into pcm. frameSize is the max number of
// samples per channel that pcm can hold. Returns the number of decoded samples
// per channel.
func (d *Decoder) Decode(data []byte, pcm []int16, frameSize int, fec bool) (int, error) {
	if len(pcm) == 0 {
		return 0, errors.New("opus decode: empty output buffer")
	}

	var dataPtr *byte
	var dataLen int32
	if len(data) > 0 {
		dataPtr = &data[0]
		dataLen = int32(len(data))
	}

	decodeFec := int32(0)
	if fec {
		decodeFec = 1
	}

	n := cDecode(d.st, dataPtr, dataLen, &pcm[0], int32(frameSize), decodeFec)
	if n < 0 {
		return 0, fmt.Errorf("opus_decode failed: error %d", n)
	}
	return int(n), nil
}

func (d *Decoder) Close() {
	if d.st != 0 {
		cDecoderDestroy(d.st)
		d.st = 0
	}
}

// Init eagerly loads the opus libraries, returning any error.
// Calling this is optional; the libraries are loaded lazily on first use.
func Init() error {
	return ensureInit()
}

// Reset allows re-initialization (for testing).
func Reset() {
	initOnce = sync.Once{}
	initErr = nil
	opusLib = 0
	shimLib = 0
}
