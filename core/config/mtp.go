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

// isDraftOnlyAssistantArch reports whether an architecture names a standalone
// MTP *draft* model rather than a self-speculating trunk. Upstream's Gemma4 MTP
// (ggml-org/llama.cpp#23398) registers the head as a separate `gemma4-assistant`
// architecture whose GGUF still carries `nextn_predict_layers`, but which cannot
// run alone: it requires a paired target context (`ctx_other`). Such archs must
// not trigger the embedded-head self-speculation defaults. The `-assistant`
// suffix is upstream's naming convention for these draft-only checkpoints.
func isDraftOnlyAssistantArch(arch string) bool {
	return strings.HasSuffix(arch, "-assistant")
}

// HasEmbeddedMTPHead reports whether the parsed GGUF declares a self-speculating
// Multi-Token Prediction head. Detection reads `<arch>.nextn_predict_layers`,
// which is what `gguf_writer.add_nextn_predict_layers(n)` emits in upstream's
// `conversion/qwen.py` MTP mixin. A positive layer count means the head is
// present in the same GGUF as the trunk.
//
// Draft-only assistant architectures (e.g. Gemma4's `gemma4-assistant`) carry
// the same key but are separate draft checkpoints meant to be paired with a
// target model, so they are deliberately excluded here.
func HasEmbeddedMTPHead(f *gguf.GGUFFile) (uint32, bool) {
	if f == nil {
		return 0, false
	}
	arch := f.Architecture().Architecture
	if arch == "" {
		return 0, false
	}
	if isDraftOnlyAssistantArch(arch) {
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
