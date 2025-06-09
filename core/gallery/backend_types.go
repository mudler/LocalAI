package gallery

import "github.com/mudler/LocalAI/core/config"

type GalleryBackend struct {
	Metadata `json:",inline" yaml:",inline"`
	Alias    string `json:"alias,omitempty" yaml:"alias,omitempty"`
	URI      string `json:"uri,omitempty" yaml:"uri,omitempty"`
}

type GalleryBackends []*GalleryBackend

func (m *GalleryBackend) SetGallery(gallery config.Gallery) {
	m.Gallery = gallery
}

func (m *GalleryBackend) SetInstalled(installed bool) {
	m.Installed = installed
}

func (m *GalleryBackend) GetName() string {
	return m.Name
}

func (m *GalleryBackend) GetGallery() config.Gallery {
	return m.Gallery
}

func (m *GalleryBackend) GetDescription() string {
	return m.Description
}

func (m *GalleryBackend) GetTags() []string {
	return m.Tags
}
