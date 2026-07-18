package gallery

import (
	"fmt"

	"github.com/mudler/LocalAI/pkg/vram"
)

// Variant is one option in a gallery entry's variant list. It references an
// existing gallery entry by name, and that is all an author has to write.
//
// Authored order carries no meaning. Selection filters out what this host
// cannot run and then ranks what is left, so hardware knowledge lives in the
// selector rather than being pushed onto whoever edits the gallery.
type Variant struct {
	// Model is the name of a gallery entry that declares no variants of its own.
	Model string `json:"model" yaml:"model"`
	// MinMemory is the authored memory requirement (e.g. "20GiB"). It exists to
	// override an inferred estimate that is known to be wrong; most variants
	// should not need it.
	//
	// One number covers both VRAM and system RAM. A model's footprint is
	// roughly the same wherever it lives, and the selector compares this against
	// whichever of the two this host will actually use, so splitting it in two
	// would only invite the pair to disagree.
	MinMemory string `json:"min_memory,omitempty" yaml:"min_memory,omitempty"`

	// The fields below are denormalized by the nightly job for display and
	// lint. They are never authored by hand and never affect what gets
	// installed, because installation reads the referenced entry live.
	Backend           string `json:"backend,omitempty" yaml:"backend,omitempty"`
	Quantization      string `json:"quantization,omitempty" yaml:"quantization,omitempty"`
	InferredMinMemory string `json:"inferred_min_memory,omitempty" yaml:"inferred_min_memory,omitempty"`
}

// EffectiveMinMemory returns this variant's memory requirement in bytes and
// whether one is known at all. An authored MinMemory wins over an inferred one,
// because a human who measured a real load knows more than a pre-download
// estimate does.
//
// An unknown requirement is not a zero requirement. Callers must not read the
// returned 0 as "fits anywhere"; it means nothing is known either way.
func (v Variant) EffectiveMinMemory() (uint64, bool, error) {
	raw := v.MinMemory
	if raw == "" {
		raw = v.InferredMinMemory
	}
	if raw == "" {
		return 0, false, nil
	}
	size, err := vram.ParseSizeString(raw)
	if err != nil {
		return 0, false, fmt.Errorf("variant %q has an unparseable min_memory %q: %w", v.Model, raw, err)
	}
	return size, true, nil
}
