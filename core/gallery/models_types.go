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
	// Candidates is an optional ordered list of hardware-gated UPGRADES over
	// this entry. The entry itself is always the last-resort candidate, so an
	// entry carrying candidates stays a complete, installable entry and older
	// LocalAI releases, which drop this key, install it exactly as before.
	Candidates []Candidate `json:"candidates,omitempty" yaml:"candidates,omitempty"`
	// Capability, when set, is the host capability this entry's own payload
	// prefers. It is advisory for the base entry: an unmet capability warns
	// rather than refusing, because the base always installs.
	Capability string `json:"capability,omitempty" yaml:"capability,omitempty"`
	// MinVRAM is this entry's own VRAM floor, in the same form as a
	// candidate's (e.g. "2GiB"). It positions the entry within its own
	// candidate list and is likewise advisory: falling short only warns.
	MinVRAM string `json:"min_vram,omitempty" yaml:"min_vram,omitempty"`
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

// HasCandidates reports whether this entry declares hardware-gated upgrades
// over its own payload. It says nothing about the entry being installable:
// an entry with candidates is a complete entry that can always be installed
// as-is.
func (m GalleryModel) HasCandidates() bool {
	return len(m.Candidates) > 0
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
