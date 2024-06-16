package library

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func LoadExtractedLibs(dir string) {
	// Skip this if LOCALAI_SKIP_LIBRARY_PATH is set
	if os.Getenv("LOCALAI_SKIP_LIBRARY_PATH") != "" {
		return
	}

	for _, libDir := range []string{filepath.Join(dir, "backend-assets", "lib"), filepath.Join(dir, "lib")} {
		LoadExternal(libDir)
	}
}

func LoadExternal(dir string) {
	// Skip this if LOCALAI_SKIP_LIBRARY_PATH is set
	if os.Getenv("LOCALAI_SKIP_LIBRARY_PATH") != "" {
		return
	}

	lpathVar := "LD_LIBRARY_PATH"
	if runtime.GOOS == "darwin" {
		lpathVar = "DYLD_FALLBACK_LIBRARY_PATH" // should it be DYLD_LIBRARY_PATH ?
	}

	if _, err := os.Stat(dir); err == nil {
		ldLibraryPath := os.Getenv(lpathVar)
		if ldLibraryPath == "" {
			ldLibraryPath = dir
		} else {
			ldLibraryPath = fmt.Sprintf("%s:%s", ldLibraryPath, dir)
		}
		os.Setenv(lpathVar, ldLibraryPath)
	}
}
