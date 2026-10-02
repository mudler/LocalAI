// SPDX-License-Identifier: MIT
package main

import (
	"bytes"
	"fmt"
	"github.com/ebitengine/purego"
)

var liveCreate func(string, string, string, string, string, *uint32, int64, *uintptr, *byte, uint64) int32
var liveDestroy func(uintptr)
var liveDefinition func(uintptr, *byte, uint64, *uint64, *byte, uint64) int32
var liveReset func(uintptr, *byte, uint64) int32
var liveSubmit func(uintptr, *byte, uint64, uint32, uint32, uint64, uint64, int64, *float32, uint64, uint64, *uintptr, *byte, uint64) int32
var resultDestroy func(uintptr)
var resultInfo func(uintptr, *uint64, *int64, *uint64, *uint64, *uint32, *uint32, *byte, uint64) int32
var resultIntervalStart func(uintptr, *int64, *byte, uint64) int32
var resultCopy func(uintptr, uint32, *float32, uint64, *uint64, *byte, uint64) int32

func loadNativeLibrary(path string) error {
	lib, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	for _, b := range []struct {
		fn   any
		name string
	}{
		{&liveCreate, "localai_gemx_create"}, {&liveDestroy, "gemx_live_destroy"},
		{&liveDefinition, "gemx_live_definition"}, {&liveReset, "gemx_live_reset"},
		{&liveSubmit, "gemx_live_submit"}, {&resultDestroy, "gemx_live_result_destroy"},
		{&resultInfo, "gemx_live_result_info"}, {&resultCopy, "gemx_live_result_copy"},
	} {
		symbol, err := purego.Dlsym(lib, b.name)
		if err != nil {
			return fmt.Errorf("streaming ABI: %w", err)
		}
		purego.RegisterFunc(b.fn, symbol)
	}
	// Optional additive ABI; old libraries can still stream poses.
	resultIntervalStart = nil
	if symbol, err := purego.Dlsym(lib, "gemx_live_result_interval_start"); err == nil {
		purego.RegisterFunc(&resultIntervalStart, symbol)
	}
	return nil
}
func nativeError(code int32, buf []byte) error {
	if code == 0 {
		return nil
	}
	return fmt.Errorf("gemx (%d): %s", code, bytes.TrimRight(buf, "\x00"))
}
