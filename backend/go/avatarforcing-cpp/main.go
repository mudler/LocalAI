package main

// Started internally by LocalAI: one gRPC server per loaded model.
//
// Loads libavatarforcing (the C API of avatarforcing.cpp) with purego and
// registers the functions this backend calls. AVATARFORCING_LIBRARY overrides
// the library path; the default is libavatarforcing.so, found through
// LD_LIBRARY_PATH, which run.sh points at the packaged lib/ directory.
import (
	"flag"
	"fmt"
	"os"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	libName := os.Getenv("AVATARFORCING_LIBRARY")
	if libName == "" {
		libName = "libavatarforcing.so"
	}
	if err := loadLibrary(libName); err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stderr, "[avatarforcing-cpp] library ABI=%d\n", CppAbiVersion())

	flag.Parse()

	if err := grpc.StartServer(*addr, &AvatarForcing{}); err != nil {
		panic(err)
	}
}

// loadLibrary opens the library and binds the C API functions.
func loadLibrary(libName string) error {
	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("avatarforcing-cpp: dlopen %q: %w", libName, err)
	}
	funcs := []struct {
		ptr  any
		name string
	}{
		{&CppAbiVersion, "avatarforcing_abi_version"},
		{&CppLoad, "avatarforcing_load"},
		{&CppFree, "avatarforcing_free"},
		{&CppLastError, "avatarforcing_last_error"},
		{&CppFreeString, "avatarforcing_free_string"},
		{&CppInfoJSON, "avatarforcing_info_json"},
		{&CppGenerateMp4, "avatarforcing_generate_mp4"},
	}
	for _, f := range funcs {
		purego.RegisterLibFunc(f.ptr, lib, f.name)
	}
	return nil
}
