// SPDX-License-Identifier: MIT
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ebitengine/purego"
	"github.com/mudler/LocalAI/pkg/grpc"
)

var (
	nativeABI  func() uint32
	nativeLoad func(string, string, uint32, string, *uintptr, *byte, uintptr) int32
	nativeFree func(uintptr)
	nativeRun  func(uintptr, string, string, string, string, string, *int32, *byte, uintptr) int32
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
		{&nativeABI, "st_abi_version"}, {&nativeLoad, "localai_skintokens_load"},
		{&nativeFree, "st_model_free"}, {&nativeRun, "localai_skintokens_run"},
	} {
		purego.RegisterLibFunc(binding.function, lib, binding.name)
	}
	if version := nativeABI(); version != 1 {
		return fmt.Errorf("SkinTokens ABI mismatch: expected 1, got %d", version)
	}
	return nil
}

func main() {
	addr := flag.String("addr", "localhost:50051", "gRPC listen address")
	flag.Parse()
	if err := loadNativeLibrary(os.Getenv("SKINTOKENS_LIBRARY")); err != nil {
		panic(err)
	}
	if err := grpc.StartServer(*addr, &SkinTokens{}); err != nil {
		panic(err)
	}
}
