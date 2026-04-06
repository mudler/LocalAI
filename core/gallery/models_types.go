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
	ConfigFile map[string]any `json:"config_file,omitempty" yaml:"config_file,omitempty"`
	// Overrides are used to override the configuration of the model located at URL
	Overrides map[string]any `json:"overrides,omitempty" yaml:"overrides,omitempty"`
}

func (m *GalleryModel) GetInstalled() bool {
	return m.Installed
}

func (m *GalleryModel) GetLicense() string {
	return m.License
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

// GetKnownUsecases extracts known_usecases from the model's Overrides and
// returns the parsed usecase flags. Returns nil when no usecases are declared.
func (m *GalleryModel) GetKnownUsecases() *config.ModelConfigUsecase {
	raw, ok := m.Overrides["known_usecases"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	strs := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok {
			strs = append(strs, s)
		}
	}
	return config.GetUsecasesFromYAML(strs)
}
