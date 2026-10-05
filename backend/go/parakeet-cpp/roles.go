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
	modelKindSpeaker     = 4
	modelKindVAD         = 5 // Silero VAD GGUF
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
	case modelKindSpeaker:
		return "speaker"
	case modelKindVAD:
		return "VAD"
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
	// component and compOpt are the bundle component name for this role and the
	// option that sets it (bundle.go). With a component set and no value, the
	// role is loaded from the primary model file, which must be a bundle.
	component string
	compOpt   string
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
	speakerModelOpt := optString(opts, "speaker_model")
	vadModelOpt := optString(opts, "vad_model")
	bundleASR := optString(opts, "bundle_asr")
	vadComp := optString(opts, "vad_component")
	diarComp := optString(opts, "diar_component")
	soundComp := optString(opts, "sound_component")
	speakerComp := optString(opts, "speaker_component")
	hasCompanionOpts := diarModelOpt != "" || asrModelOpt != "" || soundModelOpt != "" || speakerModelOpt != "" || vadModelOpt != "" ||
		bundleASR != "" || vadComp != "" || diarComp != "" || soundComp != "" || speakerComp != ""

	if hasCompanionOpts && CppModelKind == nil {
		return errors.New("parakeet-cpp: asr_model/diarization_model/sound_model/speaker_model/vad_model and the bundle component options need " +
			"parakeet_capi_model_kind (ABI v8) to verify what they load; the loaded libparakeet.so " +
			"is too old to report companion model roles")
	}

	if speakerModelOpt != "" || speakerComp != "" {
		if CppSpeakerRegistryAddEmbedding == nil || CppSpeakerDim == nil || CppSceneStreamBeginSpeaker == nil {
			return errors.New("parakeet-cpp: speaker_model and speaker_component need libparakeet.so ABI 10 " +
				"(parakeet_capi_speaker_registry_add_embedding); the loaded library is older")
		}
	}
	accept, err := parseSpeakerThreshold(optString(opts, "speaker_threshold"))
	if err != nil {
		return err
	}
	margin, err := parseSpeakerMargin(optString(opts, "speaker_margin"))
	if err != nil {
		return err
	}

	latency, err := parseDiarLatency(optString(opts, "diarization_latency"))
	if err != nil {
		return err
	}

	primary, comps, err := loadPrimary(opts.ModelFile, bundleASR)
	if err != nil {
		return err
	}
	p.bundle = comps
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
		p.ctxPtr, p.diarCtx, p.tagCtx, p.spkCtx, p.vadCtx = 0, 0, 0, 0, 0
		p.bundle = nil
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
	case modelKindSpeaker:
		freeLoaded()
		return errors.New("parakeet-cpp: a speaker model cannot be the primary model; " +
			"use it as speaker_model: next to a diarization model")
	case modelKindVAD:
		p.vadCtx = primary
	default:
		p.ctxPtr = primary
	}

	specs := []companionSpec{
		{"diarization_model", diarModelOpt, modelKindDiarization,
			func(pp *ParakeetCpp, c uintptr) { pp.diarCtx = c },
			func(pp *ParakeetCpp) uintptr { return pp.diarCtx },
			diarComp, "diar_component"},
		{"asr_model", asrModelOpt, modelKindASR,
			func(pp *ParakeetCpp, c uintptr) { pp.ctxPtr = c },
			func(pp *ParakeetCpp) uintptr { return pp.ctxPtr },
			bundleASR, "bundle_asr"},
		{"sound_model", soundModelOpt, modelKindSound,
			func(pp *ParakeetCpp, c uintptr) { pp.tagCtx = c },
			func(pp *ParakeetCpp) uintptr { return pp.tagCtx },
			soundComp, "sound_component"},
		{"speaker_model", speakerModelOpt, modelKindSpeaker,
			func(pp *ParakeetCpp, c uintptr) { pp.spkCtx = c },
			func(pp *ParakeetCpp) uintptr { return pp.spkCtx },
			speakerComp, "speaker_component"},
		{"vad_model", vadModelOpt, modelKindVAD,
			func(pp *ParakeetCpp, c uintptr) { pp.vadCtx = c },
			func(pp *ParakeetCpp) uintptr { return pp.vadCtx },
			vadComp, "vad_component"},
	}
	for _, spec := range specs {
		path := resolveModelPath(opts.ModelPath, spec.value)
		fromPrimary := false
		if spec.value == "" {
			// bundle_asr names the ASR of the primary file, so on its own it is
			// not a request for an asr_model companion.
			if spec.component == "" || spec.optName == "asr_model" {
				continue
			}
			// A component option with no companion file: the role comes from the
			// primary file, which must be a bundle.
			if comps == nil {
				freeLoaded()
				return fmt.Errorf("parakeet-cpp: %s needs the model file or a %s to be a bundle GGUF; %q is not one",
					spec.compOpt, spec.optName, opts.ModelFile)
			}
			path, fromPrimary = opts.ModelFile, true
		}
		// A companion whose role the primary already occupies (e.g. asr_model:
		// on an already-ASR primary) would overwrite that role field below,
		// leaking the primary ctx: Free() walks ctxPtr/diarCtx/tagCtx/spkCtx, so
		// the overwritten pointer is never freed. Reject it before loading.
		if spec.current(p) != 0 {
			freeLoaded()
			return fmt.Errorf("parakeet-cpp: %s is not allowed on %s %s model",
				spec.optName, indefiniteArticle(modelKindName(spec.wantKind)), modelKindName(spec.wantKind))
		}
		cctx, err := loadCompanion(spec, path, fromPrimary, comps)
		if cctx != 0 {
			loaded = append(loaded, cctx)
		}
		if err != nil {
			freeLoaded()
			return err
		}
		spec.assign(p, cctx)
		p.companions = append(p.companions, cctx)
	}

	// A Silero VAD component of the primary bundle is the VAD source without any
	// option, as in the parakeet-cli rules: it is small and serves the VAD
	// endpoint and vad:true. Not finding one, or not being able to load it, is
	// not an error here: the ASR head (if any) stays the fallback.
	if vadModelOpt == "" && vadComp == "" && p.vadCtx == 0 && p.ctxPtr != 0 && comps != nil && CppLoadComponent != nil {
		if names := componentsOfKind(comps, componentVAD); len(names) == 1 {
			vctx, verr := loadComponent(opts.ModelFile, names[0])
			switch {
			case verr != nil:
				xlog.Warn("parakeet-cpp: bundle VAD component not loaded", "error", verr)
			case CppModelKind(vctx) != modelKindVAD:
				CppFree(vctx)
				xlog.Warn("parakeet-cpp: bundle VAD component is not a Silero model; using the ASR VAD head if any", "component", names[0])
			default:
				loaded = append(loaded, vctx)
				p.vadCtx = vctx
				p.companions = append(p.companions, vctx)
			}
		}
	}

	if (vadModelOpt != "" || vadComp != "") && p.ctxPtr == 0 {
		freeLoaded()
		return errors.New("parakeet-cpp: vad_model and vad_component cut audio for transcription and needs an ASR model (the primary or asr_model:)")
	}
	if p.spkCtx != 0 && p.diarCtx == 0 {
		freeLoaded()
		return errors.New("parakeet-cpp: speaker_model needs a diarization model (the primary, diarization_model: or diar_component:)")
	}
	p.speakerAccept, p.speakerMargin = accept, margin
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
	case p.vadCtx != 0:
		return errors.New("parakeet-cpp: loaded model is a Silero VAD model, not ASR " +
			"(use the VAD endpoint, or load an ASR model with vad_model:)")
	default:
		return nil
	}
}

// loadPrimary opens the primary model file. A bundle is opened by component: the
// bundle_asr one, else its only ASR component. A bundle with several ASR
// components and no bundle_asr is refused with their names. A bundle with no ASR
// component is left to the library, which opens its only loadable component.
// Any other file is opened as before. comps lists the components of a bundle
// (nil for a plain file); nothing is loaded when it returns an error.
func loadPrimary(path, bundleASR string) (ctx uintptr, comps []bundleComponent, err error) {
	comps, err = bundleComponents(path)
	if err != nil {
		return 0, nil, err
	}
	if comps == nil {
		if bundleASR != "" {
			return 0, nil, fmt.Errorf("parakeet-cpp: bundle_asr needs a bundle GGUF; %q is not one (or libparakeet.so has no bundle support)", path)
		}
		ctx = CppLoad(path)
		if ctx == 0 {
			// No ctx to ask for last_error (the C-API's last-error buffer lives on
			// the ctx that was never returned). Surface the path so the operator
			// at least knows which load failed.
			//
			// A packed ternary Redux GGUF (redux-packed) is the likeliest cause on a
			// GPU build: the library refuses it with a message that goes to its own
			// log, not through the C-API, so name the cause here.
			return 0, nil, fmt.Errorf("parakeet-cpp: parakeet_capi_load failed for %q (see the backend log for the library message; "+
				"a packed ternary Redux model is CPU only and is refused on a GPU backend, use the redux-f16 or redux-q8_0 file there)", path)
		}
		return ctx, nil, nil
	}
	if bundleASR == "" && len(componentsOfKind(comps, componentASR)) == 0 {
		ctx = CppLoad(path)
		if ctx == 0 {
			return 0, nil, fmt.Errorf("parakeet-cpp: failed to load the bundle %q: %s", path, loadErrorText())
		}
		return ctx, comps, nil
	}
	name, err := pickComponent(path, comps, bundleASR, componentASR, "the primary model", "bundle_asr")
	if err != nil {
		return 0, nil, err
	}
	ctx, err = loadComponent(path, name)
	if err != nil {
		return 0, nil, withRedux(err)
	}
	return ctx, comps, nil
}

// withRedux adds the CPU-only hint of a packed Redux component to a failed
// component load: the library refuses it on a GPU backend.
func withRedux(err error) error {
	return fmt.Errorf("%w (a packed ternary Redux model is CPU only and is refused on a GPU backend)", err)
}

func loadErrorText() string {
	if CppLoadError == nil {
		return ""
	}
	return CppLoadError()
}

// loadCompanion loads the model of one companion role from path. A bundle gives
// its component of the wanted kind (named by the role's component option, else
// the only one); a plain file is loaded as before and refuses a component
// option. It returns the context even with an error when the context was
// opened but is not usable, so the caller can free it. fromPrimary tells that
// path is the primary file, already known to be a bundle with components comps.
func loadCompanion(spec companionSpec, path string, fromPrimary bool, comps []bundleComponent) (uintptr, error) {
	if !fromPrimary {
		var err error
		comps, err = bundleComponents(path)
		if err != nil {
			return 0, err
		}
	}
	var cctx uintptr
	if comps == nil {
		if spec.component != "" {
			return 0, fmt.Errorf("parakeet-cpp: %s needs a bundle GGUF; %q is not one (or libparakeet.so has no bundle support)",
				spec.compOpt, path)
		}
		cctx = CppLoad(path)
		if cctx == 0 {
			return 0, fmt.Errorf("parakeet-cpp: failed to load %s %q", spec.optName, path)
		}
	} else {
		wantKind := kindForModel(spec.wantKind)
		name, err := pickComponent(path, comps, spec.component, wantKind, spec.optName, spec.compOpt)
		if err != nil {
			return 0, err
		}
		cctx, err = loadComponent(path, name)
		if err != nil {
			return 0, err
		}
	}
	if gotKind := CppModelKind(cctx); gotKind != spec.wantKind {
		return cctx, fmt.Errorf("parakeet-cpp: %s %q is %s %s model, expected %s %s model",
			spec.optName, path, indefiniteArticle(modelKindName(gotKind)), modelKindName(gotKind),
			indefiniteArticle(modelKindName(spec.wantKind)), modelKindName(spec.wantKind))
	}
	return cctx, nil
}
