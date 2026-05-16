package config

import (
	"strings"

	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/xlog"
)

// mtpSpecOptions lists the speculative-decoding option keys auto-applied when
// an MTP head is detected on a llama-cpp GGUF. Defaults track the upstream
// MTP PR (ggml-org/llama.cpp#22673):
//
//   - spec_type:draft-mtp      activates Multi-Token Prediction
//   - spec_n_max:6             draft window
//   - spec_p_min:0.75          pinned because upstream marked the 0.75 default
//     with a "change to 0.0f" TODO; locking it here keeps acceptance
//     thresholds stable across future bumps
var mtpSpecOptions = []string{
	"spec_type:draft-mtp",
	"spec_n_max:6",
	"spec_p_min:0.75",
}

// MTPSpecOptions returns a copy of the option keys auto-applied when an MTP
// head is detected. Exported for testing and for the importer.
func MTPSpecOptions() []string {
	out := make([]string, len(mtpSpecOptions))
	copy(out, mtpSpecOptions)
	return out
}

// HasEmbeddedMTPHead reports whether the parsed GGUF declares a Multi-Token
// Prediction head. Detection reads `<arch>.nextn_predict_layers`, which is
// what `gguf_writer.add_nextn_predict_layers(n)` emits in upstream's
// `conversion/qwen.py` MTP mixin. A positive layer count means the head is
// present in the same GGUF as the trunk.
func HasEmbeddedMTPHead(f *gguf.GGUFFile) (uint32, bool) {
	if f == nil {
		return 0, false
	}
	arch := f.Architecture().Architecture
	if arch == "" {
		return 0, false
	}
	v, ok := f.Header.MetadataKV.Get(arch + ".nextn_predict_layers")
	if !ok {
		return 0, false
	}
	n := gguf.ValueNumeric[uint32](v)
	return n, n > 0
}

// hasSpecTypeOption returns true when the slice already contains a
// user-configured `spec_type:` / `speculative_type:` entry. Used to avoid
// clobbering an explicit choice with the MTP auto-defaults.
func hasSpecTypeOption(opts []string) bool {
	for _, o := range opts {
		if strings.HasPrefix(o, "spec_type:") || strings.HasPrefix(o, "speculative_type:") {
			return true
		}
	}
	return false
}

// ApplyMTPDefaults appends the auto-MTP option keys to cfg.Options when none
// is already configured. It is a no-op when the user already picked a
// `spec_type` (either via YAML or via the importer's preferences flow).
//
// `layers` is the value read from `<arch>.nextn_predict_layers` and is only
// used for the diagnostic log line.
func ApplyMTPDefaults(cfg *ModelConfig, layers uint32) {
	if cfg == nil {
		return
	}
	if hasSpecTypeOption(cfg.Options) {
		xlog.Debug("[mtp] embedded MTP head detected but spec_type already configured; leaving user choice intact",
			"name", cfg.Name, "nextn_layers", layers)
		return
	}
	cfg.Options = append(cfg.Options, mtpSpecOptions...)
	xlog.Info("[mtp] embedded MTP head detected; enabling draft-mtp speculative decoding",
		"name", cfg.Name, "nextn_layers", layers, "spec_n_max", 6, "spec_p_min", 0.75)
}
