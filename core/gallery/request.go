package gallery

import (
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"
)

// GalleryModel is the struct used to represent a model in the gallery returned by the endpoint.
// It is used to install the model by resolving the URL and downloading the files.
// The other fields are used to override the configuration of the model.
type GalleryModel struct {
	URL         string   `json:"url,omitempty" yaml:"url,omitempty"`
	Name        string   `json:"name,omitempty" yaml:"name,omitempty"`
	Description string   `json:"description,omitempty"  yaml:"description,omitempty"`
	License     string   `json:"license,omitempty"  yaml:"license,omitempty"`
	URLs        []string `json:"urls,omitempty" yaml:"urls,omitempty"`
	Icon        string   `json:"icon,omitempty" yaml:"icon,omitempty"`
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	// config_file is read in the situation where URL is blank - and therefore this is a base config.
	ConfigFile map[string]interface{} `json:"config_file,omitempty" yaml:"config_file,omitempty"`
	// Overrides are used to override the configuration of the model located at URL
	Overrides map[string]interface{} `json:"overrides,omitempty" yaml:"overrides,omitempty"`
	// AdditionalFiles are used to add additional files to the model
	AdditionalFiles []File `json:"files,omitempty" yaml:"files,omitempty"`
	// Gallery is a reference to the gallery which contains the model
	Gallery config.Gallery `json:"gallery,omitempty" yaml:"gallery,omitempty"`
	// Installed is used to indicate if the model is installed or not
	Installed bool `json:"installed,omitempty" yaml:"installed,omitempty"`
}

func (m GalleryModel) ID() string {
	return fmt.Sprintf("%s@%s", m.Gallery.Name, m.Name)
}

type GalleryModels []*GalleryModel

func (gm GalleryModels) Search(term string) GalleryModels {
	var filteredModels GalleryModels

	for _, m := range gm {
		if strings.Contains(m.Name, term) ||
			strings.Contains(m.Description, term) ||
			strings.Contains(m.Gallery.Name, term) ||
			strings.Contains(strings.Join(m.Tags, ","), term) {
			filteredModels = append(filteredModels, m)
		}
	}
	return filteredModels
}

func (gm GalleryModels) FindByName(name string) *GalleryModel {
	for _, m := range gm {
		if strings.EqualFold(m.Name, name) {
			return m
		}
	}
	return nil
}
