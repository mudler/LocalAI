package gallery

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/system"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/rs/zerolog/log"
)

const (
	aliasFile = "alias"
	metaFile  = "meta"
	runFile   = "run.sh"
)

func findBestBackendFromMeta(backend *GalleryBackend, systemState *system.SystemState, backends GalleryElements[*GalleryBackend]) *GalleryBackend {
	realBackend := backend.CapabilitiesMap[systemState.GPUVendor]
	if realBackend == "" {
		return nil
	}

	return backends.FindByName(realBackend)
}

// Installs a model from the gallery
func InstallBackendFromGallery(galleries []config.Gallery, systemState *system.SystemState, name string, basePath string, downloadStatus func(string, string, string, float64)) error {
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
		bestBackend := findBestBackendFromMeta(backend, systemState, backends)
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

		// Then, let's create an meta file to point to the best backend
		metaFile := filepath.Join(metaBackendPath, metaFile)
		if err := os.WriteFile(metaFile, []byte(bestBackend.Name), 0644); err != nil {
			return fmt.Errorf("failed to write meta file %q: %v", metaFile, err)
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

	img, err := oci.GetImage(config.URI, "", nil, nil)
	if err != nil {
		return fmt.Errorf("failed to get image %q: %v", config.URI, err)
	}

	backendPath := filepath.Join(basePath, name)
	if err := os.MkdirAll(backendPath, 0750); err != nil {
		return fmt.Errorf("failed to create backend path %q: %v", backendPath, err)
	}

	if err := oci.ExtractOCIImage(img, backendPath, downloadStatus); err != nil {
		return fmt.Errorf("failed to extract image %q: %v", config.URI, err)
	}

	if config.Alias != "" {
		// Write an alias file inside
		aliasFile := filepath.Join(backendPath, aliasFile)
		if err := os.WriteFile(aliasFile, []byte(config.Alias), 0644); err != nil {
			return fmt.Errorf("failed to write alias file %q: %v", aliasFile, err)
		}
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

		for _, backend := range backends {
			if backend.IsDir() {
				aliasFile := filepath.Join(basePath, backend.Name(), aliasFile)
				alias, err := os.ReadFile(aliasFile)
				if err != nil {
					return err
				}
				if string(alias) == name {
					backendDirectory = filepath.Join(basePath, backend.Name())
					break
				}
			}
		}

		if backendDirectory == "" {
			return fmt.Errorf("no backend found with name %q", name)
		}
	}

	// If it's a meta, delete also associated backend
	metaFile := filepath.Join(backendDirectory, metaFile)
	if _, err := os.Stat(metaFile); err == nil {
		meta, err := os.ReadFile(metaFile)
		if err != nil {
			return err
		}
		metaBackendDirectory := filepath.Join(basePath, string(meta))
		log.Debug().Str("backendDirectory", metaBackendDirectory).Msg("Deleting meta backend")
		if _, err := os.Stat(metaBackendDirectory); os.IsNotExist(err) {
			return fmt.Errorf("meta backend %q not found", string(meta))
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
			backendsNames[backend.Name()] = runFile

			aliasFile := filepath.Join(basePath, backend.Name(), aliasFile)
			if _, err := os.Stat(aliasFile); err == nil {
				// read the alias file, and use it as key
				alias, err := os.ReadFile(aliasFile)
				if err != nil {
					return nil, err
				}
				backendsNames[string(alias)] = runFile
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
		modelLoader.SetExternalBackend(name, runFile)
	}

	return nil
}
