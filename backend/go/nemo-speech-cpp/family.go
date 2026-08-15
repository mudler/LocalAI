package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gguf "github.com/gpustack/gguf-parser-go"
)

// auxOnlyArchitectures are converted NeMo components that attach to a primary
// model but are never loadable on their own. Pointing a model config at one is
// a configuration mistake worth naming explicitly.
var auxOnlyArchitectures = map[string]string{
	"nemo-nano-codec": "a TTS codec, set it with the codec_model option on a magpietts model",
	"vad":             "a VAD model, set it with the vad_model option on an asr model",
	"pnc":             "a punctuation model, set it with the pnc_model option on an asr model",
}

// familyFor maps a GGUF general.architecture value to a model family.
//
// Unknown architectures resolve to NMT rather than an error: NMT GGUFs come
// from llama.cpp's converter and carry an ordinary LLM architecture, so there
// is no NeMo-specific string to match. The user selected this backend
// explicitly, which is the signal that the model is meant for it.
func familyFor(arch string) (family, error) {
	if reason, ok := auxOnlyArchitectures[arch]; ok {
		return familyUnknown, fmt.Errorf(
			"nemo-speech-cpp: %q is %s, not a model that can be loaded directly", arch, reason)
	}
	switch arch {
	case "asr":
		return familyASR, nil
	case "sortformer":
		return familyDiarization, nil
	case "magpietts":
		return familyTTS, nil
	}
	return familyNMT, nil
}

// ggufArchitecture reads general.architecture from a GGUF file.
func ggufArchitecture(path string) (string, error) {
	f, err := gguf.ParseGGUFFile(path, gguf.UseMMap(), gguf.SkipLargeMetadata())
	if err != nil {
		return "", fmt.Errorf("nemo-speech-cpp: parse gguf %q: %w", path, err)
	}
	kv, found := f.Header.MetadataKV.Index([]string{"general.architecture"})
	if found == 0 {
		return "", fmt.Errorf("nemo-speech-cpp: %q has no general.architecture key", path)
	}
	arch := kv["general.architecture"]
	// ValueString panics on a mistyped key, and a hand-written or half-converted
	// GGUF is exactly where that happens. This function is the load-time guard;
	// it reports, it does not take the process down.
	if arch.ValueType != gguf.GGUFMetadataValueTypeString {
		return "", fmt.Errorf(
			"nemo-speech-cpp: %q has a non-string general.architecture (type %v)", path, arch.ValueType)
	}
	return arch.ValueString(), nil
}

// discoverTTSAssets fills in codecModel and tokenizerDir when they were not set
// explicitly, by scanning the primary GGUF's own directory.
//
// A missing asset is a hard error rather than a warning: the runtime would
// otherwise load and emit garbage audio, which surfaces far from the cause.
func discoverTTSAssets(primaryGGUF string, o *loadOptions) error {
	dir := filepath.Dir(primaryGGUF)

	if o.codecModel == "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("nemo-speech-cpp: scan %q for a codec model: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			candidate := filepath.Join(dir, name)
			// Skip the primary model itself: a file called nanocodec-magpie.gguf
			// would otherwise be selected as its own codec. Compare basenames,
			// because candidate is Cleaned by filepath.Join while primaryGGUF
			// arrives as the caller wrote it, so "/models//magpie.gguf" would
			// slip past a whole-path equality.
			if name == filepath.Base(primaryGGUF) {
				continue
			}
			if strings.Contains(strings.ToLower(name), "nanocodec") ||
				strings.Contains(strings.ToLower(name), "nano-codec") {
				o.codecModel = candidate
				break
			}
		}
	}
	if o.codecModel == "" {
		return fmt.Errorf(
			"nemo-speech-cpp: no NanoCodec GGUF found next to %q, set the codec_model option",
			primaryGGUF)
	}

	if o.tokenizerDir == "" {
		candidate := filepath.Join(dir, "extracted")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			o.tokenizerDir = candidate
		}
	}
	if o.tokenizerDir == "" {
		return fmt.Errorf(
			"nemo-speech-cpp: no tokenizer directory found next to %q, set the tokenizer_dir option",
			primaryGGUF)
	}
	return nil
}
