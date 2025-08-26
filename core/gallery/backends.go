// Package gallery provides installation and registration utilities for LocalAI backends,
// including meta-backend resolution based on system capabilities.
package gallery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	cp "github.com/otiai10/copy"
	"github.com/rs/zerolog/log"
)

const (
	metadataFile = "metadata.json"
	runFile      = "run.sh"
)

// backendCandidate represents an installed concrete backend option for a given alias
type backendCandidate struct {
	name    string
	runFile string
}

// readBackendMetadata reads the metadata JSON file for a backend
func readBackendMetadata(backendPath string) (*BackendMetadata, error) {
	metadataPath := filepath.Join(backendPath, metadataFile)

	// If metadata file doesn't exist, return nil (for backward compatibility)
	if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
		return nil, nil
	}

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata file %q: %v", metadataPath, err)
	}

	var metadata BackendMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata file %q: %v", metadataPath, err)
	}

	return &metadata, nil
}

// writeBackendMetadata writes the metadata JSON file for a backend
func writeBackendMetadata(backendPath string, metadata *BackendMetadata) error {
	metadataPath := filepath.Join(backendPath, metadataFile)

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %v", err)
	}

	if err := os.WriteFile(metadataPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata file %q: %v", metadataPath, err)
	}

	return nil
}

// InstallBackendFromGallery installs a backend from galleries.
// If the backend is a meta backend, it selects the best concrete backend using system capabilities.
func InstallBackendFromGallery(galleries []config.Gallery, systemState *system.SystemState, name string, downloadStatus func(string, string, string, float64), force bool) error {
	if !force {
		// check if we already have the backend installed
		backends, err := ListSystemBackends(systemState)
		if err != nil {
			return err
		}
		if backends.Exists(name) {
			return nil
		}
	}

	if name == "" {
		return fmt.Errorf("backend name is empty")
	}

	log.Debug().Interface("galleries", galleries).Str("name", name).Msg("Installing backend from gallery")

	backends, err := AvailableBackends(galleries, systemState)
	if err != nil {
		return err
	}

	backend := FindGalleryElement(backends, name)
	if backend == nil {
		return fmt.Errorf("no backend found with name %q", name)
	}

	if backend.IsMeta() {
		log.Debug().Interface("systemState", systemState).Str("name", name).Msg("Backend is a meta backend")

		// Then, let's try to find the best backend based on the capabilities map
		bestBackend := backend.FindBestBackendFromMeta(systemState, backends)
		if bestBackend == nil {
			return fmt.Errorf("no backend found with capabilities %q", backend.CapabilitiesMap)
		}

		log.Debug().Str("name", name).Str("bestBackend", bestBackend.Name).Msg("Installing backend from meta backend")

		// Then, let's install the best backend
		if err := InstallBackend(systemState, bestBackend, downloadStatus); err != nil {
			return err
		}

		// we need now to create a path for the meta backend, with the alias to the installed ones so it can be used to remove it
		metaBackendPath := filepath.Join(systemState.Backend.BackendsPath, name)
		if err := os.MkdirAll(metaBackendPath, 0750); err != nil {
			return fmt.Errorf("failed to create meta backend path %q: %v", metaBackendPath, err)
		}

		// Create metadata for the meta backend
		metaMetadata := &BackendMetadata{
			MetaBackendFor: bestBackend.Name,
			Name:           name,
			GalleryURL:     backend.Gallery.URL,
			InstalledAt:    time.Now().Format(time.RFC3339),
		}

		if err := writeBackendMetadata(metaBackendPath, metaMetadata); err != nil {
			return fmt.Errorf("failed to write metadata for meta backend %q: %v", name, err)
		}

		return nil
	}

	return InstallBackend(systemState, backend, downloadStatus)
}

func InstallBackend(systemState *system.SystemState, config *GalleryBackend, downloadStatus func(string, string, string, float64)) error {
	// Create base path if it doesn't exist
	err := os.MkdirAll(systemState.Backend.BackendsPath, 0750)
	if err != nil {
		return fmt.Errorf("failed to create base path: %v", err)
	}

	if config.IsMeta() {
		return fmt.Errorf("meta backends cannot be installed directly")
	}

	name := config.Name
	backendPath := filepath.Join(systemState.Backend.BackendsPath, name)
	err = os.MkdirAll(backendPath, 0750)
	if err != nil {
		return fmt.Errorf("failed to create base path: %v", err)
	}

	uri := downloader.URI(config.URI)
	// Check if it is a directory
	if uri.LooksLikeDir() {
		// It is a directory, we just copy it over in the backend folder
		if err := cp.Copy(config.URI, backendPath); err != nil {
			return fmt.Errorf("failed copying: %w", err)
		}
	} else {
		uri := downloader.URI(config.URI)
		if err := uri.DownloadFile(backendPath, "", 1, 1, downloadStatus); err != nil {
			success := false
			// Try to download from mirrors
			for _, mirror := range config.Mirrors {
				if err := downloader.URI(mirror).DownloadFile(backendPath, "", 1, 1, downloadStatus); err == nil {
					success = true
					break
				}
			}

			if !success {
				return fmt.Errorf("failed to download backend %q: %v", config.URI, err)
			}
		}
	}

	// Create metadata for the backend
	metadata := &BackendMetadata{
		Name:        name,
		GalleryURL:  config.Gallery.URL,
		InstalledAt: time.Now().Format(time.RFC3339),
	}

	if config.Alias != "" {
		metadata.Alias = config.Alias
	}

	if err := writeBackendMetadata(backendPath, metadata); err != nil {
		return fmt.Errorf("failed to write metadata for backend %q: %v", name, err)
	}

	return nil
}

func DeleteBackendFromSystem(systemState *system.SystemState, name string) error {
	backends, err := ListSystemBackends(systemState)
	if err != nil {
		return err
	}

	backend, ok := backends.Get(name)
	if !ok {
		return fmt.Errorf("backend %q not found", name)
	}

	if backend.IsSystem {
		return fmt.Errorf("system backend %q cannot be deleted", name)
	}

	backendDirectory := filepath.Join(systemState.Backend.BackendsPath, name)

	// check if the backend dir exists
	if _, err := os.Stat(backendDirectory); os.IsNotExist(err) {
		// if doesn't exist, it might be an alias, so we need to check if we have a matching alias in
		// all the backends in the basePath
		backends, err := os.ReadDir(systemState.Backend.BackendsPath)
		if err != nil {
			return err
		}
		foundBackend := false

		for _, backend := range backends {
			if backend.IsDir() {
				metadata, err := readBackendMetadata(filepath.Join(systemState.Backend.BackendsPath, backend.Name()))
				if err != nil {
					return err
				}
				if metadata != nil && metadata.Alias == name {
					backendDirectory = filepath.Join(systemState.Backend.BackendsPath, backend.Name())
					foundBackend = true
					break
				}
			}
		}

		// If no backend found, return successfully (idempotent behavior)
		if !foundBackend {
			return fmt.Errorf("no backend found with name %q", name)
		}
	}

	// If it's a meta backend, delete also associated backend
	metadata, err := readBackendMetadata(backendDirectory)
	if err != nil {
		return err
	}

	if metadata != nil && metadata.MetaBackendFor != "" {
		metaBackendDirectory := filepath.Join(systemState.Backend.BackendsPath, metadata.MetaBackendFor)
		log.Debug().Str("backendDirectory", metaBackendDirectory).Msg("Deleting meta backend")
		if _, err := os.Stat(metaBackendDirectory); os.IsNotExist(err) {
			return fmt.Errorf("meta backend %q not found", metadata.MetaBackendFor)
		}
		os.RemoveAll(metaBackendDirectory)
	}

	return os.RemoveAll(backendDirectory)
}

type SystemBackend struct {
	Name     string
	RunFile  string
	IsMeta   bool
	IsSystem bool
	Metadata *BackendMetadata
}

type SystemBackends map[string]SystemBackend

func (b SystemBackends) Exists(name string) bool {
	_, ok := b[name]
	return ok
}

func (b SystemBackends) Get(name string) (SystemBackend, bool) {
	backend, ok := b[name]
	return backend, ok
}

func (b SystemBackends) GetAll() []SystemBackend {
	backends := make([]SystemBackend, 0)
	for _, backend := range b {
		backends = append(backends, backend)
	}
	return backends
}

func ListSystemBackends(systemState *system.SystemState) (SystemBackends, error) {
	potentialBackends, err := os.ReadDir(systemState.Backend.BackendsPath)
	if err != nil {
		return nil, err
	}

	backends := make(SystemBackends)

	systemBackends, err := os.ReadDir(systemState.Backend.BackendsSystemPath)
	if err == nil {
		// system backends are special, they are provided by the system and not managed by LocalAI
		for _, systemBackend := range systemBackends {
			if systemBackend.IsDir() {
				systemBackendRunFile := filepath.Join(systemState.Backend.BackendsSystemPath, systemBackend.Name(), runFile)
				if _, err := os.Stat(systemBackendRunFile); err == nil {
					backends[systemBackend.Name()] = SystemBackend{
						Name:     systemBackend.Name(),
						RunFile:  filepath.Join(systemState.Backend.BackendsSystemPath, systemBackend.Name(), runFile),
						IsMeta:   false,
						IsSystem: true,
						Metadata: nil,
					}
				}
			}
		}
	} else {
		log.Warn().Err(err).Msg("Failed to read system backends, but that's ok, we will just use the backends managed by LocalAI")
	}

	for _, potentialBackend := range potentialBackends {
		if potentialBackend.IsDir() {
			potentialBackendRunFile := filepath.Join(systemState.Backend.BackendsPath, potentialBackend.Name(), runFile)

			var metadata *BackendMetadata

			// If metadata file does not exist, we just use the directory name
			// and we do not fill the other metadata (such as potential backend Aliases)
			metadataFilePath := filepath.Join(systemState.Backend.BackendsPath, potentialBackend.Name(), metadataFile)
			if _, err := os.Stat(metadataFilePath); os.IsNotExist(err) {
				metadata = &BackendMetadata{
					Name: potentialBackend.Name(),
				}
			} else {
				// Check for alias in metadata
				metadata, err = readBackendMetadata(filepath.Join(systemState.Backend.BackendsPath, potentialBackend.Name()))
				if err != nil {
					return nil, err
				}
			}

			if !backends.Exists(potentialBackend.Name()) {
				// We don't want to override aliases if already set, and if we are meta backend
				if _, err := os.Stat(potentialBackendRunFile); err == nil {
					backends[potentialBackend.Name()] = SystemBackend{
						Name:     potentialBackend.Name(),
						RunFile:  potentialBackendRunFile,
						IsMeta:   false,
						Metadata: metadata,
					}
				}
			}

			if metadata == nil {
				continue
			}

			if metadata.Alias != "" {
				backends[metadata.Alias] = SystemBackend{
					Name:     metadata.Alias,
					RunFile:  potentialBackendRunFile,
					IsMeta:   false,
					Metadata: metadata,
				}
			}

			if metadata.MetaBackendFor != "" {
				backends[metadata.Name] = SystemBackend{
					Name:     metadata.Name,
					RunFile:  filepath.Join(systemState.Backend.BackendsPath, metadata.MetaBackendFor, runFile),
					IsMeta:   true,
					Metadata: metadata,
				}
			}
		}
	}

	return backends, nil
}

func RegisterBackends(systemState *system.SystemState, modelLoader *model.ModelLoader) error {
	// Prefer optimal alias resolution when multiple concrete backends share the same alias
	backends, err := ListSystemBackendsSelected(systemState)
	if err != nil {
		return err
	}

	for _, backend := range backends {
		log.Debug().Str("name", backend.Name).Str("runFile", backend.RunFile).Msg("Registering backend")
		modelLoader.SetExternalBackend(backend.Name, backend.RunFile)
	}

	return nil
}

// ResolveBestBackendName returns the concrete backend name to use for a given meta or concrete backend name,
// based on the current system state and gallery metadata. If the provided name is already concrete, it is returned as-is.
func ResolveBestBackendName(galleries []config.Gallery, systemState *system.SystemState, name string) (string, error) {
	backends, err := AvailableBackends(galleries, systemState)
	if err != nil {
		return "", err
	}
	be := FindGalleryElement(backends, name)
	if be == nil {
		return "", fmt.Errorf("no backend found with name %q", name)
	}
	if !be.IsMeta() {
		return be.Name, nil
	}
	best := be.FindBestBackendFromMeta(systemState, backends)
	if best == nil {
		return "", fmt.Errorf("no backend found with capabilities %v", be.CapabilitiesMap)
	}
	return best.Name, nil
}

// ListSystemBackendsSelected lists system backends and, when multiple concrete backends share the same alias
// (e.g., cpu-llama-cpp and cuda12-llama-cpp both alias to "llama-cpp"), selects the optimal one based on the
// detected system capability (GPU vendor/platform). Concrete backend names are always included.
func ListSystemBackendsSelected(systemState *system.SystemState) (SystemBackends, error) {
	// First, include system-provided backends
	backends := make(SystemBackends)

	systemBackends, err := os.ReadDir(systemState.Backend.BackendsSystemPath)
	if err == nil {
		for _, systemBackend := range systemBackends {
			if systemBackend.IsDir() {
				systemBackendRunFile := filepath.Join(systemState.Backend.BackendsSystemPath, systemBackend.Name(), runFile)
				if _, err := os.Stat(systemBackendRunFile); err == nil {
					backends[systemBackend.Name()] = SystemBackend{
						Name:     systemBackend.Name(),
						RunFile:  systemBackendRunFile,
						IsMeta:   false,
						IsSystem: true,
						Metadata: nil,
					}
				}
			}
		}
	} else {
		log.Warn().Err(err).Msg("Failed to read system backends, proceeding with user-managed backends")
	}

	// Scan user-managed backends and group alias candidates
	entries, err := os.ReadDir(systemState.Backend.BackendsPath)
	if err != nil {
		return nil, err
	}

	aliasGroups := make(map[string][]backendCandidate)
	metaMap := make(map[string]*BackendMetadata)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		run := filepath.Join(systemState.Backend.BackendsPath, dir, runFile)

		var metadata *BackendMetadata
		metadataPath := filepath.Join(systemState.Backend.BackendsPath, dir, metadataFile)
		if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
			metadata = &BackendMetadata{Name: dir}
		} else {
			m, rerr := readBackendMetadata(filepath.Join(systemState.Backend.BackendsPath, dir))
			if rerr != nil {
				return nil, rerr
			}
			if m == nil {
				metadata = &BackendMetadata{Name: dir}
			} else {
				metadata = m
			}
		}

		metaMap[dir] = metadata

		// Always include the concrete backend name
		if _, err := os.Stat(run); err == nil {
			backends[dir] = SystemBackend{
				Name:     dir,
				RunFile:  run,
				IsMeta:   false,
				Metadata: metadata,
			}
		}

		// Collect alias candidates
		if metadata.Alias != "" {
			aliasGroups[metadata.Alias] = append(aliasGroups[metadata.Alias], backendCandidate{name: dir, runFile: run})
		}

		// Meta backend indirection (meta dir -> real dir run.sh)
		if metadata.MetaBackendFor != "" {
			backends[metadata.Name] = SystemBackend{
				Name:     metadata.Name,
				RunFile:  filepath.Join(systemState.Backend.BackendsPath, metadata.MetaBackendFor, runFile),
				IsMeta:   true,
				Metadata: metadata,
			}
		}
	}

	// For each alias, choose the best candidate for this system
	for alias, cands := range aliasGroups {
		selected := selectBestCandidate(systemState, cands)
		if selected.runFile == "" {
			// Skip if the candidate has no runnable file
			continue
		}
		// Attach metadata of the selected concrete backend, if known
		md := metaMap[selected.name]
		backends[alias] = SystemBackend{
			Name:     alias,
			RunFile:  selected.runFile,
			IsMeta:   false,
			Metadata: md,
		}
	}

	return backends, nil
}

func selectBestCandidate(systemState *system.SystemState, cands []backendCandidate) backendCandidate {
	if len(cands) == 0 {
		return backendCandidate{}
	}
	if len(cands) == 1 {
		return cands[0]
	}

	// Determine capability
	capStr := systemState.GPUVendor
	if capStr == "" {
		capStr = "default"
	}

	for _, token := range capabilityPriority(capStr) {
		for _, c := range cands {
			lname := strings.ToLower(c.name)
			if strings.Contains(lname, token) {
				return c
			}
		}
	}
	// Fallback: first one with a runfile
	for _, c := range cands {
		if c.runFile != "" {
			return c
		}
	}
	return cands[0]
}

func capabilityPriority(capStr string) []string {
	capStr = strings.ToLower(capStr)
	switch {
	case strings.HasPrefix(capStr, "nvidia"):
		return []string{"cuda", "vulkan", "cpu"}
	case strings.HasPrefix(capStr, "amd"):
		return []string{"rocm", "hip", "vulkan", "cpu"}
	case strings.HasPrefix(capStr, "intel"):
		return []string{"sycl", "intel", "cpu"}
	case strings.HasPrefix(capStr, "metal"):
		return []string{"metal", "cpu"}
	case strings.HasPrefix(capStr, "darwin-x86"):
		return []string{"darwin-x86", "cpu"}
	default:
		return []string{"cpu"}
	}
}
