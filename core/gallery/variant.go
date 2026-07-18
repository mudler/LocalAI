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
	// override the size the installer probes live from the model's weights when
	// that figure is known to be wrong; most variants should not need it.
	//
	// One number covers both VRAM and system RAM. A model's footprint is
	// roughly the same wherever it lives, and the selector compares this against
	// whichever of the two this host will actually use, so splitting it in two
	// would only invite the pair to disagree.
	MinMemory string `json:"min_memory,omitempty" yaml:"min_memory,omitempty"`
}

// AuthoredMinMemory returns the hand-written memory requirement in bytes and
// whether one was written at all.
//
// An absent requirement is not a zero requirement. Callers must not read the
// returned 0 as "fits anywhere"; it means the author said nothing, and the
// figure has to come from a live probe instead.
func (v Variant) AuthoredMinMemory() (uint64, bool, error) {
	if v.MinMemory == "" {
		return 0, false, nil
	}
	size, err := vram.ParseSizeString(v.MinMemory)
	if err != nil {
		return 0, false, fmt.Errorf("variant %q has an unparseable min_memory %q: %w", v.Model, v.MinMemory, err)
	}
	return size, true, nil
}
