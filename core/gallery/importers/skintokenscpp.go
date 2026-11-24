// SPDX-License-Identifier: MIT
package importers

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"go.yaml.in/yaml/v2"
)

//go:embed skintokens_files.json
var skinTokensFiles []byte

type SkinTokensCppImporter struct{}

func (*SkinTokensCppImporter) Name() string      { return "skintokenscpp" }
func (*SkinTokensCppImporter) Modality() string  { return "3d_animation" }
func (*SkinTokensCppImporter) AutoDetects() bool { return true }

func skinTokensRepository(uri string) bool {
	owner, repo, ok := HFOwnerRepoFromURI(uri)
	return ok && strings.EqualFold(owner+"/"+repo, "LocalAI-io/SkinTokens-GGUF")
}

func (*SkinTokensCppImporter) Match(details Details) bool {
	var preferences struct {
		Backend string `json:"backend"`
	}
	if len(details.Preferences) > 0 {
		if err := json.Unmarshal(details.Preferences, &preferences); err != nil {
			return false
		}
	}
	if preferences.Backend != "" {
		return preferences.Backend == "skintokenscpp"
	}
	return skinTokensRepository(details.URI)
}

func (*SkinTokensCppImporter) Import(details Details) (gallery.ModelConfig, error) {
	if !skinTokensRepository(details.URI) {
		return gallery.ModelConfig{}, fmt.Errorf("skintokenscpp: choose the published LocalAI-io/SkinTokens-GGUF bundle")
	}
	var preferences struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if len(details.Preferences) > 0 {
		if err := json.Unmarshal(details.Preferences, &preferences); err != nil {
			return gallery.ModelConfig{}, err
		}
	}
	if preferences.Name == "" {
		preferences.Name = "skintokens-f16"
	}
	if preferences.Description == "" {
		preferences.Description = "SkinTokens F16 native rigging, learned skinning weights and SOMA30 motion application"
	}
	var files []gallery.File
	if err := json.Unmarshal(skinTokensFiles, &files); err != nil {
		return gallery.ModelConfig{}, err
	}
	cfg := config.ModelConfig{
		Name: preferences.Name, Description: preferences.Description, Backend: "skintokenscpp",
		KnownUsecaseStrings: []string{"FLAG_3D_ANIMATION"},
		PredictionOptions:   schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "skintokens/F16"}},
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	return gallery.ModelConfig{Name: preferences.Name, Description: preferences.Description, Files: files, ConfigFile: string(data)}, nil
}
