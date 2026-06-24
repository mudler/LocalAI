#pragma once

// Thin C shim over moss-tts.cpp's flat C-API (include/moss_tts_capi.h) for the
// MOSS-TTS-Local (v1.5) pipeline. It holds the loaded pipeline as a single
// global handle so the purego bindings on the Go side stay handle-free (mirrors
// the qwen3-tts-cpp qt3_* shim). Only the Local variant is wired here; the
// Delay / Realtime / Nano variants of the upstream API are intentionally
// unused.

extern "C" {

// Load the Local (MossTTSLocal v1.5) pipeline from its three GGUFs: the local
// transformer, the MOSS-Audio-Tokenizer codec, and the text tokenizer. Sets up
// ggml logging + backend registration first. Returns 0 on success, non-zero on
// failure.
int mtl_load(const char *local_path, const char *codec_path,
             const char *tokenizer_path);

// Synthesize `text` to a malloc'd interleaved f32 PCM buffer (caller frees via
// mtl_pcm_free). v1.5 output is 48 kHz stereo: *out_n is the total number of
// float samples across both channels (frames * 2) and *out_sr is the sample
// rate. `reference_wav` is an optional path to a clone-reference WAV (may be
// NULL/empty for no cloning); the engine decodes it itself. `seed` < 0 means
// random. Returns NULL on failure (out_n / out_sr set to 0).
float *mtl_tts(const char *text, const char *reference_wav, int seed,
               int *out_n, int *out_sr);

// Free a buffer returned by mtl_tts.
void mtl_pcm_free(float *p);

// Release the loaded pipeline.
void mtl_unload(void);

// Upstream engine version string (moss_tts_version()).
const char *mtl_version(void);
}
