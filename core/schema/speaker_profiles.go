// SPDX-License-Identifier: MIT

package schema

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// SpeakerEncoder identifies the exact encoder weights, not a model filename.
type SpeakerEncoder struct {
	Identity  string `json:"identity"`
	Dimension int    `json:"dimension"`
	// Family is the embedding space of the encoder. The server fills it from the
	// loaded encoder; exported profiles do not carry it and it is not matched.
	Family string `json:"family,omitempty"`
}

// SpeakerProfileInterval locates retained clean audio in the original recording,
// in seconds. It does not describe separated or synthesized audio.
type SpeakerProfileInterval struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// SpeakerProfile contains one sensitive voice vector per discovered speaker.
type SpeakerProfile struct {
	Speaker           int                      `json:"speaker"`
	CleanDuration     float64                  `json:"clean_duration"`
	Intervals         []SpeakerProfileInterval `json:"intervals"`
	UnavailableReason *string                  `json:"unavailable_reason"`
	Embedding         []float32                `json:"embedding,omitempty"`
}

// SpeakerProfiles is the versioned speaker_profiles object exported by parakeet.
// These unsigned profiles are not proof of identity or consent to enrollment.
type SpeakerProfiles struct {
	Version  int              `json:"version"`
	Encoder  SpeakerEncoder   `json:"encoder"`
	Speakers []SpeakerProfile `json:"speakers"`
}

var speakerEncoderIdentity = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Validate checks portable data against metadata from the server's loaded encoder.
// trusted must never come from the request itself. This does not authorize export
// or enrollment, verify provenance, or check intervals against recording length.
func (p SpeakerProfiles) Validate(trusted SpeakerEncoder) error {
	if p.Version != 1 {
		return fmt.Errorf("unsupported speaker profile version: %d", p.Version)
	}
	if !speakerEncoderIdentity.MatchString(trusted.Identity) || trusted.Dimension <= 0 {
		return fmt.Errorf("invalid trusted speaker encoder metadata")
	}
	if p.Encoder.Identity != trusted.Identity || p.Encoder.Dimension != trusted.Dimension {
		return fmt.Errorf("speaker profile encoder does not match loaded encoder")
	}
	seen := make(map[int]bool, len(p.Speakers))
	for _, s := range p.Speakers {
		if s.Speaker < 0 || seen[s.Speaker] {
			return fmt.Errorf("invalid or duplicate speaker slot: %d", s.Speaker)
		}
		seen[s.Speaker] = true
		if err := s.validate(trusted.Dimension); err != nil {
			return fmt.Errorf("speaker %d: %w", s.Speaker, err)
		}
	}
	return nil
}

// Select validates the complete export and returns a usable speaker for explicit
// enrollment. Callers must supply trusted loaded-encoder metadata, not p.Encoder.
func (p SpeakerProfiles) Select(speaker int, trusted SpeakerEncoder) (SpeakerProfile, error) {
	if err := p.Validate(trusted); err != nil {
		return SpeakerProfile{}, err
	}
	for _, s := range p.Speakers {
		if s.Speaker == speaker {
			if s.UnavailableReason != nil {
				return SpeakerProfile{}, fmt.Errorf("speaker %d is unavailable", speaker)
			}
			return s, nil
		}
	}
	return SpeakerProfile{}, fmt.Errorf("speaker %d not found", speaker)
}

func (s SpeakerProfile) validate(dimension int) error {
	// Native JSON rounds timestamps; allow a millisecond of serialization drift.
	const tolerance = 0.001
	if !finiteProfileNumber(s.CleanDuration) || s.CleanDuration < 0 || s.CleanDuration > 30+tolerance {
		return fmt.Errorf("invalid clean duration")
	}
	var duration, previousEnd float64
	for _, interval := range s.Intervals {
		if !finiteProfileNumber(interval.Start) || !finiteProfileNumber(interval.End) || interval.Start < previousEnd || interval.End <= interval.Start {
			return fmt.Errorf("invalid clean interval")
		}
		duration += interval.End - interval.Start
		previousEnd = interval.End
	}
	if !finiteProfileNumber(duration) || math.Abs(duration-s.CleanDuration) > tolerance {
		return fmt.Errorf("clean duration does not match intervals")
	}
	if s.UnavailableReason != nil {
		if strings.TrimSpace(*s.UnavailableReason) == "" || len(s.Embedding) != 0 {
			return fmt.Errorf("invalid unavailable profile")
		}
		return nil
	}
	if s.CleanDuration < 2-tolerance {
		return fmt.Errorf("insufficient clean speech")
	}
	if len(s.Embedding) != dimension {
		return fmt.Errorf("speaker embedding dimension mismatch")
	}
	var norm float64
	for _, value := range s.Embedding {
		v := float64(value)
		if !finiteProfileNumber(v) {
			return fmt.Errorf("speaker embedding must be finite")
		}
		norm += v * v
	}
	if norm == 0 {
		return fmt.Errorf("speaker embedding must be nonzero")
	}
	return nil
}

func finiteProfileNumber(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
