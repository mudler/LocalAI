package utils

import (
	"fmt"
	"path/filepath"
)

func inTrustedRoot(path string, trustedRoot string) error {
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
	return inTrustedRoot(c, filepath.Clean(basePath))
}
