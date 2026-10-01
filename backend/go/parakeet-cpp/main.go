package main

// Started internally by LocalAI - one gRPC server per loaded model.
//
// Loads the parakeet shared library via purego and registers the flat
// C-API entry points declared in parakeet_capi.h. The library name can be
// overridden with PARAKEET_LIBRARY (mirrors the WHISPER_LIBRARY /
// VIBEVOICECPP_LIBRARY convention in the sibling backends); the default
// looks next to this binary for libparakeet.so on Linux and
// libparakeet.dylib on macOS.
import (
	"flag"
	"fmt"
	"os"
	"runtime"

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
	libName := os.Getenv("PARAKEET_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "libparakeet.dylib"
		} else {
			libName = "libparakeet.so"
		}
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(fmt.Errorf("parakeet-cpp: dlopen %q: %w", libName, err))
	}

	// Bound 1:1 to parakeet_capi.h. The C-API returns malloc'd char*
	// buffers from transcribe_*; we register those as uintptr so we get
	// the raw pointer back and can call parakeet_capi_free_string on it
	// (purego's string return would copy and forget the original pointer,
	// leaking it on every call).
	libFuncs := []LibFuncs{
		{&CppAbiVersion, "parakeet_capi_abi_version"},
		{&CppLoad, "parakeet_capi_load"},
		{&CppFree, "parakeet_capi_free"},
		{&CppTranscribePath, "parakeet_capi_transcribe_path"},
		{&CppTranscribePathJSON, "parakeet_capi_transcribe_path_json"},
		{&CppStreamBegin, "parakeet_capi_stream_begin"},
		{&CppStreamFeed, "parakeet_capi_stream_feed"},
		{&CppStreamFinalize, "parakeet_capi_stream_finalize"},
		{&CppStreamFree, "parakeet_capi_stream_free"},
		{&CppFreeString, "parakeet_capi_free_string"},
		{&CppLastError, "parakeet_capi_last_error"},
	}
	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	// The batched-JSON entry point exists only in newer libparakeet.so (ABI >= 2).
	// Probe with Dlsym and register only if present, so the backend still loads
	// against an older library (it falls back to per-request transcription).
	if sym, err := purego.Dlsym(lib, "parakeet_capi_transcribe_pcm_batch_json"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppTranscribePcmBatchJSON, lib, "parakeet_capi_transcribe_pcm_batch_json")
	}

	// Per-request language variants (multilingual nemotron). Same probe pattern:
	// present only in libparakeet.so built with multilingual support, so the
	// backend still loads against an older library and falls back to the
	// non-lang batched + streaming entry points (model default / "auto").
	if sym, err := purego.Dlsym(lib, "parakeet_capi_transcribe_pcm_batch_json_lang"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppTranscribePcmBatchJSONLang, lib, "parakeet_capi_transcribe_pcm_batch_json_lang")
	}
	if sym, err := purego.Dlsym(lib, "parakeet_capi_stream_begin_lang"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppStreamBeginLang, lib, "parakeet_capi_stream_begin_lang")
	}

	// Streaming JSON entry points (ABI v4): surface per-word timestamps on the
	// streaming path. Same probe pattern; absent in older libparakeet.so, where
	// the backend falls back to the text-only streaming feed.
	if sym, err := purego.Dlsym(lib, "parakeet_capi_stream_feed_json"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppStreamFeedJSON, lib, "parakeet_capi_stream_feed_json")
		purego.RegisterLibFunc(&CppStreamFinalizeJSON, lib, "parakeet_capi_stream_finalize_json")
	}

	// Model roles + diarization/sound (ABI v7-v8): parakeet_capi_model_kind is
	// what lets Load tell an ASR/diarization/sound context apart, so it gates
	// every other new symbol below (an older libparakeet.so gets none of
	// them, and companion model options are rejected in roles.go). Diarization
	// itself (diarize_pcm, transcribe_and_diarize_json) predates model_kind
	// (ABI v7), so it is probed on its own.
	if sym, err := purego.Dlsym(lib, "parakeet_capi_diarize_pcm"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppDiarizePCM, lib, "parakeet_capi_diarize_pcm")
	}
	if sym, err := purego.Dlsym(lib, "parakeet_capi_transcribe_and_diarize_json"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppTranscribeAndDiarizeJSON, lib, "parakeet_capi_transcribe_and_diarize_json")
	}
	if sym, err := purego.Dlsym(lib, "parakeet_capi_model_kind"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppModelKind, lib, "parakeet_capi_model_kind")
		purego.RegisterLibFunc(&CppNumClasses, lib, "parakeet_capi_num_classes")
		purego.RegisterLibFunc(&CppSoundOptsDefault, lib, "parakeet_capi_sound_opts_default")
		purego.RegisterLibFunc(&CppSoundStreamBegin, lib, "parakeet_capi_sound_stream_begin")
		purego.RegisterLibFunc(&CppSoundStreamFeed, lib, "parakeet_capi_sound_stream_feed")
		purego.RegisterLibFunc(&CppSoundStreamDrainScoresJSON, lib, "parakeet_capi_sound_stream_drain_scores_json")
		purego.RegisterLibFunc(&CppFreeSoundSegments, lib, "parakeet_capi_free_sound_segments")
		purego.RegisterLibFunc(&CppSoundStreamFree, lib, "parakeet_capi_sound_stream_free")
		purego.RegisterLibFunc(&CppSceneOptsDefault, lib, "parakeet_capi_scene_opts_default")
		purego.RegisterLibFunc(&CppSceneStreamBegin, lib, "parakeet_capi_scene_stream_begin")
		purego.RegisterLibFunc(&CppSceneStreamFeedJSON, lib, "parakeet_capi_scene_stream_feed_json")
		purego.RegisterLibFunc(&CppSceneStreamLastError, lib, "parakeet_capi_scene_stream_last_error")
		purego.RegisterLibFunc(&CppSceneStreamFree, lib, "parakeet_capi_scene_stream_free")
	}
	// Speaker identification (ABI v9 and v10). Probed separately from model_kind so an older
	// libparakeet.so still loads; speaker_model: is refused in roles.go unless the v10 symbols exist.
	if sym, err := purego.Dlsym(lib, "parakeet_capi_scene_stream_begin_speaker"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppSpeakerDim, lib, "parakeet_capi_speaker_dim")
		purego.RegisterLibFunc(&CppSpeakerRegistryNew, lib, "parakeet_capi_speaker_registry_new")
		purego.RegisterLibFunc(&CppSpeakerRegistryFree, lib, "parakeet_capi_speaker_registry_free")
		purego.RegisterLibFunc(&CppSpeakerRegistryLastError, lib, "parakeet_capi_speaker_registry_last_error")
		purego.RegisterLibFunc(&CppSceneStreamBeginSpeaker, lib, "parakeet_capi_scene_stream_begin_speaker")
		purego.RegisterLibFunc(&CppTranscribeAndDiarizeNamedJSON, lib, "parakeet_capi_transcribe_and_diarize_named_json")
	}
	if sym, err := purego.Dlsym(lib, "parakeet_capi_diarize_named_pcm_json"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppSpeakerRegistryAddEmbedding, lib, "parakeet_capi_speaker_registry_add_embedding")
		purego.RegisterLibFunc(&CppDiarizeNamedPCMJSON, lib, "parakeet_capi_diarize_named_pcm_json")
	}

	fmt.Fprintf(os.Stderr, "[parakeet-cpp] ABI=%d\n", CppAbiVersion())

	flag.Parse()

	if err := grpc.StartServer(*addr, &ParakeetCpp{}); err != nil {
		panic(err)
	}
}
