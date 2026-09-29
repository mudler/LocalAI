package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// Model kinds returned by parakeet_capi_model_kind (ABI v8; mirrors the
// PARAKEET_MODEL_KIND_* defines in parakeet_capi.h).
const (
	modelKindNone        = 0
	modelKindASR         = 1
	modelKindDiarization = 2
	modelKindSound       = 3
)

// Diarization streaming latency modes (mirrors PARAKEET_DIAR_LATENCY_* in
// parakeet_capi.h). diarLatencyLow is the spec's default when
// diarization_latency: is unset.
const (
	diarLatencyModel    int32 = 0
	diarLatencyLow      int32 = 1
	diarLatencyVeryLow  int32 = 2
	diarLatencyUltraLow int32 = 3
)

// modelKindName renders a model kind for error messages.
func modelKindName(kind int32) string {
	switch kind {
	case modelKindASR:
		return "ASR"
	case modelKindDiarization:
		return "diarization"
	case modelKindSound:
		return "sound"
	default:
		return "unknown"
	}
}

// optString reads a string model option (key:value form) from ModelOptions,
// returning "" when the key is absent. Same strings.Cut parsing as optInt.
func optString(opts *pb.ModelOptions, key string) string {
	for _, o := range opts.GetOptions() {
		k, v, ok := strings.Cut(o, ":")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// resolveModelPath resolves a companion model option's path against
// modelPath (opts.ModelPath, the LocalAI models root): an absolute p, or an
// empty modelPath, passes through unchanged; anything else is joined onto
// modelPath. Mirrors vibevoice-cpp's resolvePath for tokenizer=/voice=/etc.
func resolveModelPath(modelPath, p string) string {
	if p == "" || filepath.IsAbs(p) || modelPath == "" {
		return p
	}
	return filepath.Join(modelPath, p)
}

// parseDiarLatency maps the diarization_latency option value to a
// PARAKEET_DIAR_LATENCY_* mode. "" defaults to "low" (the spec's default);
// any other unrecognized value is a Load error.
func parseDiarLatency(s string) (int32, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return diarLatencyLow, nil
	case "model":
		return diarLatencyModel, nil
	case "low":
		return diarLatencyLow, nil
	case "very_low":
		return diarLatencyVeryLow, nil
	case "ultra_low":
		return diarLatencyUltraLow, nil
	default:
		return 0, fmt.Errorf("parakeet-cpp: unknown diarization_latency %q (want model|low|very_low|ultra_low)", s)
	}
}

// companionSpec is one asr_model:/diarization_model:/sound_model: option: its
// name (for error messages and path resolution), the raw option value, the
// model kind the loaded companion must report, the ParakeetCpp field it is
// assigned to on success, and a getter for that same field's current value
// (used to reject a companion whose role the primary already occupies).
type companionSpec struct {
	optName  string
	value    string
	wantKind int32
	assign   func(*ParakeetCpp, uintptr)
	current  func(*ParakeetCpp) uintptr
}

// indefiniteArticle returns "an" for a word starting with a vowel sound and
// "a" otherwise, for grammatical error messages built from modelKindName.
func indefiniteArticle(word string) string {
	if len(word) == 0 {
		return "a"
	}
	switch word[0] {
	case 'A', 'E', 'I', 'O', 'U', 'a', 'e', 'i', 'o', 'u':
		return "an"
	default:
		return "a"
	}
}

// loadRoles loads opts.ModelFile as the primary parakeet_ctx, classifies it
// with parakeet_capi_model_kind (ABI v8) into ctxPtr/diarCtx/tagCtx, and
// loads any companion models named in Options[] (asr_model:,
// diarization_model:, sound_model:; paths resolved against opts.ModelPath).
// It also parses diarization_latency: into p.diarLatency.
//
// Against an older libparakeet.so (CppModelKind == nil) the primary is
// treated as ASR — the pre-v8 behavior — and companion model options are
// rejected outright, since there is no way to verify what they loaded.
//
// On any failure every context this call opened (primary and any companions
// loaded before the failure) is freed before the error is returned.
func (p *ParakeetCpp) loadRoles(opts *pb.ModelOptions) error {
	diarModelOpt := optString(opts, "diarization_model")
	asrModelOpt := optString(opts, "asr_model")
	soundModelOpt := optString(opts, "sound_model")
	hasCompanionOpts := diarModelOpt != "" || asrModelOpt != "" || soundModelOpt != ""

	if hasCompanionOpts && CppModelKind == nil {
		return errors.New("parakeet-cpp: asr_model/diarization_model/sound_model options need " +
			"parakeet_capi_model_kind (ABI v8) to verify what they load; the loaded libparakeet.so " +
			"is too old to report companion model roles")
	}

	latency, err := parseDiarLatency(optString(opts, "diarization_latency"))
	if err != nil {
		return err
	}

	primary := CppLoad(opts.ModelFile)
	if primary == 0 {
		// No ctx to ask for last_error (the C-API's last-error buffer lives on
		// the ctx that was never returned). Surface the path so the operator
		// at least knows which load failed.
		return fmt.Errorf("parakeet-cpp: parakeet_capi_load failed for %q", opts.ModelFile)
	}
	loaded := []uintptr{primary}
	// freeLoaded undoes everything loadRoles opened this call: every context
	// it freed AND every ParakeetCpp field it may have assigned (the primary
	// lands in one of ctxPtr/diarCtx/tagCtx before the companion loop runs,
	// and an earlier companion's spec.assign runs before a later one fails).
	// Leaving a role field pointing at a freed ctx would double-free it on a
	// later Free() call.
	freeLoaded := func() {
		for _, c := range loaded {
			CppFree(c)
		}
		p.ctxPtr, p.diarCtx, p.tagCtx = 0, 0, 0
		p.companions = nil
	}

	primaryKind := int32(modelKindASR) // old-library default: today's behavior
	if CppModelKind != nil {
		primaryKind = CppModelKind(primary)
		if primaryKind == modelKindNone {
			xlog.Warn("parakeet-cpp: parakeet_capi_model_kind reported PARAKEET_MODEL_KIND_NONE " +
				"for a successfully loaded primary; treating it as an ASR model")
		}
	}
	switch primaryKind {
	case modelKindDiarization:
		p.diarCtx = primary
	case modelKindSound:
		p.tagCtx = primary
	default:
		p.ctxPtr = primary
	}

	specs := []companionSpec{
		{"diarization_model", diarModelOpt, modelKindDiarization,
			func(pp *ParakeetCpp, c uintptr) { pp.diarCtx = c },
			func(pp *ParakeetCpp) uintptr { return pp.diarCtx }},
		{"asr_model", asrModelOpt, modelKindASR,
			func(pp *ParakeetCpp, c uintptr) { pp.ctxPtr = c },
			func(pp *ParakeetCpp) uintptr { return pp.ctxPtr }},
		{"sound_model", soundModelOpt, modelKindSound,
			func(pp *ParakeetCpp, c uintptr) { pp.tagCtx = c },
			func(pp *ParakeetCpp) uintptr { return pp.tagCtx }},
	}
	for _, spec := range specs {
		if spec.value == "" {
			continue
		}
		// A companion whose role the primary already occupies (e.g. asr_model:
		// on an already-ASR primary) would overwrite that role field below,
		// leaking the primary ctx: Free() only walks ctxPtr/diarCtx/tagCtx, so
		// the overwritten pointer is never freed. Reject it before loading.
		if spec.current(p) != 0 {
			freeLoaded()
			return fmt.Errorf("parakeet-cpp: %s is not allowed on %s %s model",
				spec.optName, indefiniteArticle(modelKindName(spec.wantKind)), modelKindName(spec.wantKind))
		}
		resolved := resolveModelPath(opts.ModelPath, spec.value)
		cctx := CppLoad(resolved)
		if cctx == 0 {
			freeLoaded()
			return fmt.Errorf("parakeet-cpp: failed to load %s %q", spec.optName, resolved)
		}
		loaded = append(loaded, cctx)
		if gotKind := CppModelKind(cctx); gotKind != spec.wantKind {
			freeLoaded()
			return fmt.Errorf("parakeet-cpp: %s %q is a %s model, expected a %s model",
				spec.optName, resolved, modelKindName(gotKind), modelKindName(spec.wantKind))
		}
		spec.assign(p, cctx)
		p.companions = append(p.companions, cctx)
	}

	p.diarLatency = latency
	return nil
}

// notASRError reports why AudioTranscription (and the streaming/live RPCs)
// cannot run when p.ctxPtr == 0: a loaded diarization or sound primary with
// no asr_model companion, named explicitly so the caller knows to use the
// right RPC instead of a generic "model not loaded". Returns nil when
// neither role is loaded (genuinely no model), leaving the caller to report
// the ordinary ModelNotLoaded error.
func (p *ParakeetCpp) notASRError() error {
	switch {
	case p.diarCtx != 0:
		return errors.New("parakeet-cpp: loaded model is a diarization model, not ASR " +
			"(use Diarize, or load with an asr_model: companion)")
	case p.tagCtx != 0:
		return errors.New("parakeet-cpp: loaded model is a sound model, not ASR " +
			"(use SoundDetection)")
	default:
		return nil
	}
}
