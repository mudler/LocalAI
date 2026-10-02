package nodes

import (
	"crypto/rand"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/safefile"
)

// A validated model path does not validate the adjacent metadata: an attacker
// can place a separate symlink at the sidecar name.
func readHashSidecar(path string) ([]byte, error) {
	data, _, err := safefile.ReadRegularAt(filepath.Dir(path), filepath.Base(path))
	return data, err
}

func writeHashSidecar(path, hash string) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	// Replace metadata atomically instead of following an existing sidecar link.
	name := ".localai-hash-" + rand.Text()
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(name) }()
	_, writeErr := file.WriteString(hash)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(name, filepath.Base(path))
}
