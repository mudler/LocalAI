package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func ResolvePath(dir string, paths ...string) string {
	return filepath.Join(append([]string{dir, "backend-assets"}, paths...)...)
}

func ExtractFiles(content embed.FS, extractDir string) error {
	// Create the target directory if it doesn't exist
	err := os.MkdirAll(extractDir, 0750)
	if err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	// Walk through the embedded FS and extract files
	err = fs.WalkDir(content, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Reconstruct the directory structure in the target directory
		targetFile := filepath.Join(extractDir, path)
		if d.IsDir() {
			// Create the directory in the target directory
			err := os.MkdirAll(targetFile, 0750)
			if err != nil {
				return fmt.Errorf("failed to create directory: %v", err)
			}
			return nil
		}

		// Read the file from the embedded FS
		fileData, err := content.ReadFile(path)
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

	// Skip this if LOCALAI_SKIP_LD_LIBRARY_PATH is set
	if os.Getenv("LOCALAI_SKIP_LD_LIBRARY_PATH") != "" {
		return err
	}

	for _, libDir := range []string{filepath.Join(extractDir, "backend_assets", "lib"), filepath.Join(extractDir, "lib")} {
		if _, err := os.Stat(libDir); err == nil {
			ldLibraryPath := os.Getenv("LD_LIBRARY_PATH")
			if ldLibraryPath == "" {
				ldLibraryPath = libDir
			} else {
				ldLibraryPath = fmt.Sprintf("%s:%s", ldLibraryPath, libDir)
			}
			os.Setenv("LD_LIBRARY_PATH", ldLibraryPath)
		}
	}
	return err
}
