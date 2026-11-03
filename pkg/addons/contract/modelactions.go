// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ModelAction contributes navigation for a non-native model capability. Page
// must be declared; Path is an optional suffix, never a URL or query template.
type ModelAction struct {
	Capability string `json:"capability"`
	Page       string `json:"page"`
	Path       string `json:"path,omitempty"`
}

// ModelActionCapabilities returns the host's concrete non-native model flags.
// FLAG_ANY and aggregate FLAG_LLM are not destinations. Chat and completion
// remain native and cannot be shadowed. Keep vocabulary parity in core/config.
func ModelActionCapabilities() []string {
	return []string{
		"FLAG_EDIT", "FLAG_EMBEDDINGS", "FLAG_RERANK", "FLAG_IMAGE", "FLAG_TRANSCRIPT",
		"FLAG_TTS", "FLAG_SOUND_GENERATION", "FLAG_TOKENIZE", "FLAG_VAD", "FLAG_VIDEO",
		"FLAG_DETECTION", "FLAG_VISION", "FLAG_FACE_RECOGNITION", "FLAG_SPEAKER_RECOGNITION",
		"FLAG_AUDIO_TRANSFORM", "FLAG_DIARIZATION", "FLAG_SOUND_CLASSIFICATION",
		"FLAG_REALTIME_AUDIO", "FLAG_SCORE", "FLAG_DEPTH", "FLAG_TOKEN_CLASSIFY",
		"FLAG_3D", "FLAG_3D_ANIMATION", "FLAG_DECISIONS", "FLAG_UPSCALE", "FLAG_MOTION", "FLAG_FORECAST",
	}
}

// MaxModelActions bounds the declaration independently of the wire byte limit.
const MaxModelActions = 27

var modelRouteSegment = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,64}$`)

func (a *ModelAction) UnmarshalJSON(b []byte) error {
	fields, err := exactObjectFields(b, DefaultLimits().ManifestBytes, "capability", "page", "path")
	if err != nil {
		return err
	}
	type wire ModelAction
	var v wire
	if err := strictJSON(b, &v, DefaultLimits().ManifestBytes); err != nil {
		return err
	}
	if _, present := fields["path"]; present && v.Path == "" {
		return fmt.Errorf("model action path must be nonempty when present")
	}
	*a = ModelAction(v)
	return nil
}

// ValidateModelActions uses the SDK resolveSubpath grammar: at most eight
// segments and 256 ASCII characters including the page, 64 per segment.
// Declarations intentionally disallow the SDK's optional leading slash/query.
func ValidateModelActions(u *UI) error {
	if len(u.ModelActions) > MaxModelActions {
		return fmt.Errorf("too many model actions")
	}
	seen := map[string]bool{}
	for _, a := range u.ModelActions {
		if !slices.Contains(ModelActionCapabilities(), a.Capability) || seen[a.Capability] {
			return fmt.Errorf("unsupported or duplicate model action capability")
		}
		seen[a.Capability] = true
		if !ValidName(a.Page) || !slices.Contains(u.Pages, a.Page) {
			return fmt.Errorf("model action requires a declared page")
		}
		route := a.Page
		if a.Path != "" {
			route += "/" + a.Path
		}
		segments := strings.Split(route, "/")
		if len(route) > 256 || len(segments) > 8 {
			return fmt.Errorf("model action route exceeds SDK bounds")
		}
		for _, s := range segments {
			if !modelRouteSegment.MatchString(s) || s == "." || s == ".." {
				return fmt.Errorf("invalid model action route segment")
			}
		}
	}
	return nil
}
