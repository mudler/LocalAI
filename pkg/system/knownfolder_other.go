//go:build !windows

package system

import "errors"

// knownFolderPath is only used on Windows.
func knownFolderPath(knownFolder) (string, error) {
	return "", errors.New("known folders exist only on Windows")
}
