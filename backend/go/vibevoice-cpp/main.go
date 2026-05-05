package main

// Started internally by LocalAI - one gRPC server per loaded model.
import (
	"flag"
	"os"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

type LibFuncs struct {
	FuncPtr any
	Name    string
}

func main() {
	libName := os.Getenv("VIBEVOICECPP_LIBRARY")
	if libName == "" {
		libName = "./libgovibevoicecpp-fallback.so"
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CppLoad, "vv_capi_load"},
		{&CppTTS, "vv_capi_tts"},
		{&CppASR, "vv_capi_asr"},
		{&CppUnload, "vv_capi_unload"},
		{&CppVersion, "vv_capi_version"},
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &VibevoiceCpp{}); err != nil {
		panic(err)
	}
}
