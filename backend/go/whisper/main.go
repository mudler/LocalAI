package main

// Note: this is started internally by LocalAI and a server is allocated for each model
import (
	"flag"
	"os"
	"runtime"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

const (
	defaultAddr = "localhost:50051"
)

var (
	addr = flag.String("addr", defaultAddr, "the address to listen on")
)

// resolveAddr picks the address the gRPC server binds to. An explicitly set
// -addr always wins. Launchers may hand us the listen address as a bare
// positional argument, which Go's flag package silently drops — honour it
// next so the server binds the port its caller actually allocated instead of
// the default one (#11623). An explicitly empty -addr counts as unset:
// binding the empty address would listen on an OS-chosen port on every
// interface instead of the one the caller allocated.
func resolveAddr(flagAddr string, addrSet bool, args []string) string {
	if addrSet && flagAddr != "" {
		return flagAddr
	}
	if len(args) > 0 {
		return args[0]
	}
	return defaultAddr
}

type LibFuncs struct {
	FuncPtr any
	Name    string
}

func main() {
	// Get library name from environment variable, default to fallback
	libName := os.Getenv("WHISPER_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "./libgowhisper-fallback.dylib"
		} else {
			libName = "./libgowhisper-fallback.so"
		}
	}

	gosd, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CppLoadModel, "load_model"},
		{&CppLoadModelVAD, "load_model_vad"},
		{&CppVAD, "vad"},
		{&CppTranscribe, "transcribe"},
		{&CppGetSegmentText, "get_segment_text"},
		{&CppGetSegmentStart, "get_segment_t0"},
		{&CppGetSegmentEnd, "get_segment_t1"},
		{&CppNTokens, "n_tokens"},
		{&CppGetTokenID, "get_token_id"},
		{&CppGetSegmentSpeakerTurnNext, "get_segment_speaker_turn_next"},
		{&CppSetAbort, "set_abort"},
		{&CppSetNewSegmentCallback, "set_new_segment_callback"},
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, gosd, lf.Name)
	}

	// Build a stable C-callable function pointer from the Go callback. The
	// pointer lives for the lifetime of the process; per-call dispatch is
	// keyed by user_data through streamCallStates.
	goNewSegmentCb = purego.NewCallback(onNewSegment)

	flag.Parse()

	// flag.Visit reports only flags that were explicitly set, so an -addr
	// equal to the default is still distinguished from an untouched one.
	addrSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "addr" {
			addrSet = true
		}
	})

	if err := grpc.StartServer(resolveAddr(*addr, addrSet, flag.Args()), &Whisper{}); err != nil {
		panic(err)
	}
}
