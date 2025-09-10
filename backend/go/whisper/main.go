package main

// Note: this is started internally by LocalAI and a server is allocated for each model
import (
	"flag"

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
	gosd, err := purego.Dlopen("./libgowhisper.so", purego.RTLD_NOW|purego.RTLD_GLOBAL)
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
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, gosd, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &Whisper{}); err != nil {
		panic(err)
	}
}
