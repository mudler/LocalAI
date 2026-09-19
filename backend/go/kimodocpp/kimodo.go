// SPDX-License-Identifier: MIT
package main

import (
	"bytes"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

type generationOptions struct {
	Size               uint32
	_                  uint32
	Seed               uint64
	Frames             uint32
	Steps              uint32
	TextGuidance       float32
	ConstraintGuidance float32
}

var (
	nativeABI         func() int32
	nativeLoad        func(string, string, string, uintptr, *byte, int32) uintptr
	nativeFree        func(uintptr)
	nativeGenerate    func(uintptr, string, *generationOptions, *byte, int32) uintptr
	nativeMotionFree  func(uintptr)
	nativeFrames      func(uintptr) int32
	nativeJoints      func(uintptr) int32
	nativeRotations   func(uintptr) *float32
	nativeRoots       func(uintptr) *float32
	nativeConfigure   func(string, int32, int32) int32
	nativeJointName   func(int32, int32) string
	nativeJointParent func(int32, int32) int32
	nativeJointOffset func(int32, int32) *float32
)

type Kimodo struct {
	base.Base
	mu       sync.Mutex
	model    uintptr
	defaults map[string]string
}

func parseGeneration(params map[string]string) (generationOptions, error) {
	options := generationOptions{Seed: 0, Frames: 150, Steps: 100, TextGuidance: 2, ConstraintGuidance: 2}
	options.Size = uint32(unsafe.Sizeof(options))
	for name, value := range params {
		switch name {
		case "seed", "frames", "steps":
			bits := 32
			if name == "seed" {
				bits = 64
			}
			number, err := strconv.ParseUint(value, 10, bits)
			if err != nil {
				return options, fmt.Errorf("invalid %s: %w", name, err)
			}
			switch name {
			case "seed":
				options.Seed = number
			case "frames":
				options.Frames = uint32(number)
			case "steps":
				options.Steps = uint32(number)
			}
		case "text_guidance":
			number, err := strconv.ParseFloat(value, 32)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > 100 {
				return options, fmt.Errorf("text_guidance must be finite and in 0..100")
			}
			options.TextGuidance = float32(number)
		default:
			return options, fmt.Errorf("unsupported animation parameter %q", name)
		}
	}
	if options.Frames < 60 || options.Frames > 150 {
		return options, fmt.Errorf("frames must be in 60..150")
	}
	if options.Steps < 1 || options.Steps > 1000 {
		return options, fmt.Errorf("steps must be in 1..1000")
	}
	return options, nil
}

func (k *Kimodo) Load(options *pb.ModelOptions) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	defaults := map[string]string{}
	motion := options.ModelFile
	if !filepath.IsAbs(motion) {
		motion = filepath.Join(options.ModelPath, motion)
	}
	textBundle := ""
	device := os.Getenv("KIMODO_BACKEND")
	if device == "" {
		device = "auto"
	}
	threads := int(options.Threads)
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	chunk := 32
	for _, option := range options.Options {
		name, value, ok := strings.Cut(option, ":")
		if !ok {
			return fmt.Errorf("invalid kimodo option %q", option)
		}
		switch name {
		case "text_bundle":
			textBundle = value
		case "device":
			device = value
		case "text_layer_chunk":
			var err error
			chunk, err = strconv.Atoi(value)
			if err != nil || chunk < 1 || chunk > 32 {
				return fmt.Errorf("text_layer_chunk must be in 1..32")
			}
		default:
			defaults[name] = value
		}
	}
	if _, err := parseGeneration(defaults); err != nil {
		return err
	}
	if textBundle == "" {
		return fmt.Errorf("text_bundle is required for text-to-motion")
	}
	if !filepath.IsAbs(textBundle) {
		textBundle = filepath.Join(options.ModelPath, textBundle)
	}
	if options.ModelFile == "" {
		return fmt.Errorf("motion model is required")
	}
	if code := nativeConfigure(device, int32(threads), int32(chunk)); code != 0 {
		return fmt.Errorf("cannot configure kimodo device %q (code %d)", device, code)
	}
	errorBuffer := make([]byte, 1024)
	loaded := nativeLoad(motion, textBundle, "", 0, &errorBuffer[0], int32(len(errorBuffer)))
	if loaded == 0 {
		return fmt.Errorf("loading kimodo: %s", nativeError(errorBuffer))
	}
	if k.model != 0 {
		nativeFree(k.model)
	}
	k.model, k.defaults = loaded, defaults
	xlog.Info("Kimodo loaded", "device", device, "threads", threads, "text_layer_chunk", chunk)
	return nil
}

func nativeError(buffer []byte) string {
	if end := bytes.IndexByte(buffer, 0); end >= 0 {
		buffer = buffer[:end]
	}
	return string(buffer)
}

func (k *Kimodo) Free() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.model != 0 {
		nativeFree(k.model)
		k.model = 0
	}
	return nil
}

func (k *Kimodo) Animate3D(request *pb.Animate3DRequest) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.model == 0 {
		return fmt.Errorf("kimodo model is not loaded")
	}
	prompt := request.Inputs["prompt"]
	if len(request.Inputs) != 1 || prompt == nil || prompt.Type != "text" ||
		strings.TrimSpace(prompt.Data) == "" || len(prompt.Data) > 4096 ||
		!utf8.ValidString(prompt.Data) || strings.ContainsRune(prompt.Data, 0) {
		return fmt.Errorf("kimodo requires one UTF-8 text prompt of 1..4096 bytes without NUL characters")
	}
	if request.Dst == "" {
		return fmt.Errorf("animation destination is required")
	}
	params := maps.Clone(k.defaults)
	if params == nil {
		params = map[string]string{}
	}
	maps.Copy(params, request.Params)
	options, err := parseGeneration(params)
	if err != nil {
		return err
	}
	errorBuffer := make([]byte, 1024)
	motion := nativeGenerate(k.model, prompt.Data, &options, &errorBuffer[0], int32(len(errorBuffer)))
	if motion == 0 {
		return fmt.Errorf("generating kimodo motion: %s", nativeError(errorBuffer))
	}
	defer nativeMotionFree(motion)
	frames, joints := nativeFrames(motion), nativeJoints(motion)
	if frames != int32(options.Frames) || (joints != 22 && joints != 30 && joints != 34) {
		return fmt.Errorf("unexpected kimodo motion dimensions: %d frames, %d joints", frames, joints)
	}
	roots, rotations := nativeRoots(motion), nativeRotations(motion)
	if roots == nil || rotations == nil {
		return fmt.Errorf("kimodo returned empty motion buffers")
	}
	skeleton := make([]animationJoint, joints)
	for joint := range joints {
		offset := nativeJointOffset(joints, joint)
		if offset == nil {
			return fmt.Errorf("missing skeleton joint %d", joint)
		}
		skeleton[joint] = animationJoint{Name: nativeJointName(joints, joint), Parent: int(nativeJointParent(joints, joint)), Offset: [3]float32(unsafe.Slice(offset, 3))}
	}
	return writeAnimationGLB(request.Dst, unsafe.Slice(roots, int(frames)*3), unsafe.Slice(rotations, int(frames*joints)*4), skeleton)
}
