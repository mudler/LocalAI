package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Bundle GGUF support. A bundle is one GGUF file holding several models
// ("components"), each with its own kind and licence (docs/bundle.md in
// parakeet.cpp). The library opens one component per context; this file works
// out which component each role of a loaded model uses.
//
// Model options (key:value, all optional):
//
//	bundle_asr:<name>        the ASR component of the primary file (or of the
//	                         asr_model: file), when the bundle has several
//	vad_component:<name>     the VAD component; with no vad_model: the VAD of the
//	                         primary file
//	diar_component:<name>    the diarization component; with no diarization_model:
//	                         the one of the primary file
//	sound_component:<name>   the sound (CED) component; likewise for sound_model:
//	speaker_component:<name> the speaker encoder component; likewise for speaker_model:
//
// A companion option (diarization_model: and the others) may name a bundle: the
// only component of the wanted kind is used, and a *_component option picks one
// when there are several. A Silero VAD component of the primary bundle is loaded
// without any option, because it costs little and serves the VAD endpoint.

// Component kinds as the C-API reports them in the bundle component list.
const (
	componentASR   = "asr"
	componentVAD   = "vad"
	componentDiar  = "diar"
	componentSound = "ced"
	componentVoice = "voice"
)

// bundleComponent is one entry of parakeet_capi_bundle_components_json. The
// library reports more fields (sources, hashes); only the ones this backend
// reads are decoded.
type bundleComponent struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	License string `json:"license"`
	Bytes   int64  `json:"bytes"`
}

// componentKindModel maps a component kind to the model kind the loaded context
// must report.
func componentKindModel(kind string) (int32, bool) {
	switch kind {
	case componentASR:
		return modelKindASR, true
	case componentVAD:
		return modelKindVAD, true
	case componentDiar:
		return modelKindDiarization, true
	case componentSound:
		return modelKindSound, true
	case componentVoice:
		return modelKindSpeaker, true
	}
	return 0, false
}

// kindForModel is the reverse of componentKindModel.
func kindForModel(model int32) string {
	switch model {
	case modelKindASR:
		return componentASR
	case modelKindVAD:
		return componentVAD
	case modelKindDiarization:
		return componentDiar
	case modelKindSound:
		return componentSound
	case modelKindSpeaker:
		return componentVoice
	}
	return ""
}

// bundleComponents lists the components of the file at path. It returns nil
// when the file is not a bundle, and also when the library has no bundle
// support (the three bundle symbols are probed together in main.go), so callers
// treat both as a plain file. Only the header of the file is read.
func bundleComponents(path string) ([]bundleComponent, error) {
	if CppBundleComponentsJSON == nil {
		return nil, nil
	}
	cstr := CppBundleComponentsJSON(path)
	if cstr == 0 {
		return nil, nil
	}
	raw := goStringFromCPtr(cstr)
	CppFreeString(cstr)
	var comps []bundleComponent
	if err := json.Unmarshal([]byte(raw), &comps); err != nil {
		return nil, fmt.Errorf("parakeet-cpp: decode bundle component list of %q: %w", path, err)
	}
	return comps, nil
}

// describeComponents renders a component list for error messages, for example
// "asr (asr), vad (vad)": name, then kind.
func describeComponents(comps []bundleComponent) string {
	parts := make([]string, 0, len(comps))
	for _, c := range comps {
		parts = append(parts, fmt.Sprintf("%s (%s)", c.Name, c.Kind))
	}
	return strings.Join(parts, ", ")
}

// componentsOfKind returns the names of the components of one kind.
func componentsOfKind(comps []bundleComponent, kind string) []string {
	var names []string
	for _, c := range comps {
		if c.Kind == kind {
			names = append(names, c.Name)
		}
	}
	return names
}

// pickComponent chooses the component of kind wantKind in a bundle. A name set
// by the user must exist and have that kind; with no name the bundle must hold
// exactly one component of the kind. what names the role for the messages and
// nameOpt the option that picks a component.
func pickComponent(path string, comps []bundleComponent, name, wantKind, what, nameOpt string) (string, error) {
	if name != "" {
		for _, c := range comps {
			if c.Name != name {
				continue
			}
			if c.Kind != wantKind {
				return "", fmt.Errorf("parakeet-cpp: component %q of %q has kind %q, but %s needs kind %q",
					name, path, c.Kind, what, wantKind)
			}
			return name, nil
		}
		return "", fmt.Errorf("parakeet-cpp: %q has no component %q (components: %s)",
			path, name, describeComponents(comps))
	}
	names := componentsOfKind(comps, wantKind)
	switch len(names) {
	case 1:
		return names[0], nil
	case 0:
		return "", fmt.Errorf("parakeet-cpp: %s needs a %q component, but the bundle %q has none (components: %s)",
			what, wantKind, path, describeComponents(comps))
	default:
		return "", fmt.Errorf("parakeet-cpp: the bundle %q has several %q components (%s); set %s to pick one",
			path, wantKind, strings.Join(names, ", "), nameOpt)
	}
}

// loadComponent opens one component of a bundle and returns the library's
// reason on failure.
func loadComponent(path, name string) (uintptr, error) {
	if CppLoadComponent == nil {
		return 0, errors.New("parakeet-cpp: bundle components need a libparakeet.so with parakeet_capi_load_component; rebuild the backend against a newer parakeet.cpp")
	}
	ctx := CppLoadComponent(path, name)
	if ctx == 0 {
		reason := ""
		if CppLoadError != nil {
			reason = CppLoadError()
		}
		return 0, fmt.Errorf("parakeet-cpp: failed to load component %q of %q: %s", name, path, reason)
	}
	return ctx, nil
}

// roleHint ends the error of an RPC that needs a role the loaded model does not
// have. For a bundle it says whether the bundle has a component of that kind
// (and which option loads it) or lacks one; for any other model it is empty.
func (p *ParakeetCpp) roleHint(kind, option string) string {
	if len(p.bundle) == 0 {
		return ""
	}
	if names := componentsOfKind(p.bundle, kind); len(names) > 0 {
		return fmt.Sprintf(" (the model file is a bundle with a %q component %s: set %s:%s to load it)",
			kind, strings.Join(names, ", "), option, names[0])
	}
	return fmt.Sprintf(" (the model file is a bundle without a %q component; components: %s)",
		kind, describeComponents(p.bundle))
}
