package gallery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// Installs a model from the gallery
func InstallBackendFromGallery(galleries []config.Gallery, systemState *system.SystemState, name string, basePath string, downloadStatus func(string, string, string, float64), force bool) error {
	if !force {
		// check if we already have the backend installed
		backends, err := ListSystemBackends(basePath)
		if err != nil {
			return err
		}
		if _, ok := backends[name]; ok {
			return nil
		}
	}

	if name == "" {
		return fmt.Errorf("backend name is empty")
	}

	log.Debug().Interface("galleries", galleries).Str("name", name).Msg("Installing backend from gallery")

	backends, err := AvailableBackends(galleries, basePath)
	if err != nil {
		return err
	}

	backend := FindGalleryElement(backends, name, basePath)
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
		if err := InstallBackend(basePath, bestBackend, downloadStatus); err != nil {
			return err
		}

		// we need now to create a path for the meta backend, with the alias to the installed ones so it can be used to remove it
		metaBackendPath := filepath.Join(basePath, name)
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

	return InstallBackend(basePath, backend, downloadStatus)
}

func InstallBackend(basePath string, config *GalleryBackend, downloadStatus func(string, string, string, float64)) error {
	// Create base path if it doesn't exist
	err := os.MkdirAll(basePath, 0750)
	if err != nil {
		return fmt.Errorf("failed to create base path: %v", err)
	}

	if config.IsMeta() {
		return fmt.Errorf("meta backends cannot be installed directly")
	}

	name := config.Name
	backendPath := filepath.Join(basePath, name)
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

func DeleteBackendFromSystem(basePath string, name string) error {
	backendDirectory := filepath.Join(basePath, name)

	// check if the backend dir exists
	if _, err := os.Stat(backendDirectory); os.IsNotExist(err) {
		// if doesn't exist, it might be an alias, so we need to check if we have a matching alias in
		// all the backends in the basePath
		backends, err := os.ReadDir(basePath)
		if err != nil {
			return err
		}
		foundBackend := false

		for _, backend := range backends {
			if backend.IsDir() {
				metadata, err := readBackendMetadata(filepath.Join(basePath, backend.Name()))
				if err != nil {
					return err
				}
				if metadata != nil && metadata.Alias == name {
					backendDirectory = filepath.Join(basePath, backend.Name())
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
		metaBackendDirectory := filepath.Join(basePath, metadata.MetaBackendFor)
		log.Debug().Str("backendDirectory", metaBackendDirectory).Msg("Deleting meta backend")
		if _, err := os.Stat(metaBackendDirectory); os.IsNotExist(err) {
			return fmt.Errorf("meta backend %q not found", metadata.MetaBackendFor)
		}
		os.RemoveAll(metaBackendDirectory)
	}

	return os.RemoveAll(backendDirectory)
}

func ListSystemBackends(basePath string) (map[string]string, error) {
	backends, err := os.ReadDir(basePath)
	if err != nil {
		return nil, err
	}

	backendsNames := make(map[string]string)

	for _, backend := range backends {
		if backend.IsDir() {
			runFile := filepath.Join(basePath, backend.Name(), runFile)
			// Skip if metadata file don't exist
			metadataFilePath := filepath.Join(basePath, backend.Name(), metadataFile)
			if _, err := os.Stat(metadataFilePath); os.IsNotExist(err) {
				continue
			}

			// Check for alias in metadata
			metadata, err := readBackendMetadata(filepath.Join(basePath, backend.Name()))
			if err != nil {
				return nil, err
			}

			if metadata == nil {
				continue
			}

			if _, exists := backendsNames[backend.Name()]; !exists {
				// We don't want to override aliases if already set, and if we are meta backend
				if _, err := os.Stat(runFile); err == nil {
					backendsNames[backend.Name()] = runFile
				} else {
					backendsNames[backend.Name()] = ""
				}
			}

			if metadata.Alias != "" {
				backendsNames[metadata.Alias] = runFile
			}
		}
	}

	return backendsNames, nil
}

func RegisterBackends(basePath string, modelLoader *model.ModelLoader) error {
	backends, err := ListSystemBackends(basePath)
	if err != nil {
		return err
	}

	for name, runFile := range backends {
		log.Debug().Str("name", name).Str("runFile", runFile).Msg("Registering backend")
		modelLoader.SetExternalBackend(name, runFile)
	}

	return nil
}
