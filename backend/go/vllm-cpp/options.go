package main

// Load-time engine configuration, from two config surfaces:
//
//   - `engine_args:` (ModelOptions.EngineArgs, a JSON object) is the canonical
//     one. Keys are spelled exactly as vLLM's own CLI flags, so a config written
//     against vLLM works verbatim here - `speculative_config` and
//     `kv_transfer_config` in particular take the same JSON documents vLLM's
//     --speculative-config / --kv-transfer-config accept, and are handed to the
//     engine unparsed.
//   - `options:` (the free-form "key:value" list) is the older surface this
//     backend shipped with. It is still honoured so existing configs keep
//     working; engine_args wins on any key set in both.
//
// Anything unrecognised is ignored rather than fatal: the engine validates the
// documents it is given and reports a precise error at load, and a config that
// also carries knobs for a different backend must not fail the load here.

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

type loadOptions struct {
	blockSize  int32 // KV block size (tokens/block); engine default 32.
	numBlocks  int32 // KV blocks to allocate; engine default 256.
	maxNumSeqs int32 // max concurrent sequences; engine default 8.
	// Max sequence length. Also settable through the model config's
	// context_size / max_model_len; see Load for the precedence.
	maxModelLen int32
	// Per-step chunked-prefill token budget (ABI v9). 0 = the engine's
	// bounded per-arch default.
	maxNumBatchedTokens int32
	// Automatic prefix caching tri-state (ABI v7): 0 = the model-capability
	// default, 1 = force on, 2 = force off.
	enablePrefixCaching int32
	// Jump-forward decoding tri-state (ABI v10), SGLang's grammar-speed subset:
	// 0 = defer to the environment (VT_ENABLE_JUMP_FORWARD, default off),
	// 1 = force on, 2 = force off.
	enableJumpForward int32
	// Scheduler admission policy (ABI v9): "" = fcfs, else fcfs|priority|lpm.
	schedulingPolicy string
	// Engine-side parser selection (ABI v4/v5). Empty = the engine
	// auto-detects from the chat template; "none" disables the reasoning
	// split; unknown names fail the first chat call.
	toolParser      string
	reasoningParser string
	// Speculative decoding (ABI v6), as vLLM's --speculative-config JSON:
	// {"method":"mtp"|"dflash"|"ngram", ...}. Empty = no speculation.
	speculativeConfig string
	// External KV connector / LMCache (ABI v9), as vLLM's --kv-transfer-config
	// JSON. Empty = no connector.
	kvTransferConfig string
	// Override for the tokenizer_config.json the chat template is read from
	// (ABI v9). Empty = <model_dir>/tokenizer_config.json.
	tokenizerConfigPath string
	// MiniMax-H3 video+audio generation (ABI v12). Present only when the config
	// carries at least one of its keys; see videoOptions.engaged.
	video videoOptions
}

// videoOptions is the MiniMax-H3 checkpoint SET plus its generation defaults.
//
// H3 is not one model directory: the DiT, the text encoder and the two VAEs are
// separate artifacts, which is why vllm.cpp gives video its own engine handle
// (vllm_video_engine, ABI v12) rather than another vllm_engine. The DiT is the
// model config's `parameters.model`; everything else arrives through these
// options, so one gallery entry can name five files.
//
// The geometry/frame defaults exist because H3's trained canvas is nothing like
// the generic /video defaults: 1344x768 at 124 frames is a ~5.2 s clip, and the
// frame count must sit on the 17n+5 grid. A request that leaves a field unset
// gets the model's own default from here instead of a canvas the checkpoint was
// never trained at.
type videoOptions struct {
	encoderPath      string // H3-Encoder GGUF or bf16 shard dir
	tokenizerPath    string // tokenizer.json, needed with an encoder
	videoVaePath     string
	videoVaeConfig   string
	audioVaePath     string
	audioVaeConfig   string
	promptEmbedsPath string // fallback conditioning when there is no encoder
	// The served checkpoint PARTITION. Community GGUF/NVFP4 files strip the
	// release metadata and the FL2VA/Ref2VA DiTs are byte-structurally
	// identical, so the engine refuses every generate until it is DECLARED.
	// "fl2va" serves t2va + fl2va; "ref2va" serves reference conditioning.
	partition   string
	device      int32 // 0 cpu, 1 cuda (the ABI's own encoding, no auto slot)
	deviceSet   bool
	dequantBf16 int32
	fp4Resident int32
	// Per-model generation defaults, applied when the request leaves the field
	// at 0.
	width     int32
	height    int32
	numFrames int32
	steps     int32
	// Where frames + WAV are written. Empty = a temporary directory beside the
	// requested output, removed once the mux succeeds. Set it to keep the
	// frame_%06d.ppm runs around (they are what ref2va's ref_video consumes).
	workdir string
	// The ffmpeg binary the composed mux argv is exec'd with. Empty = "ffmpeg"
	// from PATH. libvllm composes the argv and spawns nothing, by design.
	ffmpeg string
	crf    int32
}

// engaged reports whether this config describes an H3 video engine. Load uses
// it to choose which of the two mutually exclusive engine handles to open: the
// checkpoints refuse each other, so guessing is not an option, and every key
// below is meaningless to the text engine.
func (v videoOptions) engaged() bool {
	return v.encoderPath != "" || v.tokenizerPath != "" ||
		v.videoVaePath != "" || v.videoVaeConfig != "" ||
		v.audioVaePath != "" || v.audioVaeConfig != "" ||
		v.promptEmbedsPath != "" || v.partition != ""
}

func parseOptions(opts *pb.ModelOptions) loadOptions {
	lo := loadOptions{}
	applyOptionsList(&lo, opts.GetOptions())
	applyEngineArgs(&lo, opts.GetEngineArgs())
	return lo
}

// applyOptionsList reads the legacy free-form "key:value" list. strings.Cut
// splits on the FIRST colon only, so a JSON object value survives intact.
func applyOptionsList(lo *loadOptions, options []string) {
	for _, o := range options {
		k, v, found := strings.Cut(o, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(k) {
		case "block_size":
			lo.blockSize = parseInt32(v, lo.blockSize)
		case "num_blocks":
			lo.numBlocks = parseInt32(v, lo.numBlocks)
		case "max_num_seqs":
			lo.maxNumSeqs = parseInt32(v, lo.maxNumSeqs)
		case "max_num_batched_tokens":
			lo.maxNumBatchedTokens = parseInt32(v, lo.maxNumBatchedTokens)
		case "max_model_len":
			lo.maxModelLen = parseInt32(v, lo.maxModelLen)
		case "scheduling_policy", "schedule_policy":
			lo.schedulingPolicy = strings.TrimSpace(v)
		case "tool_parser", "tool_call_parser":
			lo.toolParser = strings.TrimSpace(v)
		case "reasoning_parser":
			lo.reasoningParser = strings.TrimSpace(v)
		case "speculative_config":
			lo.speculativeConfig = strings.TrimSpace(v)
		case "kv_transfer_config":
			lo.kvTransferConfig = strings.TrimSpace(v)
		case "tokenizer_config", "tokenizer_config_path":
			lo.tokenizerConfigPath = strings.TrimSpace(v)
		case "enable_prefix_caching", "enable_radix_attention":
			if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
				lo.enablePrefixCaching = boolTriState(b)
			}
		case "enable_jump_forward":
			if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
				lo.enableJumpForward = boolTriState(b)
			}
		default:
			applyVideoOption(&lo.video, strings.TrimSpace(k), v)
		}
	}
}

// applyVideoOption reads one MiniMax-H3 key. Split out of applyOptionsList so
// the video surface stays legible next to the videoOptions it fills, and so
// video_test.go can exercise it directly.
func applyVideoOption(vo *videoOptions, key, value string) bool {
	v := strings.TrimSpace(value)
	switch key {
	case "video_encoder":
		vo.encoderPath = v
	case "video_tokenizer":
		vo.tokenizerPath = v
	case "video_vae":
		vo.videoVaePath = v
	case "video_vae_config":
		vo.videoVaeConfig = v
	case "audio_vae":
		vo.audioVaePath = v
	case "audio_vae_config":
		vo.audioVaeConfig = v
	case "video_prompt_embeds":
		vo.promptEmbedsPath = v
	case "video_partition":
		vo.partition = strings.ToLower(v)
	case "video_device":
		switch strings.ToLower(v) {
		case "cpu":
			vo.device, vo.deviceSet = videoDeviceCPU, true
		case "cuda", "gpu":
			vo.device, vo.deviceSet = videoDeviceCUDA, true
		default:
			xlog.Warn("[vllm-cpp] ignoring unknown video_device", "value", v)
		}
	case "video_dequant_bf16":
		if b, err := strconv.ParseBool(v); err == nil {
			vo.dequantBf16 = boolInt32(b)
		}
	case "video_fp4_resident":
		if b, err := strconv.ParseBool(v); err == nil {
			vo.fp4Resident = boolInt32(b)
		}
	case "video_width":
		vo.width = parseInt32(v, vo.width)
	case "video_height":
		vo.height = parseInt32(v, vo.height)
	case "video_num_frames":
		vo.numFrames = parseInt32(v, vo.numFrames)
	case "video_steps":
		vo.steps = parseInt32(v, vo.steps)
	case "video_workdir":
		vo.workdir = v
	case "video_crf":
		vo.crf = parseInt32(v, vo.crf)
	case "ffmpeg", "ffmpeg_path":
		vo.ffmpeg = v
	default:
		return false
	}
	return true
}

// videoScalarString renders an engine_args scalar so the video keys can share
// one parser with the "key:value" list. Objects and arrays have no video
// meaning and are left to the caller's unknown-key path.
func videoScalarString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	default:
		return "", false
	}
}

func boolInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// applyEngineArgs overlays the `engine_args:` JSON object. A document that does
// not parse is logged and skipped: engine_args is shared with the other engines
// (the vLLM and SGLang backends read the same field), so a stray key must not
// take the model down.
func applyEngineArgs(lo *loadOptions, engineArgs string) {
	if strings.TrimSpace(engineArgs) == "" {
		return
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(engineArgs), &args); err != nil {
		xlog.Warn("[vllm-cpp] ignoring unparseable engine_args", "error", err)
		return
	}
	for k, v := range args {
		switch k {
		case "block_size":
			lo.blockSize = jsonInt32(v, lo.blockSize)
		case "num_blocks":
			lo.numBlocks = jsonInt32(v, lo.numBlocks)
		case "max_num_seqs":
			lo.maxNumSeqs = jsonInt32(v, lo.maxNumSeqs)
		case "max_num_batched_tokens":
			lo.maxNumBatchedTokens = jsonInt32(v, lo.maxNumBatchedTokens)
		case "max_model_len":
			lo.maxModelLen = jsonInt32(v, lo.maxModelLen)
		case "scheduling_policy", "schedule_policy":
			lo.schedulingPolicy = jsonString(v, lo.schedulingPolicy)
		case "tool_parser", "tool_call_parser":
			lo.toolParser = jsonString(v, lo.toolParser)
		case "reasoning_parser":
			lo.reasoningParser = jsonString(v, lo.reasoningParser)
		case "tokenizer_config", "tokenizer_config_path":
			lo.tokenizerConfigPath = jsonString(v, lo.tokenizerConfigPath)
		case "speculative_config":
			lo.speculativeConfig = jsonDocument(v, lo.speculativeConfig, k)
		case "kv_transfer_config":
			lo.kvTransferConfig = jsonDocument(v, lo.kvTransferConfig, k)
		case "enable_prefix_caching", "enable_radix_attention":
			if b, ok := v.(bool); ok {
				lo.enablePrefixCaching = boolTriState(b)
			}
		case "enable_jump_forward":
			if b, ok := v.(bool); ok {
				lo.enableJumpForward = boolTriState(b)
			}
		default:
			if s, ok := videoScalarString(v); ok && applyVideoOption(&lo.video, k, s) {
				continue
			}
			xlog.Debug("[vllm-cpp] ignoring unknown engine_args key", "key", k)
		}
	}
}

// boolTriState maps a YAML/JSON boolean onto the ABI's tri-state encoding. An
// explicit `false` must reach the engine as force-OFF (2), NOT as the 0 that
// means "defer". The difference is real in both directions: prefix caching
// defaults ON for dense archs and OFF for hybrid ones, and jump forward defers
// to VT_ENABLE_JUMP_FORWARD.
func boolTriState(on bool) int32 {
	if on {
		return triStateOn
	}
	return triStateOff
}

// jsonDocument normalises an object-valued engine_args entry to a JSON string
// for the C ABI. YAML nesting arrives as a map (the natural spelling); a
// pre-encoded JSON string is accepted too, since a config round-tripped through
// a flat store may carry it that way.
func jsonDocument(v any, fallback string, key string) string {
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) == "" {
			return fallback
		}
		return t
	default:
		buf, err := json.Marshal(t)
		if err != nil {
			xlog.Warn("[vllm-cpp] ignoring unencodable engine_args value", "key", key, "error", err)
			return fallback
		}
		return string(buf)
	}
}

func jsonString(v any, fallback string) string {
	s, ok := v.(string)
	if !ok {
		return fallback
	}
	return strings.TrimSpace(s)
}

// jsonInt32 accepts the float64 a JSON number decodes to, plus the string
// spelling a YAML config may produce. Non-positive values keep the fallback:
// every knob this covers uses "<= 0 means the engine default".
func jsonInt32(v any, fallback int32) int32 {
	switch t := v.(type) {
	case float64:
		if t <= 0 || t > 1<<31-1 {
			return fallback
		}
		return int32(t)
	case string:
		return parseInt32(t, fallback)
	default:
		return fallback
	}
}

// resolveDraftModelPath rewrites a DFlash draft reference into an absolute path
// the engine can actually open.
//
// The engine resolves `speculative_config.model` against a directory containing
// config.json, or against ~/.cache/huggingface/hub/models--<org>--<repo>/
// snapshots/* - and it NEVER downloads. LocalAI keeps models in its own
// directory, so a bare HF repo id (the spelling the vLLM docs teach) misses the
// HF cache and dies deep in the load with "draft checkpoint not found", which
// reads like a broken checkpoint rather than a missing download.
//
// So: try the reference as given, then the last path segment under the models
// dir (`z-lab/Qwen3.6-27B-DFlash` -> `<models>/Qwen3.6-27B-DFlash`, which is
// what LocalAI's own downloader produces), then the whole reference under the
// models dir. If none exist, fail HERE with a message naming both what was
// asked for and where we looked.
//
// mtp and ngram carry no separate draft checkpoint, so they pass through. A
// document that does not parse also passes through: the engine owns config
// validation and produces the better error.
func resolveDraftModelPath(speculativeConfig, modelsDir string) (string, error) {
	if strings.TrimSpace(speculativeConfig) == "" {
		return speculativeConfig, nil
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(speculativeConfig), &spec); err != nil {
		return speculativeConfig, nil
	}
	if method, _ := spec["method"].(string); !strings.EqualFold(method, "dflash") {
		return speculativeConfig, nil
	}

	ref, _ := spec["model"].(string)
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf(
			"vllm-cpp: speculative_config method %q requires a \"model\" key naming the draft checkpoint", "dflash")
	}

	candidates := []string{ref}
	if modelsDir != "" {
		if base := path.Base(filepath.ToSlash(ref)); base != "" && base != "." && base != "/" {
			candidates = append(candidates, filepath.Join(modelsDir, base))
		}
		candidates = append(candidates, filepath.Join(modelsDir, filepath.FromSlash(ref)))
	}

	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "config.json")); err != nil {
			continue
		}
		abs, err := filepath.Abs(c)
		if err != nil {
			abs = c
		}
		spec["model"] = abs
		out, err := json.Marshal(spec)
		if err != nil {
			return "", fmt.Errorf("vllm-cpp: re-encoding speculative_config: %w", err)
		}
		xlog.Info("[vllm-cpp] resolved DFlash draft checkpoint", "reference", ref, "path", abs)
		return string(out), nil
	}

	return "", fmt.Errorf(
		"vllm-cpp: DFlash draft checkpoint %q not found (looked in: %s). "+
			"The engine does not download drafts - install the draft model into LocalAI first, "+
			"or set speculative_config.model to an absolute path to a directory containing config.json",
		ref, strings.Join(candidates, ", "))
}

func parseInt32(s string, fallback int32) int32 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil || n <= 0 {
		return fallback
	}
	return int32(n)
}
