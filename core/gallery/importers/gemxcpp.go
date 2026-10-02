// SPDX-License-Identifier: MIT
package importers

import (
	"encoding/json"
	"fmt"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"go.yaml.in/yaml/v2"
	"strings"
)

type GemXCppImporter struct{}

func (*GemXCppImporter) Name() string      { return "gemxcpp" }
func (*GemXCppImporter) Modality() string  { return "motion" }
func (*GemXCppImporter) AutoDetects() bool { return true }
func gemxRepository(uri string) bool {
	owner, repo, ok := HFOwnerRepoFromURI(uri)
	return ok && strings.EqualFold(owner+"/"+repo, "LocalAI-io/GEM-X-GGUF")
}
func (*GemXCppImporter) Match(d Details) bool {
	var p struct {
		Backend string `json:"backend"`
	}
	if len(d.Preferences) > 0 {
		if json.Unmarshal(d.Preferences, &p) != nil {
			return false
		}
	}
	if p.Backend != "" {
		return p.Backend == "gemxcpp"
	}
	return gemxRepository(d.URI)
}
func (*GemXCppImporter) Import(d Details) (gallery.ModelConfig, error) {
	var p struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if len(d.Preferences) > 0 {
		if err := json.Unmarshal(d.Preferences, &p); err != nil {
			return gallery.ModelConfig{}, err
		}
	}
	if !gemxRepository(d.URI) {
		return gallery.ModelConfig{}, fmt.Errorf("gemxcpp requires the published LocalAI-io/GEM-X-GGUF bundle")
	}
	if p.Name == "" {
		p.Name = "gem-x"
	}
	if p.Description == "" {
		p.Description = "Live human motion capture: SOMA-77 or SMPL-24 poses on CPU/Vulkan"
	}
	files := []gallery.File{}
	for _, f := range []struct{ name, sha string }{
		{"gem-x-contact-f32.gguf", "175857b8453b65d028f3703d813c3186b31b66ea65fbdb94e64308c39e083f78"},
		{"vitpose-f32.gguf", "272c75d4c3a6a740f1eb3f3222832de206150fa035c134af17e66ca9f8386d11"},
		{"yolox-f32.gguf", "2be3d28e0dd8a171f4ad980b3dfbddaa1ecb88e478c612bc11379913908e7b78"},
	} {
		files = append(files, gallery.File{Filename: "gem-x/" + f.name, URI: "https://huggingface.co/LocalAI-io/GEM-X-GGUF/resolve/b36180fd4c0d7c6a7fb2269348f632d3ed279868/" + f.name, SHA256: f.sha})
	}
	cfg := config.ModelConfig{Name: p.Name, Description: p.Description, Backend: "gemxcpp", KnownUsecaseStrings: []string{"motion"}, Options: []string{"vitpose:gem-x/vitpose-f32.gguf", "yolox:gem-x/yolox-f32.gguf", "selection:continuity", "window:30", "detector_interval:1"}, PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "gem-x/gem-x-contact-f32.gguf"}}}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	return gallery.ModelConfig{Name: p.Name, Description: p.Description, Files: files, ConfigFile: string(data)}, nil
}
