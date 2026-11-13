// SPDX-License-Identifier: MIT
package gallery

import (
	"context"
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	gguf "github.com/gpustack/gguf-parser-go"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/vram"
	"github.com/mudler/xlog"
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

// recommendedModelsYAML is the fallback list, see BuiltinRecommendedModels.
//
//go:embed recommended_models.yaml
var recommendedModelsYAML []byte

// RecommendedModel is one curated entry: a gallery model, the name to show and
// a one-line reason it is on the list.
type RecommendedModel struct {
	// Rank is "default" or "alternative".
	Rank string `yaml:"rank" json:"rank"`
	// ID is the name of the model in the gallery index.
	ID          string `yaml:"id" json:"id"`
	DisplayName string `yaml:"name" json:"name"`
	Reason      string `yaml:"reason" json:"reason"`
}

// Ranks of an entry in its tier.
const (
	RankDefault     = "default"
	RankAlternative = "alternative"
)

// RecommendedTier is the choice for one tier: a default and two alternatives.
type RecommendedTier struct {
	Default      RecommendedModel
	Alternatives []RecommendedModel
}

// Models returns the default first, then the alternatives in file order.
func (t RecommendedTier) Models() []RecommendedModel {
	return append([]RecommendedModel{t.Default}, t.Alternatives...)
}

// RecommendedSchemaVersion is the version of the file format this code reads.
const RecommendedSchemaVersion = 1

// RecommendedList is the curated list of models, per hardware tier.
type RecommendedList struct {
	Tiers map[string]RecommendedTier
}

type recommendedFile struct {
	Version int                           `yaml:"version"`
	Tiers   map[string][]RecommendedModel `yaml:"tiers"`
}

var (
	builtinOnce sync.Once
	builtinList RecommendedList
	builtinErr  error
)

// BuiltinRecommendedModels returns the list that ships in the binary, the
// fallback when the served file is missing or invalid.
func BuiltinRecommendedModels() (RecommendedList, error) {
	builtinOnce.Do(func() {
		builtinList, builtinErr = ParseRecommendedModels(recommendedModelsYAML)
	})
	return builtinList, builtinErr
}

// ParseRecommendedModels reads and checks a curated list: a schema version this
// code knows, every tier of RecommendedTiers with exactly one default and two
// alternatives, and no model named twice in a tier. Checking against the
// gallery index is left to the caller, which has it.
func ParseRecommendedModels(data []byte) (RecommendedList, error) {
	var f recommendedFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return RecommendedList{}, fmt.Errorf("parse recommended models: %w", err)
	}
	if f.Version != RecommendedSchemaVersion {
		return RecommendedList{}, fmt.Errorf("recommended models: schema version %d is not supported, want %d", f.Version, RecommendedSchemaVersion)
	}
	for name := range f.Tiers {
		if !slices.Contains(RecommendedTiers, name) {
			return RecommendedList{}, fmt.Errorf("recommended models: unknown tier %q", name)
		}
	}
	list := RecommendedList{Tiers: map[string]RecommendedTier{}}
	for _, name := range RecommendedTiers {
		entries, ok := f.Tiers[name]
		if !ok {
			return RecommendedList{}, fmt.Errorf("recommended models: tier %q is missing", name)
		}
		var tier RecommendedTier
		defaults := 0
		seen := map[string]bool{}
		for _, m := range entries {
			if m.ID == "" || m.DisplayName == "" || m.Reason == "" {
				return RecommendedList{}, fmt.Errorf("recommended models: tier %q has an entry without id, name or reason", name)
			}
			if seen[m.ID] {
				return RecommendedList{}, fmt.Errorf("recommended models: tier %q names %q twice", name, m.ID)
			}
			seen[m.ID] = true
			switch m.Rank {
			case RankDefault:
				defaults++
				tier.Default = m
			case RankAlternative:
				tier.Alternatives = append(tier.Alternatives, m)
			default:
				return RecommendedList{}, fmt.Errorf("recommended models: tier %q: rank %q of %q is not %q or %q", name, m.Rank, m.ID, RankDefault, RankAlternative)
			}
		}
		if defaults != 1 || len(tier.Alternatives) != 2 {
			return RecommendedList{}, fmt.Errorf("recommended models: tier %q has %d defaults and %d alternatives, want 1 and 2", name, defaults, len(tier.Alternatives))
		}
		list.Tiers[name] = tier
	}
	return list, nil
}

// recommendedFileName is the file served next to a gallery index.
const recommendedFileName = "recommended.yaml"

var indexFileSuffix = regexp.MustCompile(`index\.yaml(@[^/]*)?$`)

// recommendedSibling returns the URL of the recommended models file next to a
// gallery index URL, or "" when the URL does not name an index file (a bare
// endpoint, an OCI artifact) and so has no sibling.
func recommendedSibling(indexURL string) string {
	if looksLikeOCIGallery(indexURL) || !indexFileSuffix.MatchString(indexURL) {
		return ""
	}
	return indexFileSuffix.ReplaceAllString(indexURL, recommendedFileName+"$1")
}

// RecommendedSource says where a list came from.
const RecommendedSourceBuiltin = "builtin"

// LoadRecommendedModels returns the curated list to use. It asks each gallery,
// in order, for the recommended models file next to its index, through the same
// fetch as the index (mirrors, failure cooldown), and takes the first file that
// is valid. When none is, it returns the list built into the binary and says why
// in the log. The second result names the source: a gallery name, or
// RecommendedSourceBuiltin.
//
// The file is not signed with the index: it can only choose among models the
// index lists, whose entries are what is verified and installed, and it is
// checked before it is used. With strict integrity on, a file that cannot be
// verified is not read at all.
func LoadRecommendedModels(ctx context.Context, galleries []config.Gallery, basePath string, requireIntegrity bool) (RecommendedList, string) {
	fallback := func(reason string, err error) (RecommendedList, string) {
		list, ferr := BuiltinRecommendedModels()
		if ferr != nil {
			// The embedded file is checked by a test; an empty list is the safe answer
			// to a broken build.
			xlog.Error("the built-in recommended models are invalid", "error", ferr)
			return RecommendedList{}, RecommendedSourceBuiltin
		}
		xlog.Info("using the built-in recommended models", "reason", reason, "error", err)
		return list, RecommendedSourceBuiltin
	}
	if requireIntegrity {
		return fallback("strict integrity is on and the file is not signed", nil)
	}
	var lastErr error
	tried := false
	for _, g := range galleries {
		if g.Library || strings.HasSuffix(g.URL, ".ref") {
			continue
		}
		var urls []string
		for _, candidate := range galleryCandidates(g) {
			if s := recommendedSibling(candidate); s != "" {
				urls = append(urls, s)
			}
		}
		if len(urls) == 0 {
			continue
		}
		tried = true
		sibling := config.Gallery{Name: g.Name + "-recommended", URL: urls[0], Mirrors: urls[1:]}
		fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		body, servedBy, err := fetchGalleryIndex(fetchCtx, sibling, basePath, false)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		list, err := ParseRecommendedModels(body)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", servedBy, err)
			continue
		}
		return list, g.Name
	}
	if !tried {
		return fallback("no gallery has an index file to take the list from", nil)
	}
	return fallback("the file could not be fetched or is not valid", lastErr)
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

// MemoryModelReader reads what the planner needs to know about a gallery entry.
// It is the seam between the recommendation and where the numbers come from:
// ReadGGUFMemoryModel reads them from the header of the weights, and a reader of
// figures published in the gallery entries can replace it.
type MemoryModelReader func(ctx context.Context, m *GalleryModel) (config.MemoryModel, error)

// ReadGGUFMemoryModel reads the GGUF header of a gallery entry from its remote
// file. Only the header is fetched, with ranged requests: never the weights.
func ReadGGUFMemoryModel(ctx context.Context, m *GalleryModel) (model config.MemoryModel, err error) {
	uri := RecommendedWeightURI(m)
	if uri == "" {
		return nil, fmt.Errorf("model %q has no GGUF file", m.Name)
	}
	// The parser has panicked on lengths a file can supply; the file is remote
	// and the caller is a request handler.
	defer func() {
		if r := recover(); r != nil {
			model, err = nil, fmt.Errorf("read GGUF header: parser panic: %v", r)
		}
	}()
	f, err := gguf.ParseGGUFFileRemote(ctx, uri, gguf.SkipLargeMetadata())
	if err != nil {
		return nil, fmt.Errorf("read GGUF header of %q: %w", m.Name, err)
	}
	profile, err := config.NewModelProfile(f)
	if err != nil {
		return nil, err
	}
	return profile, nil
}
