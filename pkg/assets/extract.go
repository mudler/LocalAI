package assets

import (
	"fmt"
	"os"
	"path/filepath"

	rice "github.com/GeertJohan/go.rice"
	"github.com/mudler/LocalAI/pkg/library"
)

const backendAssetsDir = "backend-assets"

func ResolvePath(dir string, paths ...string) string {
	return filepath.Join(append([]string{dir, backendAssetsDir}, paths...)...)
}

func ExtractFiles(content *rice.Box, extractDir string) error {
	// Create the target directory with backend-assets subdirectory
	backendAssetsDir := filepath.Join(extractDir, backendAssetsDir)
	err := os.MkdirAll(backendAssetsDir, 0750)
	if err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	// Walk through the rice box and extract files
	err = content.Walk("", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Reconstruct the directory structure in the target directory
		targetFile := filepath.Join(backendAssetsDir, path)
		if info.IsDir() {
			// Create the directory in the target directory
			err := os.MkdirAll(targetFile, 0750)
			if err != nil {
				return fmt.Errorf("failed to create directory: %v", err)
			}
			return nil
		}

		// Read the file from the rice box
		fileData, err := content.Bytes(path)
		if err != nil {
			return fmt.Errorf("failed to read file: %v", err)
		}

		// Create the file in the target directory
		err = os.WriteFile(targetFile, fileData, 0700)
		if err != nil {
			return fmt.Errorf("failed to write file: %v", err)
		}

		return nil
	})

	// If there is a lib directory, set LD_LIBRARY_PATH to include it
	// we might use this mechanism to carry over e.g. Nvidia CUDA libraries
	// from the embedded FS to the target directory
	library.LoadExtractedLibs(backendAssetsDir)

	return err
}
