package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ExistsInPath(path string, s string) bool {
	_, err := os.Stat(filepath.Join(path, s))
	return err == nil
}

func InTrustedRoot(path string, trustedRoot string) error {
	for path != "/" {
		path = filepath.Dir(path)
		if path == trustedRoot {
			return nil
		}
	}
	return fmt.Errorf("path is outside of trusted root")
}

// VerifyPath verifies that path is based in basePath.
func VerifyPath(path, basePath string) error {
	c := filepath.Clean(filepath.Join(basePath, path))
	return InTrustedRoot(c, filepath.Clean(basePath))
}

// SanitizeFileName sanitizes the given filename
func SanitizeFileName(fileName string) string {
	// filepath.Clean to clean the path
	cleanName := filepath.Clean(fileName)
	// filepath.Base to ensure we only get the final element, not any directory path
	baseName := filepath.Base(cleanName)
	// Replace any remaining tricky characters that might have survived cleaning
	safeName := strings.ReplaceAll(baseName, "..", "")
	return safeName
}

func GenerateUniqueFileName(dir, baseName, ext string) string {
	counter := 1
	fileName := baseName + ext

	for {
		filePath := filepath.Join(dir, fileName)
		_, err := os.Stat(filePath)
		if os.IsNotExist(err) {
			return fileName
		}

		counter++
		fileName = fmt.Sprintf("%s_%d%s", baseName, counter, ext)
	}
}
