package gallery

import (
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/rs/zerolog/log"
)

// BackendMetadata represents the metadata stored in a JSON file for each installed backend
type BackendMetadata struct {
	// Alias is an optional alternative name for the backend
	Alias string `json:"alias,omitempty"`
	// MetaBackendFor points to the concrete backend if this is a meta backend
	MetaBackendFor string `json:"meta_backend_for,omitempty"`
	// Name is the original name from the gallery
	Name string `json:"name,omitempty"`
	// GalleryURL is the URL of the gallery this backend came from
	GalleryURL string `json:"gallery_url,omitempty"`
	// InstalledAt is the timestamp when the backend was installed
	InstalledAt string `json:"installed_at,omitempty"`
}

type GalleryBackend struct {
	Metadata        `json:",inline" yaml:",inline"`
	Alias           string            `json:"alias,omitempty" yaml:"alias,omitempty"`
	URI             string            `json:"uri,omitempty" yaml:"uri,omitempty"`
	Mirrors         []string          `json:"mirrors,omitempty" yaml:"mirrors,omitempty"`
	CapabilitiesMap map[string]string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

func (backend *GalleryBackend) FindBestBackendFromMeta(systemState *system.SystemState, backends GalleryElements[*GalleryBackend]) *GalleryBackend {
	if systemState == nil {
		return nil
	}

	realBackend := backend.CapabilitiesMap[systemState.Capability(backend.CapabilitiesMap)]
	if realBackend == "" {
		log.Debug().Str("backend", backend.Name).Str("reportedCapability", systemState.Capability(backend.CapabilitiesMap)).Msg("No backend found for reported capability")
		return nil
	}

	log.Debug().Str("backend", backend.Name).Str("reportedCapability", systemState.Capability(backend.CapabilitiesMap)).Msg("Found backend for reported capability")
	return backends.FindByName(realBackend)
}

type GalleryBackends []*GalleryBackend

func (m *GalleryBackend) SetGallery(gallery config.Gallery) {
	m.Gallery = gallery
}

func (m *GalleryBackend) IsMeta() bool {
	return len(m.CapabilitiesMap) > 0 && m.URI == ""
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

func (m GalleryBackend) ID() string {
	return fmt.Sprintf("%s@%s", m.Gallery.Name, m.Name)
}
