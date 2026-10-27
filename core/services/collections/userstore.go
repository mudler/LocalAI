package collections

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// userStorage lays out the files of the collections by user: the root (no user)
// uses the state directory itself, a user uses <state>/users/<id>.
type userStorage struct{ baseDir string }

func (s userStorage) resolve(userID string) string {
	if userID == "" {
		return s.baseDir
	}
	return filepath.Join(s.baseDir, "users", userID)
}

// CollectionsDir is the directory of the collections of a user.
func (s userStorage) CollectionsDir(userID string) string {
	return filepath.Join(s.resolve(userID), "collections")
}

// AssetsDir is the directory of the files of a user's collections.
func (s userStorage) AssetsDir(userID string) string {
	return filepath.Join(s.resolve(userID), "assets")
}

func (s userStorage) ensure(userID string) error {
	for _, d := range []string{s.CollectionsDir(userID), s.AssetsDir(userID)} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", d, err)
		}
	}
	return nil
}

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// userDirs lists the users that have a directory under <state>/users, sorted.
func (s userStorage) userDirs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.baseDir, "users"))
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read users directory: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && uuidRegex.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// ValidateUserID checks that a user ID is safe in a file path: empty (the root)
// or a UUID.
func ValidateUserID(id string) error {
	if id == "" {
		return nil
	}
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return fmt.Errorf("invalid user ID: contains path traversal characters")
	}
	if !uuidRegex.MatchString(id) {
		return fmt.Errorf("invalid user ID: must be a valid UUID")
	}
	return nil
}
