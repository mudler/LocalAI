package gallery

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNoCandidateMatch is returned when no candidate satisfies the host.
	ErrNoCandidateMatch = errors.New("no model variant matches this system")
	// ErrPinNotFound is returned when a pinned variant is absent from the list.
	ErrPinNotFound = errors.New("pinned model variant not found")
)

// ResolveEnv describes the host properties a candidate is matched against.
//
// It is a plain struct rather than a *SystemState so the resolver stays pure
// and testable across hardware shapes without touching the machine.
type ResolveEnv struct {
	// Capability is the raw value from SystemState.DetectedCapability().
	Capability string
	// VRAM is total available VRAM in bytes, already capped by the operator
	// VRAM budget because the cap is applied inside detection.
	VRAM uint64
}

// ResolveCandidate returns the first candidate the host satisfies, or the
// pinned candidate when pin is non-empty.
//
// Why first-match rather than best-match: the list order is the policy. It is
// authored deliberately and lint-checked, which keeps selection explainable
// (you can read the list top to bottom) and keeps fallback expressible without
// a scoring function nobody can predict.
func ResolveCandidate(candidates []Candidate, env ResolveEnv, pin string) (Candidate, error) {
	if pin != "" {
		for _, c := range candidates {
			if strings.EqualFold(c.Model, pin) {
				return c, nil
			}
		}
		return Candidate{}, fmt.Errorf("%w: %q is not among the %d variants of this model", ErrPinNotFound, pin, len(candidates))
	}

	reasons := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c.Capability != "" && c.Capability != env.Capability {
			reasons = append(reasons, fmt.Sprintf("%s requires capability %q", c.Model, c.Capability))
			continue
		}

		floor, declared, err := c.EffectiveMinVRAM()
		if err != nil {
			return Candidate{}, err
		}
		// A candidate with no declared floor states no requirement and
		// passes. Lint guarantees only the final last-resort candidate is
		// in that position for the official gallery.
		if declared && env.VRAM < floor {
			reasons = append(reasons, fmt.Sprintf("%s needs %s VRAM", c.Model, humanBytes(floor)))
			continue
		}

		return c, nil
	}

	return Candidate{}, fmt.Errorf(
		"%w: capability %q with %s VRAM available; candidates: %s",
		ErrNoCandidateMatch, env.Capability, humanBytes(env.VRAM), strings.Join(reasons, "; "),
	)
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
