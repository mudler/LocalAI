// SPDX-License-Identifier: MIT
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	nativeABI     func() uint32
	nativeProcess func(*float32, int32, *int32, int32, *float32, string, *byte, uint32) uintptr
	nativeFree    func(uintptr)
	nativeSize    func(uintptr) uint64
	nativeData    func(uintptr) *byte
	nativeStats   func(uintptr) string
)

func loadNativeLibrary(path string) error {
	lib, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	for _, binding := range []struct {
		function any
		name     string
	}{
		{&nativeABI, "mr_abi_version"}, {&nativeProcess, "mr_process"}, {&nativeFree, "mr_result_free"},
		{&nativeSize, "mr_result_size"}, {&nativeData, "mr_result_data"}, {&nativeStats, "mr_result_stats"},
	} {
		purego.RegisterLibFunc(binding.function, lib, binding.name)
	}
	if nativeABI() != 1 {
		return fmt.Errorf("unsupported mesh remesher ABI")
	}
	return nil
}

func remeshNative(vertices []float32, triangles []int32, pbr []float32, params map[string]string) ([]byte, string, error) {
	if len(vertices) < 9 || len(vertices)%3 != 0 || len(vertices) > 30000000 || len(triangles) < 3 || len(triangles)%3 != 0 || len(triangles) > 60000000 || (len(pbr) != 0 && len(pbr) != 2*len(vertices)) {
		return nil, "", fmt.Errorf("invalid mesh buffer dimensions")
	}
	if params == nil {
		params = map[string]string{}
	}
	parameters, err := json.Marshal(params)
	if err != nil {
		return nil, "", err
	}
	var material *float32
	if len(pbr) != 0 {
		material = &pbr[0]
	}
	errorBuffer := make([]byte, 2048)
	result := nativeProcess(&vertices[0], int32(len(vertices)/3), &triangles[0], int32(len(triangles)/3), material, string(parameters), &errorBuffer[0], uint32(len(errorBuffer)))
	if result == 0 {
		if end := bytes.IndexByte(errorBuffer, 0); end >= 0 {
			errorBuffer = errorBuffer[:end]
		}
		return nil, "", fmt.Errorf("remeshing: %s", errorBuffer)
	}
	defer nativeFree(result)
	length, data := nativeSize(result), nativeData(result)
	if length < 28 || length > 512<<20 || data == nil {
		return nil, "", fmt.Errorf("remesher returned invalid or oversized GLB")
	}
	return bytes.Clone(unsafe.Slice(data, int(length))), nativeStats(result), nil
}
