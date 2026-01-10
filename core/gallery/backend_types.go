package gallery

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
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
		xlog.Debug("No backend found for reported capability", "backend", backend.Name, "reportedCapability", systemState.Capability(backend.CapabilitiesMap))
		return nil
	}

	xlog.Debug("Found backend for reported capability", "backend", backend.Name, "reportedCapability", systemState.Capability(backend.CapabilitiesMap))
	return backends.FindByName(realBackend)
}

func (m *GalleryBackend) GetInstalled() bool {
	return m.Installed
}

func (m *GalleryBackend) GetLicense() string {
	return m.License
}

type GalleryBackends []*GalleryBackend

func (m *GalleryBackend) SetGallery(gallery config.Gallery) {
	m.Gallery = gallery
}

func (m *GalleryBackend) IsMeta() bool {
	return len(m.CapabilitiesMap) > 0 && m.URI == ""
}

// IsCompatibleWith checks if the backend is compatible with the current system capability.
// For meta backends, it checks if any of the capabilities in the map match the system capability.
// For concrete backends, it infers compatibility from the backend name and URI.
func (m *GalleryBackend) IsCompatibleWith(systemState *system.SystemState) bool {
	if systemState == nil {
		return true
	}

	// Meta backends are compatible if the system capability matches one of the keys
	if m.IsMeta() {
		capability := systemState.Capability(m.CapabilitiesMap)
		_, exists := m.CapabilitiesMap[capability]
		return exists
	}

	// For concrete backends, infer compatibility from name and URI
	name := strings.ToLower(m.Name)
	uri := strings.ToLower(m.URI)
	combined := name + " " + uri

	// Check for darwin/macOS-specific backends (mlx, metal, darwin)
	isDarwinBackend := strings.Contains(combined, "darwin") ||
		strings.Contains(combined, "mlx") ||
		strings.Contains(combined, "metal")
	if isDarwinBackend && runtime.GOOS != "darwin" {
		return false
	}

	// Check for NVIDIA L4T-specific backends (arm64 Linux with NVIDIA GPU)
	// This must be checked before the general NVIDIA check as L4T backends
	// may also contain "cuda" or "nvidia" in their names
	isL4TBackend := strings.Contains(combined, "l4t")
	if isL4TBackend {
		if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" || systemState.GPUVendor != "nvidia" {
			return false
		}
		return true
	}

	// Check for NVIDIA/CUDA-specific backends (non-L4T)
	isNvidiaBackend := strings.Contains(combined, "cuda") ||
		strings.Contains(combined, "nvidia")
	if isNvidiaBackend {
		if systemState.GPUVendor != "nvidia" {
			return false
		}
	}

	// Check for AMD/ROCm-specific backends
	isAMDBackend := strings.Contains(combined, "rocm") ||
		strings.Contains(combined, "hip") ||
		strings.Contains(combined, "amd")
	if isAMDBackend {
		if systemState.GPUVendor != "amd" {
			return false
		}
	}

	// Check for Intel/SYCL-specific backends
	isIntelBackend := strings.Contains(combined, "sycl") ||
		strings.Contains(combined, "intel")
	if isIntelBackend {
		if systemState.GPUVendor != "intel" {
			return false
		}
	}

	// CPU backends are always compatible
	return true
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
