//go:build windows

package system

import (
	"fmt"
	"os"

	winapi "golang.org/x/sys/windows"
)

// knownFolderPath asks the shell for the folder, which also works for a
// service account whose environment has no LOCALAPPDATA, and falls back to the
// environment variable.
func knownFolderPath(f knownFolder) (string, error) {
	id, name := winapi.FOLDERID_LocalAppData, "LOCALAPPDATA"
	if f == folderProgramData {
		id, name = winapi.FOLDERID_ProgramData, "ProgramData"
	}
	p, err := winapi.KnownFolderPath(id, 0)
	if err == nil && p != "" {
		return p, nil
	}
	if v := os.Getenv(name); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("resolving the %s folder: %v", name, err)
}
