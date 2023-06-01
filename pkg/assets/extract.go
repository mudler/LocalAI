package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func ExtractFiles(content embed.FS, extractDir string) error {
	// Create the target directory if it doesn't exist
	err := os.MkdirAll(extractDir, 0755)
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
			err := os.MkdirAll(targetFile, 0755)
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
		err = os.WriteFile(targetFile, fileData, 0644)
		if err != nil {
			return fmt.Errorf("failed to write file: %v", err)
		}

		return nil
	})

	return err
}
