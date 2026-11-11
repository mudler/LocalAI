// SPDX-License-Identifier: MIT
package gallery

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"sync"

	gguf "github.com/gpustack/gguf-parser-go"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/vram"
)

// The hardware tiers of the curated model list, smallest first.
const (
	TierCPU      = "cpu"
	TierGPUSmall = "gpu-small"
	TierGPUMid   = "gpu-mid"
	TierGPULarge = "gpu-large"
)

// RecommendedTiers is the order of the tiers, smallest hardware first.
var RecommendedTiers = []string{TierCPU, TierGPUSmall, TierGPUMid, TierGPULarge}

// Tier limits are in decimal gigabytes, the unit a card is sold in: an
// 8 GB card reports a little less than 8 GiB, and must still land in the tier
// of its box.
const (
	tierSmallLimit = 8_000_000_000
	tierMidLimit   = 24_000_000_000
)

// TierForMemory picks the tier of a host from its per-device VRAM, 0 meaning no
// GPU: under 8 GB is gpu-small, under 24 GB gpu-mid, and gpu-large above.
func TierForMemory(perDeviceVRAM uint64) string {
	switch {
	case perDeviceVRAM == 0:
		return TierCPU
	case perDeviceVRAM < tierSmallLimit:
		return TierGPUSmall
	case perDeviceVRAM < tierMidLimit:
		return TierGPUMid
	default:
		return TierGPULarge
	}
}

//go:embed recommended_models.yaml
var recommendedModelsYAML []byte

// RecommendedModel is one curated entry: a gallery model, the name to show and
// a one-line reason it is on the list.
type RecommendedModel struct {
	// ID is the name of the model in the gallery index.
	ID          string `yaml:"id" json:"id"`
	DisplayName string `yaml:"name" json:"name"`
	Reason      string `yaml:"reason" json:"reason"`
}

// RecommendedTier is the choice for one tier: a default and two alternatives.
type RecommendedTier struct {
	Default      RecommendedModel   `yaml:"default" json:"default"`
	Alternatives []RecommendedModel `yaml:"alternatives" json:"alternatives"`
}

// Models returns the default first, then the alternatives.
func (t RecommendedTier) Models() []RecommendedModel {
	return append([]RecommendedModel{t.Default}, t.Alternatives...)
}

// RecommendedList is the curated list of models, per hardware tier.
type RecommendedList struct {
	Version int                        `yaml:"version"`
	Tiers   map[string]RecommendedTier `yaml:"tiers"`
}

var (
	recommendedOnce sync.Once
	recommendedList RecommendedList
	recommendedErr  error
)

// RecommendedModels returns the curated list that ships in the binary.
func RecommendedModels() (RecommendedList, error) {
	recommendedOnce.Do(func() {
		recommendedList, recommendedErr = ParseRecommendedModels(recommendedModelsYAML)
	})
	return recommendedList, recommendedErr
}

// ParseRecommendedModels reads and checks a curated list: every tier of
// RecommendedTiers has one default and two alternatives, and no model is named
// twice in a tier.
func ParseRecommendedModels(data []byte) (RecommendedList, error) {
	var l RecommendedList
	if err := yaml.Unmarshal(data, &l); err != nil {
		return RecommendedList{}, fmt.Errorf("parse recommended models: %w", err)
	}
	for _, name := range RecommendedTiers {
		tier, ok := l.Tiers[name]
		if !ok {
			return RecommendedList{}, fmt.Errorf("recommended models: tier %q is missing", name)
		}
		if len(tier.Alternatives) != 2 {
			return RecommendedList{}, fmt.Errorf("recommended models: tier %q has %d alternatives, want 2", name, len(tier.Alternatives))
		}
		seen := map[string]bool{}
		for _, m := range tier.Models() {
			if m.ID == "" || m.DisplayName == "" || m.Reason == "" {
				return RecommendedList{}, fmt.Errorf("recommended models: tier %q has an entry without id, name or reason", name)
			}
			if seen[m.ID] {
				return RecommendedList{}, fmt.Errorf("recommended models: tier %q names %q twice", name, m.ID)
			}
			seen[m.ID] = true
		}
	}
	for name := range l.Tiers {
		if !strings.Contains(strings.Join(RecommendedTiers, ","), name) {
			return RecommendedList{}, fmt.Errorf("recommended models: unknown tier %q", name)
		}
	}
	return l, nil
}

// RecommendedWeightURI is the URL of the file whose header describes the model
// of a gallery entry: its first GGUF file that is not a multimodal projector.
// It returns "" when the entry has none, as a model that is not a GGUF has no
// header to read.
func RecommendedWeightURI(m *GalleryModel) string {
	for _, f := range m.AdditionalFiles {
		if vram.IsGGUF(f.URI) && !strings.Contains(strings.ToLower(f.Filename), "mmproj") {
			return downloader.URI(f.URI).ResolveURL()
		}
	}
	return ""
}

// ReadModelProfile reads the GGUF header of a gallery entry from its remote
// file. Only the header is fetched, with ranged requests: never the weights.
func ReadModelProfile(ctx context.Context, m *GalleryModel) (profile config.ModelProfile, err error) {
	uri := RecommendedWeightURI(m)
	if uri == "" {
		return config.ModelProfile{}, fmt.Errorf("model %q has no GGUF file", m.Name)
	}
	// The parser has panicked on lengths a file can supply; the file is remote
	// and the caller is a request handler.
	defer func() {
		if r := recover(); r != nil {
			profile, err = config.ModelProfile{}, fmt.Errorf("read GGUF header: parser panic: %v", r)
		}
	}()
	f, err := gguf.ParseGGUFFileRemote(ctx, uri, gguf.SkipLargeMetadata())
	if err != nil {
		return config.ModelProfile{}, fmt.Errorf("read GGUF header of %q: %w", m.Name, err)
	}
	return config.NewModelProfile(f)
}
