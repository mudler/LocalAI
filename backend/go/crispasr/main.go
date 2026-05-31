package main

// Note: this is started internally by LocalAI and a server is allocated for each model
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
	libName := os.Getenv("CRISPASR_LIBRARY")
	if libName == "" {
		libName = "./libgocrispasr-fallback.so"
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CppLoadModel, "load_model"},
		{&CppSetCodecPath, "set_codec_path"},
		{&CppLoadModelVAD, "load_model_vad"},
		{&CppVAD, "vad"},
		{&CppTranscribe, "transcribe"},
		{&CppGetSegmentText, "get_segment_text"},
		{&CppGetSegmentStart, "get_segment_t0"},
		{&CppGetSegmentEnd, "get_segment_t1"},
		{&CppGetBackend, "get_backend"},
		{&CppSetAbort, "set_abort"},
		{&CppTTSSynthesize, "tts_synthesize"},
		{&CppTTSFree, "tts_free"},
		{&CppTTSSetVoice, "tts_set_voice"},
		{&CppTTSSetVoiceFile, "tts_set_voice_file"},
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &CrispASR{}); err != nil {
		panic(err)
	}
}
