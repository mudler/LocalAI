package gallery

import (
	"fmt"

	"github.com/mudler/LocalAI/core/config"
)

// GalleryModel is the struct used to represent a model in the gallery returned by the endpoint.
// It is used to install the model by resolving the URL and downloading the files.
// The other fields are used to override the configuration of the model.
type GalleryModel struct {
	Metadata `json:",inline" yaml:",inline"`
	// config_file is read in the situation where URL is blank - and therefore this is a base config.
	ConfigFile map[string]interface{} `json:"config_file,omitempty" yaml:"config_file,omitempty"`
	// Overrides are used to override the configuration of the model located at URL
	Overrides map[string]interface{} `json:"overrides,omitempty" yaml:"overrides,omitempty"`
}

func (m *GalleryModel) SetGallery(gallery config.Gallery) {
	m.Gallery = gallery
}

func (m *GalleryModel) SetInstalled(installed bool) {
	m.Installed = installed
}

func (m *GalleryModel) GetName() string {
	return m.Name
}

func (m *GalleryModel) GetGallery() config.Gallery {
	return m.Gallery
}

func (m GalleryModel) ID() string {
	return fmt.Sprintf("%s@%s", m.Gallery.Name, m.Name)
}

func (m *GalleryModel) GetTags() []string {
	return m.Tags
}

func (m *GalleryModel) GetDescription() string {
	return m.Description
}
