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
	// Variants is an optional, UNORDERED list of alternative builds of the same
	// model (other backends such as MLX or vLLM, other quantizations) that the
	// installer may pick instead of this entry's own payload. Authoring is
	// deliberately dumb: name the models, and the selector works out which one
	// this host should get.
	//
	// The entry itself is always the last resort, so an entry carrying variants
	// stays a complete, installable entry and older LocalAI releases, which drop
	// this key, install it exactly as before.
	Variants []Variant `json:"variants,omitempty" yaml:"variants,omitempty"`
}

// installsSomething reports whether this entry, combined with the caller's
// request, would put anything on disk.
//
// It exists to keep an authoring mistake loud. Once an entry with no url and no
// config_file is accepted as an empty base, the only thing separating a
// deliberate overrides-only entry from a half-written stanza is whether it
// carries a payload at all. Without this check the stub would install cleanly
// and leave a model directory holding a config naming no weights, which fails
// far from the entry that caused it.
//
// The request is counted because its overrides and files are merged into the
// install exactly as the entry's own are, so a caller supplying them really has
// asked for something installable.
//
// An entry with a url or a config_file never reaches this: it has a base config
// to install, however thin.
func (m *GalleryModel) installsSomething(req GalleryModel) bool {
	return len(m.Overrides) > 0 || len(m.AdditionalFiles) > 0 ||
		len(req.Overrides) > 0 || len(req.AdditionalFiles) > 0
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

// HasVariants reports whether this entry declares alternative builds of itself.
// It says nothing about the entry being installable: an entry with variants is
// a complete entry that can always be installed as-is.
func (m GalleryModel) HasVariants() bool {
	return len(m.Variants) > 0
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
