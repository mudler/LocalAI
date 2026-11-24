package importers

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"go.yaml.in/yaml/v2"
)

var _ Importer = &Trellis2CppImporter{}

// trellis2File describes one component of the TRELLIS.2 GGUF set hosted on
// the LocalAI-io HuggingFace org. The pipeline spans three source repos
// (TRELLIS.2-4B, TRELLIS-image-large for the SS decoder, and a DINOv3
// mirror), so a single import URI always expands to this full set — no one
// repo can describe it alone. Filenames follow the trellis2cpp converter
// defaults, which the backend resolves without any options.
type trellis2File struct {
	filename string
	uri      string
	sha256   string
}

var trellis2Files = []trellis2File{
	{"dino_f16.gguf", "https://huggingface.co/LocalAI-io/dinov3-vitl16-pretrain-lvd1689m-GGUF/resolve/main/dino_f16.gguf", "385d8186a38a2328ec740fb2ac1f33f9194d8774efc7ccafd4aa2e51cf5f6450"},
	{"ss_flow_f16.gguf", "https://huggingface.co/LocalAI-io/TRELLIS.2-4B-GGUF/resolve/main/ss_flow_f16.gguf", "1dded5b74237d24e6876a642a26f90b43742e3554418573860f810e3bbe61e8c"},
	{"ss_dec_f16.gguf", "https://huggingface.co/LocalAI-io/TRELLIS-image-large-GGUF/resolve/main/ss_dec_f16.gguf", "9c2210b7ed830fdc8286961a8189878ff5bcfd3bfc83ab4eacee005d293d2185"},
	{"slat_flow_f16.gguf", "https://huggingface.co/LocalAI-io/TRELLIS.2-4B-GGUF/resolve/main/slat_flow_f16.gguf", "2f94bad7b1c524ad8c01943bc38fcc0c314e7d482ce896f3c6e96eb6e7cec15c"},
	{"slat_flow_1024_f16.gguf", "https://huggingface.co/LocalAI-io/TRELLIS.2-4B-GGUF/resolve/main/slat_flow_1024_f16.gguf", "b6a2270131e2e9235e9b6cb525193eb85ae132fa5af3274322aacd39e40a6bc5"},
	{"shape_dec_f16.gguf", "https://huggingface.co/LocalAI-io/TRELLIS.2-4B-GGUF/resolve/main/shape_dec_f16.gguf", "6fe53f1d7763dabf7c8d72bc38f4053d87fde6f65bf17a9d378d27edb39d3530"},
	{"shape_enc_f16.gguf", "https://huggingface.co/LocalAI-io/TRELLIS.2-4B-GGUF/resolve/main/shape_enc_f16.gguf", "3ec80ff580987fcdb9bc594fc8b6fda890d63101ca442eb2b26f5dc315e8696c"},
	{"tex_dec_f16.gguf", "https://huggingface.co/LocalAI-io/TRELLIS.2-4B-GGUF/resolve/main/tex_dec_f16.gguf", "afd304f4dfcb8c94df851b85519b415b99f04070f7d29de1320c50631b1be4e0"},
	{"tex_slat_flow_512_f16.gguf", "https://huggingface.co/LocalAI-io/TRELLIS.2-4B-GGUF/resolve/main/tex_slat_flow_512_f16.gguf", "89a081b7f5487a5b31f03d240e4d959a56db0cc2c46c327230097a2554da52ae"},
	{"tex_slat_flow_1024_f16.gguf", "https://huggingface.co/LocalAI-io/TRELLIS.2-4B-GGUF/resolve/main/tex_slat_flow_1024_f16.gguf", "bbb55b0910c7929aac5e0612a9bb15113837a2c674cafb9f0f170eda8b5558a8"},
}

// trellis2ComponentNames are the distinctive default component filenames. A
// raw .gguf URL with one of these basenames is a strong trellis2 signal.
// dino_f16.gguf is deliberately absent — DINO checkpoints are common enough
// that the bare name would over-claim.
var trellis2ComponentNames = map[string]struct{}{
	"ss_flow_f16.gguf":            {},
	"ss_dec_f16.gguf":             {},
	"slat_flow_f16.gguf":          {},
	"slat_flow_1024_f16.gguf":     {},
	"shape_dec_f16.gguf":          {},
	"shape_enc_f16.gguf":          {},
	"tex_dec_f16.gguf":            {},
	"tex_slat_flow_512_f16.gguf":  {},
	"tex_slat_flow_1024_f16.gguf": {},
}

// Trellis2CppImporter recognises Microsoft TRELLIS.2 image-to-3D GGUF sets
// (the trellis2.cpp converter outputs hosted under LocalAI-io). It must be
// registered BEFORE LlamaCPPImporter so llama-cpp does not steal the .gguf
// match. preferences.backend="trellis2cpp" overrides detection.
type Trellis2CppImporter struct{}

func (i *Trellis2CppImporter) Name() string      { return "trellis2cpp" }
func (i *Trellis2CppImporter) Modality() string  { return "3d" }
func (i *Trellis2CppImporter) AutoDetects() bool { return true }

// containsTrellisToken reports whether s (compared case-insensitively)
// carries a TRELLIS marker ("trellis" covers TRELLIS.2 / trellis2 too).
func containsTrellisToken(s string) bool {
	return strings.Contains(strings.ToLower(s), "trellis")
}

func (i *Trellis2CppImporter) Match(details Details) bool {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return false
	}
	preferencesMap := make(map[string]any)
	if len(preferences) > 0 {
		if err := json.Unmarshal(preferences, &preferencesMap); err != nil {
			return false
		}
	}

	if b, ok := preferencesMap["backend"].(string); ok && b != "" {
		return b == "trellis2cpp"
	}

	// Raw .gguf URL named after a distinctive pipeline component.
	if strings.HasSuffix(strings.ToLower(details.URI), ".gguf") {
		base := strings.ToLower(filepath.Base(details.URI))
		if _, ok := trellis2ComponentNames[base]; ok {
			return true
		}
	}

	// A trellis-named URI or HF repo carrying GGUFs.
	if containsTrellisToken(details.URI) {
		if strings.HasSuffix(strings.ToLower(details.URI), ".gguf") {
			return true
		}
		if details.HuggingFace != nil && hasGGUF(details.HuggingFace.Files) {
			return true
		}
		// HF details may be nil (tree-listing quirk) — decide from the
		// owner/repo alone.
		if _, repo, ok := HFOwnerRepoFromURI(details.URI); ok && containsTrellisToken(repo) {
			return true
		}
	}

	return false
}

func (i *Trellis2CppImporter) Import(details Details) (gallery.ModelConfig, error) {
	preferences, err := details.Preferences.MarshalJSON()
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	preferencesMap := make(map[string]any)
	if len(preferences) > 0 {
		if err := json.Unmarshal(preferences, &preferencesMap); err != nil {
			return gallery.ModelConfig{}, err
		}
	}

	name, ok := preferencesMap["name"].(string)
	if !ok {
		name = "trellis2-4b"
	}

	description, ok := preferencesMap["description"].(string)
	if !ok {
		description = "TRELLIS.2 image-to-3D (GLB with PBR textures) — imported from " + details.URI
	}

	cfg := gallery.ModelConfig{
		Name:        name,
		Description: description,
	}
	// The full pipeline spans three HF repos, so any trellis URI imports the
	// complete known-good set rather than whatever single repo was pasted.
	for _, f := range trellis2Files {
		cfg.Files = append(cfg.Files, gallery.File{
			URI:      f.uri,
			Filename: f.filename,
			SHA256:   f.sha256,
		})
	}

	modelConfig := config.ModelConfig{
		Name:                name,
		Description:         description,
		Backend:             "trellis2cpp",
		KnownUsecaseStrings: []string{"FLAG_3D"},
		PredictionOptions: schema.PredictionOptions{
			// ss_flow anchors the GGUF directory; the backend resolves the
			// other components from their default filenames next to it.
			BasicModelRequest: schema.BasicModelRequest{Model: "ss_flow_f16.gguf"},
		},
	}

	data, err := yaml.Marshal(modelConfig)
	if err != nil {
		return gallery.ModelConfig{}, err
	}

	cfg.ConfigFile = string(data)
	return cfg, nil
}
