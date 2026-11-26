package system

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Folder is a folder that LocalAI writes to, with the name a person knows it
// by, such as "models" or "data".
type Folder struct {
	Name string
	Path string
}

// UnwritableFolder is a folder that this process cannot write to.
type UnwritableFolder struct {
	Folder
	// OwnerUID is the user ID that owns the folder, or the nearest folder
	// above it that exists. It is -1 when the system has no user IDs.
	OwnerUID int
	Err      error
}

func (u UnwritableFolder) String() string {
	if u.OwnerUID < 0 {
		return fmt.Sprintf("%s=%s", u.Name, u.Path)
	}
	return fmt.Sprintf("%s=%s (owner uid %d)", u.Name, u.Path, u.OwnerUID)
}

// CheckWritable creates and removes a file in each folder and returns the
// folders where that fails. A folder that does not exist yet is checked at
// the nearest folder above it that exists, because LocalAI can make it there.
// It changes nothing else. Empty paths are skipped, and a path is checked
// once even when several names share it.
func CheckWritable(folders []Folder) []UnwritableFolder {
	var bad []UnwritableFolder
	seen := map[string]bool{}
	for _, f := range folders {
		if f.Path == "" {
			continue
		}
		clean := filepath.Clean(f.Path)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		probe := nearestExisting(clean)
		if err := probeWrite(probe); err != nil {
			bad = append(bad, UnwritableFolder{Folder: f, OwnerUID: ownerUID(probe), Err: err})
		}
	}
	return bad
}

// FormatUnwritable joins the folders for one log line.
func FormatUnwritable(folders []UnwritableFolder) string {
	parts := make([]string, len(folders))
	for i, f := range folders {
		parts[i] = f.String()
	}
	return strings.Join(parts, ", ")
}

func nearestExisting(path string) string {
	for {
		if _, err := os.Stat(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

func probeWrite(dir string) error {
	f, err := os.CreateTemp(dir, ".localai-write-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}
