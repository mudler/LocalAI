package model

import (
	"fmt"
	"path/filepath"
	"strings"
)

// windowsDirectLaunchExtensions are the file types CreateProcess can start
// without a script host.
var windowsDirectLaunchExtensions = map[string]bool{".exe": true, ".com": true, ".bat": true, ".cmd": true}

// checkWindowsDirectLaunch tells whether a Windows host can start path
// directly. A backend built only for Linux or macOS ships a run.sh and no
// run.ps1; starting that file fails with "not a valid Win32 application",
// which does not say what is wrong. Return an error that does.
//
// A name without an extension is accepted: the process start looks it up
// with the executable extensions (grpc-server finds grpc-server.exe).
func checkWindowsDirectLaunch(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" || windowsDirectLaunchExtensions[ext] {
		return nil
	}
	return fmt.Errorf("the backend in %s has no Windows launcher (run.ps1) and %s is not a Windows executable: this backend has no Windows build. On Windows only the llama-cpp backend is available", filepath.Dir(path), filepath.Base(path))
}
