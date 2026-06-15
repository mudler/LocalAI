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
	libName := os.Getenv("LOCALVQE_LIBRARY")
	if libName == "" {
		libName = "./liblocalvqe.so"
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CppOptionsNew, "localvqe_options_new"},
		{&CppOptionsFree, "localvqe_options_free"},
		{&CppOptionsSetModelPath, "localvqe_options_set_model_path"},
		{&CppOptionsSetBackend, "localvqe_options_set_backend"},
		{&CppOptionsSetDevice, "localvqe_options_set_device"},
		{&CppNewWithOptions, "localvqe_new_with_options"},
		{&CppFree, "localvqe_free"},
		{&CppProcessF32, "localvqe_process_f32"},
		{&CppProcessS16, "localvqe_process_s16"},
		{&CppProcessFrameF32, "localvqe_process_frame_f32"},
		{&CppProcessFrameS16, "localvqe_process_frame_s16"},
		{&CppReset, "localvqe_reset"},
		{&CppLastError, "localvqe_last_error"},
		{&CppSampleRate, "localvqe_sample_rate"},
		{&CppHopLength, "localvqe_hop_length"},
		{&CppFFTSize, "localvqe_fft_size"},
		{&CppSetNoiseGate, "localvqe_set_noise_gate"},
		{&CppGetNoiseGate, "localvqe_get_noise_gate"},
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &LocalVQE{}); err != nil {
		panic(err)
	}
}
