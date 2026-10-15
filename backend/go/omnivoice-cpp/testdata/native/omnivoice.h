#pragma once
// omnivoice.h: public ABI for omnivoice.cpp.
//
// Single-header public API. Pure C99, consumable from C and C++ alike.
// Bindings (Python ctypes, Rust bindgen, Go cgo) parse this file directly.
// Style follows whisper.h / llama.h: extern "C" linkage on every entry,
// POD structs only, const char * UTF-8 strings, ov_status enum returns.
//
// The opaque ov_context handle aggregates every module the synthesis path
// needs (LM weights, audio tokenizer codec, BPE tokenizer, voice-design
// vocabulary, GGML backend pair). One init, one free, one synthesize call
// covers the full TTS path. The lower-level pipeline_*_load /
// pipeline_*_free entries declared in pipeline-tts.h / pipeline-codec.h
// stay available for the debug paths that need partial init, but they
// are intentionally not part of this public ABI.

#include <stdbool.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// Symbol visibility. Three Windows cases: building the SHARED target
// (OMNIVOICE_BUILD set, dllexport), consuming the SHARED target from
// outside (nothing set, dllimport), consuming the STATIC archive
// (OMNIVOICE_STATIC set by the static target's INTERFACE definitions,
// empty so the linker resolves the symbol directly without dllimport).
// On GCC/Clang the default-visibility attribute is harmless on static
// builds and required on shared builds.
#if defined(_WIN32) || defined(__CYGWIN__)
#    if defined(OMNIVOICE_STATIC)
#        define OV_API
#    elif defined(OMNIVOICE_BUILD)
#        define OV_API __declspec(dllexport)
#    else
#        define OV_API __declspec(dllimport)
#    endif
#elif defined(__GNUC__) || defined(__clang__)
#    define OV_API __attribute__((visibility("default")))
#else
#    define OV_API
#endif

// Struct ABI version. Incremented every time a public POD struct grows a
// new field at the end. Callers fill `.abi_version = OV_ABI_VERSION` (or
// let ov_*_default_params set it). Entries that consume those structs
// reject inputs whose abi_version exceeds the build-time constant: this
// guards a binary built against vN from receiving a struct laid out for
// vN+1 by a freshly compiled binding. Adding fields stays backward-compat
// because the new tail is zero-init in older callers and the lib reads
// only what its abi_version permits.
//
// There is no separate semver triple. The runtime build identity is the
// git short hash + commit date string returned by ov_version(); for
// binding compat checks, OV_ABI_VERSION is the only number that matters.
#define OV_ABI_VERSION 3

// Returns a static string of the form "<git-hash> (<date>)" identifying
// the exact commit this binary was built from. Safe to call from any
// thread, no allocation. Pointer stays valid for the process lifetime.
OV_API const char * ov_version(void);

// Status code returned by every fallible entry. OV_STATUS_OK is always
// zero so `if (rc)` reads as `if (rc != OV_STATUS_OK)`.
enum ov_status {
    OV_STATUS_OK               = 0,
    OV_STATUS_INVALID_PARAMS   = -1,
    OV_STATUS_INSTRUCT_INVALID = -2,
    OV_STATUS_GENERATE_FAILED  = -3,
    OV_STATUS_OOM              = -4,
    OV_STATUS_CANCELLED        = -5,
};

// Returns the last error message produced on the calling thread by any
// ov_* entry, as a NUL-terminated UTF-8 string. errno-style semantics: the
// pointer is only meaningful right after a failure (ov_init returning NULL,
// or any ov_* entry returning a negative ov_status); calling it after a
// successful entry yields the previous message or an empty string. Storage
// is thread-local so two threads running ov_synthesize concurrently never
// race on each other's diagnostics. The pointer stays valid until the next
// failing ov_* entry on the same thread.
OV_API const char * ov_last_error(void);

// Output audio buffer. Plain POD: the samples pointer is malloc-allocated
// by ov_synthesize, owned by the struct, released by ov_audio_free. Do not
// free samples directly nor reassign without freeing first. Zero-initialise
// before the first use: `struct ov_audio a = {0};`.
struct ov_audio {
    float * samples;      // mono PCM, malloc-allocated
    int     n_samples;    // length in samples
    int     sample_rate;  // 24000 for OmniVoice
    int     channels;     // 1 (mono)
};

// Release the samples buffer and reset the struct to empty. Safe on a
// zero-initialised struct (no double free, no NULL deref).
OV_API void ov_audio_free(struct ov_audio * a);

// Opaque handle. Definition lives in omnivoice.cpp. Use ov_init / ov_free.
struct ov_context;

// Initialisation parameters. model_path is required (the LM GGUF). When
// codec_path is NULL the codec module is skipped and ov_synthesize fails
// immediately with OV_STATUS_INVALID_PARAMS. use_fa enables flash
// attention when a GPU backend is present; clamp_fp16 guards FP16
// matmul accumulation on sub-Ampere CUDA. abi_version stays first so a
// future struct growth keeps reading the version field at offset 0.
struct ov_init_params {
    int          abi_version;
    const char * model_path;
    const char * codec_path;
    bool         use_fa;
    bool         clamp_fp16;
};

// Initialise to the standard defaults: codec_path NULL (caller must set
// it for synthesis), use_fa true, clamp_fp16 false.
OV_API void ov_init_default_params(struct ov_init_params * p);

// Allocate every module described by params. Returns NULL on any failure
// after releasing whatever it has allocated so far. The returned handle
// owns its GGML backend pair and must be released with ov_free.
OV_API struct ov_context * ov_init(const struct ov_init_params * params);

// Release every module owned by the handle and free the handle itself.
// Safe on NULL.
OV_API void ov_free(struct ov_context * ov);

// Cooperative cancellation callback. Returns true to request the
// synthesis to abort. Polled between chunks of long-form output, so the
// cancel granularity is roughly chunk_duration_sec.
typedef bool (*ov_cancel_cb)(void * user_data);

// Streaming output callback. When set on ov_tts_params, the synth pipeline
// runs in streaming mode: audio is post processed and emitted chunk by
// chunk through this callback rather than accumulated into the `out` buffer
// of ov_synthesize. Returning false aborts the synthesis with
// OV_STATUS_CANCELLED, identical to the ov_cancel_cb behaviour. The samples
// pointer is mono float PCM at the codec sample rate; valid only for the
// duration of the call. user_data is forwarded verbatim from on_chunk_user_data.
//
// Bit perfect against the buffered path for voice cloning. For voice design
// (no reference) the streaming pipeline skips peak / 0.5 normalisation since
// the global peak is unknowable before the last sample: output level then
// runs roughly 6 to 12 dB below the buffered path. Logged at INFO when this
// branch fires.
typedef bool (*ov_audio_chunk_cb)(const float * samples, int n_samples, void * user_data);

// Log severity. Numerically ordered so a callback can filter with a
// simple `if (level < threshold) return;`. ERROR is reserved for failure
// reports that the lib also surfaces via ov_status / ov_last_error;
// WARN for recoverable surprises; INFO for the normal load and
// synthesis cadence; DEBUG for tensor-level cossim diagnostics.
enum ov_log_level {
    OV_LOG_DEBUG = 0,
    OV_LOG_INFO  = 1,
    OV_LOG_WARN  = 2,
    OV_LOG_ERROR = 3,
};

// Logging callback. msg is a NUL-terminated UTF-8 string already formatted
// by the lib, with no trailing newline (the callback is free to add one).
// user_data is forwarded verbatim from ov_log_set. Called from any thread
// the lib runs on: the callback must be reentrant.
typedef void (*ov_log_cb)(enum ov_log_level level, const char * msg, void * user_data);

// Install a global log callback. Passing cb == NULL restores the default
// behaviour (write to stderr). Safe to call at any point; takes effect
// immediately on subsequent log emissions across every thread. Storage
// is process-wide, not per-handle, matching whisper_log_set / llama_log_set.
OV_API void ov_log_set(ov_log_cb cb, void * user_data);

// Synthesis parameters. Strings are NULL-terminated UTF-8; NULL maps to
// empty. Reference inputs are mutually exclusive: either pre-encoded
// tokens [K, ref_T] OR raw 24 kHz mono samples. Passing both fails with
// OV_STATUS_INVALID_PARAMS. The MaskGIT sampler config is flattened
// directly into this struct (seven mg_* fields) to keep it fully POD.
// abi_version stays first so the lib can route on it before reading any
// field that may have shifted in a future minor.
struct ov_tts_params {
    int abi_version;

    // Input text and language hint. lang accepts "" or "none" for auto, an
    // ISO id ("fr") or a language name ("french"), resolved against the table
    // the model was trained with; ov_n_languages enumerates it, and anything
    // else is OV_STATUS_INVALID_PARAMS. instruct is the raw attribute string
    // ("female young adult moderate"), validated and normalised internally
    // against the bundled VoiceDesign.
    const char * text;
    const char * lang;
    const char * instruct;

    // Duration control. T_override > 0 forces single-shot at the exact
    // frame count and bypasses both the estimator and the chunker.
    // T_override == 0 lets the pipeline decide between single-shot and
    // chunked output. chunk_duration_sec <= 0 disables chunking entirely.
    int   T_override;
    float chunk_duration_sec;
    float chunk_threshold_sec;

    // Generation flags. denoise toggles the <|denoise|> marker emitted
    // only when a reference is present. preprocess_prompt mirrors the
    // upstream Python flag: when true, applies add_punctuation to
    // ref_text and silence-trims the raw reference waveform.
    bool denoise;
    bool preprocess_prompt;

    // MaskGIT sampler config. Defaults match the reference :
    // num_step=32, guidance_scale=2.0, t_shift=0.1,
    // layer_penalty_factor=5.0, position_temperature=5.0,
    // class_temperature=0.0, seed=42.
    int      mg_num_step;
    float    mg_guidance_scale;
    float    mg_t_shift;
    float    mg_layer_penalty_factor;
    float    mg_position_temperature;
    float    mg_class_temperature;
    uint64_t mg_seed;

    // Optional voice reference. Two mutually exclusive ways to supply it.
    const int32_t * ref_audio_tokens;
    int             ref_T;
    const float *   ref_audio_24k;
    int             ref_n_samples;
    const char *    ref_text;

    // Intermediate tensor dump directory. NULL disables dumps.
    const char * dump_dir;

    // Cooperative cancellation. cancel NULL disables the feature.
    // cancel_user_data is forwarded to the callback verbatim.
    ov_cancel_cb cancel;
    void *       cancel_user_data;

    // Streaming output. When on_chunk is non NULL, ov_synthesize runs the
    // streaming pipeline: audio chunks emit through on_chunk and `out`
    // stays empty on success. on_chunk NULL keeps the buffered path.
    ov_audio_chunk_cb on_chunk;
    void *            on_chunk_user_data;

    // Output post filtering toggle for the buffered path. true (default)
    // keeps the reference behaviour: silence removal, per utterance peak
    // normalisation when no reference is set, and edge fade plus padding.
    // false returns the raw decode at exactly T_override * hop samples, so a
    // caller assembling its own timeline onto fixed slots (dubbing) gets a
    // predictable segment length and owns its own edge shaping. Reference
    // loudness matching (ref_rms scaling) is part of voice cloning and runs
    // either way. Tail field: kept last for ABI growth, read only when
    // abi_version >= 3. The streaming path always post filters.
    bool postproc;
};

// Initialise to the standard defaults. Strings NULL, T_override 0,
// chunk_duration_sec 15, chunk_threshold_sec 30, denoise true,
// preprocess_prompt true, MaskGIT defaults as above, every reference
// pointer NULL, dump_dir NULL, cancel NULL, postproc true.
OV_API void ov_tts_default_params(struct ov_tts_params * p);

// Run the full TTS synthesis. Resolves the instruct against the bundled
// VoiceDesign vocabulary, picks between single-shot, chunked auto-voice
// and voice-cloning paths from the params struct, and fills `out` with
// mono float PCM at 24 kHz. Returns OV_STATUS_OK on success; on any
// failure returns a negative ov_status describing the cause and leaves
// `out` empty. Requires a codec-loaded handle.
OV_API enum ov_status ov_synthesize(struct ov_context * ov, const struct ov_tts_params * params, struct ov_audio * out);

// Convert a duration in seconds to a frame count using the bundled codec
// frame rate (sample_rate / hop_length). Clamps to a minimum of one
// frame. Requires a codec-loaded handle.
OV_API int ov_duration_sec_to_tokens(const struct ov_context * ov, float duration_sec);

// Number of RVQ codebooks (K) of the loaded model. Pre-encoded reference
// tokens passed via ref_audio_tokens are laid out [K, ref_T] row-major;
// callers reading a packed .rvq stream need K to derive ref_T from the
// code count. Returns 0 on a NULL handle.
OV_API int ov_num_codebooks(const struct ov_context * ov);

// Languages the synthesis accepts as a hint, in the order of the bundled
// table. An empty hint leaves the model to infer one from the text, so "auto"
// is not an entry here. The id is what the prompt carries ("fr"), the name is
// what a user reads ("french"); both pointers are static and outlive any
// context.
OV_API int          ov_n_languages(void);
OV_API const char * ov_language_id(int i);
OV_API const char * ov_language_name(int i);

// Pre-encoded voice reference codes, the in-process equivalent of an
// omnivoice-codec CLI encode. Plain POD: ref_codes is malloc allocated
// by ov_extract_voice_ref, owned by the struct, released by
// ov_voice_ref_free. Do not free the pointer directly nor reassign
// without freeing first. Zero initialise before first use:
// `struct ov_voice_ref ref = {0};`.
//
// ref_codes is the RVQ code matrix equivalent to a raw .rvq file, laid
// out [num_codebooks, ref_T] row-major (T fastest), ready to feed back
// through ov_tts_params.ref_audio_tokens / ref_T.
struct ov_voice_ref {
    int32_t * ref_codes;
    int       ref_T;
    int       num_codebooks;
};

// Extract reusable voice-clone codes from a decoded reference audio
// buffer: mono float32 PCM at 24 kHz. Requires a codec-loaded handle.
// Applies the same preprocessing as the omnivoice-codec CLI and the
// --ref-wav synth path (RMS auto-gain, silence trim, hop truncation),
// so the codes are bit-identical to both and round-trip directly through
// ov_tts_params.ref_audio_tokens.
//
// On success fills out with a malloc-owned buffer. On failure leaves out
// empty and stores a diagnostic in ov_last_error().
OV_API enum ov_status ov_extract_voice_ref(struct ov_context *   ov,
                                           const float *         ref_audio_24k,
                                           int                   ref_n_samples,
                                           struct ov_voice_ref * out);

// Release the RVQ code buffer and reset the struct to empty. Safe on a
// zero initialised struct.
OV_API void ov_voice_ref_free(struct ov_voice_ref * ref);

#ifdef __cplusplus
}
#endif
