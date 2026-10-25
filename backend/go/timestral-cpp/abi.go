package main

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// purego-bound entry points from libtimestral.so. Names match
// timestral_capi.h exactly, so `nm libtimestral.so | grep timestral_capi` is
// enough to spot drift.
//
// The context and the malloc'd forecast buffer are uintptr so the raw pointer
// comes back and can be released with timestral_capi_free_buffer. A float*
// return typed as a Go slice would be copied and the C buffer leaked.
var (
	CppAbiVersion      func() int32
	CppLoad            func(ggufPath string, threads int32) uintptr
	CppFree            func(ctx uintptr)
	CppLastError       func(ctx uintptr) string
	CppNumQuantiles    func(ctx uintptr) int32
	CppQuantileLevel   func(ctx uintptr, i int32) float64
	CppForecast        func(ctx uintptr, req unsafe.Pointer, outQ unsafe.Pointer) uintptr
	CppFreeBuffer      func(p uintptr)
	abiVersionRequired int32 = 1
)

// forecastRequest mirrors timestral_forecast_request (ABI version 1). Go lays
// the struct out like a 64-bit C compiler does, and abi_test.go checks every
// offset. The pointer fields are unsafe.Pointer, not uintptr, so the buffers
// they name stay reachable for the garbage collector until the call returns.
type forecastRequest struct {
	context         unsafe.Pointer // B*V*T float32, NaN = missing
	observationMask unsafe.Pointer // B*V*T bytes or nil; not used by this backend
	B, V, T         int32
	predictionLen   int32
	pastCov         unsafe.Pointer // B*c_past*T float32
	cPast           int32
	knownCov        unsafe.Pointer // B*c_known*(T+H) float32
	cKnown          int32
	catPast         unsafe.Pointer // B*cc_past*T float64
	ccPast          int32
	catKnown        unsafe.Pointer // B*cc_known*(T+H) float64
	ccKnown         int32
	symmetricAvg    int32 // -1 model default, 0, 1
	clipNonneg      int32 // -1 model default, 0, 1
	maxContext      int32 // 0 model default
}

// libFunc pairs a Go function variable with its C symbol.
type libFunc struct {
	ptr  any
	name string
}

// registerLibFuncs binds the eight timestral_capi_* entry points of an opened
// library. main.go and the integration tests share it.
func registerLibFuncs(lib uintptr) {
	for _, lf := range []libFunc{
		{&CppAbiVersion, "timestral_capi_abi_version"},
		{&CppLoad, "timestral_capi_load"},
		{&CppFree, "timestral_capi_free"},
		{&CppLastError, "timestral_capi_last_error"},
		{&CppNumQuantiles, "timestral_capi_num_quantiles"},
		{&CppQuantileLevel, "timestral_capi_quantile_level"},
		{&CppForecast, "timestral_capi_forecast"},
		{&CppFreeBuffer, "timestral_capi_free_buffer"},
	} {
		purego.RegisterLibFunc(lf.ptr, lib, lf.name)
	}
}
