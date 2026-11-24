// SPDX-License-Identifier: MIT
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type SkinTokens struct {
	base.Base
	mu    sync.Mutex
	model uintptr
}

func nativeError(buffer []byte) string {
	if end := bytes.IndexByte(buffer, 0); end >= 0 {
		buffer = buffer[:end]
	}
	return string(buffer)
}

func (s *SkinTokens) Load(options *pb.ModelOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(options.ModelFile) == "" || strings.ContainsRune(options.ModelFile, 0) {
		return fmt.Errorf("SkinTokens model bundle is required")
	}
	bundle := options.ModelFile
	if !filepath.IsAbs(bundle) {
		bundle = filepath.Join(options.ModelPath, bundle)
	}
	device := "auto"
	for _, option := range options.Options {
		key, value, ok := strings.Cut(option, ":")
		if !ok || key != "device" {
			return fmt.Errorf("unsupported SkinTokens option %q", option)
		}
		device = value
	}
	if device != "auto" && device != "cpu" && device != "vulkan" {
		return fmt.Errorf("device must be auto, cpu or vulkan")
	}
	threads := options.Threads
	if threads <= 0 {
		threads = int32(runtime.NumCPU())
	}
	var loaded uintptr
	buffer := make([]byte, 2048)
	code := nativeLoad(bundle, device, uint32(threads), os.Getenv("SKINTOKENS_BACKEND_DIR"), &loaded, &buffer[0], uintptr(len(buffer)))
	if code != 0 || loaded == 0 {
		return fmt.Errorf("loading SkinTokens (code %d): %s", code, nativeError(buffer))
	}
	if s.model != 0 {
		nativeFree(s.model)
	}
	s.model = loaded
	return nil
}

func (s *SkinTokens) Free() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.model != 0 {
		nativeFree(s.model)
		s.model = 0
	}
	return nil
}

func validateRequest(request *pb.Animate3DRequest) (operation, mesh, secondary string, err error) {
	fail := func(message string) (string, string, string, error) { return "", "", "", fmt.Errorf("%s", message) }
	if request == nil || request.Dst == "" || strings.ContainsRune(request.Dst, 0) {
		return fail("a GLB destination is required")
	}
	input := request.Inputs["mesh"]
	if input == nil || input.Type != "mesh" || input.Data == "" || strings.ContainsRune(input.Data, 0) {
		return fail("a mesh input is required")
	}
	mesh = input.Data
	operation = "rig"
	if len(request.Inputs) == 2 {
		if value := request.Inputs["skeleton"]; value != nil && (value.Type == "mesh" || value.Type == "motion") {
			operation, secondary = "skin", value.Data
		} else if value := request.Inputs["motion"]; value != nil && value.Type == "motion" {
			operation, secondary = "retarget", value.Data
		} else {
			return fail("expected a skeleton or motion secondary input")
		}
		if secondary == "" || strings.ContainsRune(secondary, 0) {
			return fail("secondary input is empty or invalid")
		}
	} else if len(request.Inputs) != 1 {
		return fail("unsupported SkinTokens inputs")
	}
	parameters := map[string]schema.ThreeDParameter{}
	for _, parameter := range schema.SkinTokensParameters(operation) {
		parameters[parameter.Name] = parameter
	}
	for name, value := range request.Params {
		parameter, ok := parameters[name]
		if !ok {
			return fail("unsupported parameter: " + name)
		}
		if err := parameter.Validate(value); err != nil {
			return "", "", "", err
		}
	}
	return operation, mesh, secondary, nil
}

func (s *SkinTokens) Animate3D(request *pb.Animate3DRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, mesh, secondary, err := validateRequest(request)
	if err != nil {
		return err
	}
	if s.model == 0 {
		return fmt.Errorf("SkinTokens model is not loaded")
	}
	for _, input := range []string{mesh, secondary} {
		if input != "" && filepath.Clean(input) == filepath.Clean(request.Dst) {
			return fmt.Errorf("destination must not replace an input")
		}
	}
	output, err := os.CreateTemp(filepath.Dir(request.Dst), ".skintokens-*.glb")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(output.Name()) }()
	if err := output.Close(); err != nil {
		return err
	}
	params := request.Params
	if params == nil {
		params = map[string]string{}
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	buffer := make([]byte, 2048)
	var learned int32
	code := nativeRun(s.model, operation, mesh, secondary, output.Name(), string(encoded), &learned, &buffer[0], uintptr(len(buffer)))
	if code != 0 {
		return fmt.Errorf("SkinTokens %s (code %d): %s", operation, code, nativeError(buffer))
	}
	if operation != "retarget" && learned == 0 {
		return fmt.Errorf("SkinTokens returned geometric weights rather than learned weights")
	}
	if err := preserveMaterials(mesh, output.Name()); err != nil {
		return fmt.Errorf("preserving mesh materials: %w", err)
	}
	return os.Rename(output.Name(), request.Dst)
}
