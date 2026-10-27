// Command gvisor-profile prints the AppArmor profile that lets a runsc binary
// make user namespaces. An administrator runs it once per runsc binary:
//
//	gvisor-profile /opt/gvisor/runsc | sudo tee /etc/apparmor.d/localai-runsc
//	sudo apparmor_parser -r /etc/apparmor.d/localai-runsc
//
// The profile names only that binary. It is the one step that needs root.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/gvisor"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gvisor-profile /path/to/runsc")
		os.Exit(2)
	}
	text, err := profile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "gvisor-profile:", err)
		os.Exit(1)
	}
	fmt.Print(text)
}

// profile returns the profile text for the runsc binary at path. The path
// must exist and pass gvisor.ValidateProfilePath. A symlink is followed: the
// profile names the file it leads to.
func profile(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// AppArmor attaches a profile by the real path of the executed file, and
	// New resolves symlinks the same way.
	if abs, err = filepath.EvalSymlinks(abs); err != nil {
		return "", fmt.Errorf("%s is not a file: %w", path, err)
	}
	if st, err := os.Stat(abs); err != nil || st.IsDir() {
		return "", fmt.Errorf("%s is not a file", abs)
	}
	if err := gvisor.ValidateProfilePath(abs); err != nil {
		return "", err
	}
	return gvisor.Profile(abs), nil
}
