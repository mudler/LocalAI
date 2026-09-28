// SPDX-License-Identifier: MIT
package importers

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"go.yaml.in/yaml/v2"
)

// Inventory from raven38/pixal3d.cpp d1b4926452f9e09702b891db5f05acc845e153ed (MIT).
//
//go:embed pixal3d_models.json
var pixal3dManifest []byte

type Pixal3DImporter struct{}

func (*Pixal3DImporter) Name() string      { return "pixal3dcpp" }
func (*Pixal3DImporter) Modality() string  { return "3d" }
func (*Pixal3DImporter) AutoDetects() bool { return false }
func (*Pixal3DImporter) Match(d Details) bool {
	var p struct {
		Backend string `json:"backend"`
	}
	return json.Unmarshal(d.Preferences, &p) == nil && p.Backend == "pixal3dcpp"
}
func (*Pixal3DImporter) Import(d Details) (gallery.ModelConfig, error) {
	const directory = "pixal3d-q8_0-v1"
	owner, repo, ok := HFOwnerRepoFromURI(d.URI)
	if !ok || owner != "raven38" || repo != directory {
		return gallery.ModelConfig{}, fmt.Errorf("pixal3dcpp importer supports https://huggingface.co/raven38/pixal3d-q8_0-v1; configure other complete MV directories manually")
	}
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(d.Preferences, &p); err != nil {
		return gallery.ModelConfig{}, err
	}
	if p.Name == "" {
		p.Name = "pixal3d"
	}
	var manifest struct {
		Files []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(pixal3dManifest, &manifest); err != nil {
		return gallery.ModelConfig{}, err
	}
	result := gallery.ModelConfig{Name: p.Name, Description: "Pixal3D multiview Q8_0: four RGBA views to GLB"}
	for _, file := range manifest.Files {
		result.Files = append(result.Files, gallery.File{URI: "https://huggingface.co/raven38/" + directory + "/resolve/main/" + file.Name, Filename: directory + "/" + file.Name, SHA256: file.SHA256})
	}
	result.Files = append(result.Files, gallery.File{URI: "https://raw.githubusercontent.com/raven38/pixal3d.cpp/d1b4926452f9e09702b891db5f05acc845e153ed/models/" + directory + "/pixal3d-models.json", Filename: directory + "/pixal3d-models.json", SHA256: fmt.Sprintf("%x", sha256.Sum256(pixal3dManifest))})
	cfg := config.ModelConfig{Name: p.Name, Backend: "pixal3dcpp", KnownUsecaseStrings: []string{"FLAG_3D"}, PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: directory}}}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return gallery.ModelConfig{}, err
	}
	result.ConfigFile = string(data)
	return result, nil
}
