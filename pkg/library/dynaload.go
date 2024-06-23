package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/rs/zerolog/log"
)

/*
	This file contains functions to load libraries from the asset directory to keep the business logic clean.
*/

// skipLibraryPath checks if LOCALAI_SKIP_LIBRARY_PATH is set
var skipLibraryPath = os.Getenv("LOCALAI_SKIP_LIBRARY_PATH") != ""

// LoadExtractedLibs loads the extracted libraries from the asset dir
func LoadExtractedLibs(dir string) error {
	// Skip this if LOCALAI_SKIP_LIBRARY_PATH is set
	if skipLibraryPath {
		return nil
	}

	var err error = nil
	for _, libDir := range []string{filepath.Join(dir, "backend-assets", "lib"), filepath.Join(dir, "lib")} {
		err = errors.Join(err, LoadExternal(libDir))
	}
	return err
}

// LoadLDSO checks if there is a ld.so in the asset dir and if so, prefixes the grpc process with it.
// In linux, if we find a ld.so in the asset dir we prefix it to run with the libs exposed in
// LD_LIBRARY_PATH for more compatibility
// If we don't do this, we might run into stack smash
// See also: https://stackoverflow.com/questions/847179/multiple-glibc-libraries-on-a-single-host/851229#851229
// In this case, we expect a ld.so in the lib asset dir.
// If that's present, we use it to run the grpc backends as supposedly built against
// that specific version of ld.so
func LoadLDSO(assetDir string, args []string, grpcProcess string) ([]string, string) {
	if skipLibraryPath {
		return args, grpcProcess
	}

	if runtime.GOOS != "linux" {
		return args, grpcProcess
	}

	// Check if there is a ld.so file in the assetDir, if it does, we need to run the grpc process with it
	ldPath := filepath.Join(assetDir, "backend-assets", "lib", "ld.so")
	if _, err := os.Stat(ldPath); err == nil {
		log.Debug().Msgf("ld.so found")
		// We need to run the grpc process with the ld.so
		args = append([]string{grpcProcess}, args...)
		grpcProcess = ldPath
	}

	return args, grpcProcess
}

// LoadExternal sets the LD_LIBRARY_PATH to include the given directory
func LoadExternal(dir string) error {
	// Skip this if LOCALAI_SKIP_LIBRARY_PATH is set
	if skipLibraryPath {
		return nil
	}

	lpathVar := "LD_LIBRARY_PATH"
	if runtime.GOOS == "darwin" {
		lpathVar = "DYLD_FALLBACK_LIBRARY_PATH" // should it be DYLD_LIBRARY_PATH ?
	}

	var setErr error = nil
	if _, err := os.Stat(dir); err == nil {
		ldLibraryPath := os.Getenv(lpathVar)
		if ldLibraryPath == "" {
			ldLibraryPath = dir
		} else {
			ldLibraryPath = fmt.Sprintf("%s:%s", ldLibraryPath, dir)
		}
		setErr = errors.Join(setErr, os.Setenv(lpathVar, ldLibraryPath))
	}
	return setErr
}
