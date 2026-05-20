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

// GetKnownUsecases returns the usecase flags declared by the gallery entry,
// falling back to the resolved backend's default usecases when the entry has
// none of its own. Returns nil only when neither source provides any.
//
// Why the fallback: many gallery entries omit known_usecases because their
// backend has only one sensible mode (e.g. stablediffusion-ggml is always
// image generation). Without this fallback such models silently disappear
// from usecase-based filtering in the UI.
func (m *GalleryModel) GetKnownUsecases() *config.ModelConfigUsecase {
	if strs := overrideUsecaseStrings(m.Overrides); len(strs) > 0 {
		return config.GetUsecasesFromYAML(strs)
	}
	if defaults := config.DefaultUsecasesForBackendCap(m.Backend); len(defaults) > 0 {
		return config.GetUsecasesFromYAML(defaults)
	}
	return nil
}

func overrideUsecaseStrings(overrides map[string]any) []string {
	raw, ok := overrides["known_usecases"]
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
	return strs
}
