package gallery

import (
	"fmt"

	"github.com/mudler/LocalAI/pkg/vram"
)

// Candidate is one option in a gallery entry's ordered candidate list. It
// references an existing gallery entry by name and declares the conditions
// under which that entry is the right choice for the host.
type Candidate struct {
	// Model is the name of a gallery entry that declares no candidates of its
	// own, or the name of the declaring entry itself when it stands as its own
	// last-resort candidate.
	Model string `json:"model" yaml:"model"`
	// Capability, when set, must equal the host's reported capability
	// (e.g. "metal", "nvidia-cuda-12"). Empty matches any host.
	Capability string `json:"capability,omitempty" yaml:"capability,omitempty"`
	// MinVRAM is the authored VRAM floor (e.g. "20GiB"). Authoritative:
	// inference never overwrites it.
	MinVRAM string `json:"min_vram,omitempty" yaml:"min_vram,omitempty"`

	// The fields below are denormalized by the nightly job for display and
	// lint. They are never authored by hand and never affect what gets
	// installed, because installation reads the referenced entry live.
	Backend         string `json:"backend,omitempty" yaml:"backend,omitempty"`
	Quantization    string `json:"quantization,omitempty" yaml:"quantization,omitempty"`
	InferredMinVRAM string `json:"inferred_min_vram,omitempty" yaml:"inferred_min_vram,omitempty"`
}

// EffectiveMinVRAM returns the VRAM floor in bytes for this candidate and
// whether a floor is declared at all. An authored MinVRAM wins over an
// inferred one. A candidate with no floor declares no requirement and always
// passes the VRAM check.
func (c Candidate) EffectiveMinVRAM() (uint64, bool, error) {
	raw := c.MinVRAM
	if raw == "" {
		raw = c.InferredMinVRAM
	}
	if raw == "" {
		return 0, false, nil
	}
	bytes, err := vram.ParseSizeString(raw)
	if err != nil {
		return 0, false, fmt.Errorf("candidate %q has an unparseable min_vram %q: %w", c.Model, raw, err)
	}
	return bytes, true, nil
}
