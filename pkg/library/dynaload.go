package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func LoadExtractedLibs(dir string) error {
	// Skip this if LOCALAI_SKIP_LIBRARY_PATH is set
	if os.Getenv("LOCALAI_SKIP_LIBRARY_PATH") != "" {
		return nil
	}

	var err error = nil
	for _, libDir := range []string{filepath.Join(dir, "backend-assets", "lib"), filepath.Join(dir, "lib")} {
		err = errors.Join(err, LoadExternal(libDir))
	}
	return err
}

func LoadExternal(dir string) error {
	// Skip this if LOCALAI_SKIP_LIBRARY_PATH is set
	if os.Getenv("LOCALAI_SKIP_LIBRARY_PATH") != "" {
		return nil
	}

	lpathVar := "LD_LIBRARY_PATH"
	if runtime.GOOS == "darwin" {
		lpathVar = "DYLD_FALLBACK_LIBRARY_PATH" // should it be DYLD_LIBRARY_PATH ?
	}

	var err error = nil
	if _, err := os.Stat(dir); err == nil {
		ldLibraryPath := os.Getenv(lpathVar)
		if ldLibraryPath == "" {
			ldLibraryPath = dir
		} else {
			ldLibraryPath = fmt.Sprintf("%s:%s", ldLibraryPath, dir)
		}
		err = errors.Join(err, os.Setenv(lpathVar, ldLibraryPath))
	}
	return err
}
