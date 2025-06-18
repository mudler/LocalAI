package gallery

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/oci"
)

// Installs a model from the gallery
func InstallBackendFromGallery(galleries []config.Gallery, name string, basePath string, downloadStatus func(string, string, string, float64)) error {
	backends, err := AvailableBackends(galleries, basePath)
	if err != nil {
		return err
	}

	backend := FindGalleryElement(backends, name, basePath)
	if backend == nil {
		return fmt.Errorf("no model found with name %q", name)
	}

	return InstallBackend(basePath, backend, downloadStatus)
}

func InstallBackend(basePath string, config *GalleryBackend, downloadStatus func(string, string, string, float64)) error {
	// Create base path if it doesn't exist
	err := os.MkdirAll(basePath, 0750)
	if err != nil {
		return fmt.Errorf("failed to create base path: %v", err)
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
		aliasFile := filepath.Join(backendPath, "alias")
		if err := os.WriteFile(aliasFile, []byte(config.Alias), 0644); err != nil {
			return fmt.Errorf("failed to write alias file %q: %v", aliasFile, err)
		}
	}

	return nil
}

func DeleteBackendFromSystem(basePath string, name string) error {
	backendFile := filepath.Join(basePath, name)

	return os.RemoveAll(backendFile)
}

func ListSystemBackends(basePath string) (map[string]string, error) {
	backends, err := os.ReadDir(basePath)
	if err != nil {
		return nil, err
	}

	backendsNames := make(map[string]string)

	for _, backend := range backends {
		if backend.IsDir() {
			runFile := filepath.Join(basePath, backend.Name(), "run.sh")
			backendsNames[backend.Name()] = runFile

			aliasFile := filepath.Join(basePath, backend.Name(), "alias")
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
