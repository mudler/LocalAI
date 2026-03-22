package services

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// UserScopedStorage resolves per-user storage directories.
// When userID is empty, paths resolve to root-level (backward compat).
// When userID is set, paths resolve to {baseDir}/users/{userID}/...
type UserScopedStorage struct {
	baseDir string // State directory
	dataDir string // Data directory (for jobs files)
}

// NewUserScopedStorage creates a new UserScopedStorage.
func NewUserScopedStorage(baseDir, dataDir string) *UserScopedStorage {
	return &UserScopedStorage{
		baseDir: baseDir,
		dataDir: dataDir,
	}
}

// resolve returns baseDir for empty userID, or baseDir/users/{userID} otherwise.
func (s *UserScopedStorage) resolve(userID string) string {
	if userID == "" {
		return s.baseDir
	}
	return filepath.Join(s.baseDir, "users", userID)
}

// resolveData returns dataDir for empty userID, or baseDir/users/{userID} otherwise.
func (s *UserScopedStorage) resolveData(userID string) string {
	if userID == "" {
		return s.dataDir
	}
	return filepath.Join(s.baseDir, "users", userID)
}

// UserDir returns the root directory for a user's scoped data.
func (s *UserScopedStorage) UserDir(userID string) string {
	return s.resolve(userID)
}

// CollectionsDir returns the collections directory for a user.
func (s *UserScopedStorage) CollectionsDir(userID string) string {
	return filepath.Join(s.resolve(userID), "collections")
}

// AssetsDir returns the assets directory for a user.
func (s *UserScopedStorage) AssetsDir(userID string) string {
	return filepath.Join(s.resolve(userID), "assets")
}

// OutputsDir returns the outputs directory for a user.
func (s *UserScopedStorage) OutputsDir(userID string) string {
	return filepath.Join(s.resolve(userID), "outputs")
}

// SkillsDir returns the skills directory for a user.
func (s *UserScopedStorage) SkillsDir(userID string) string {
	return filepath.Join(s.resolve(userID), "skills")
}

// TasksFile returns the path to the agent_tasks.json for a user.
func (s *UserScopedStorage) TasksFile(userID string) string {
	return filepath.Join(s.resolveData(userID), "agent_tasks.json")
}

// JobsFile returns the path to the agent_jobs.json for a user.
func (s *UserScopedStorage) JobsFile(userID string) string {
	return filepath.Join(s.resolveData(userID), "agent_jobs.json")
}

// EnsureUserDirs creates all subdirectories for a user.
func (s *UserScopedStorage) EnsureUserDirs(userID string) error {
	dirs := []string{
		s.CollectionsDir(userID),
		s.AssetsDir(userID),
		s.OutputsDir(userID),
		s.SkillsDir(userID),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0750); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", d, err)
		}
	}
	return nil
}

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ListUserDirs scans {baseDir}/users/ and returns sorted UUIDs matching uuidRegex.
// Returns an empty slice if the directory doesn't exist.
func (s *UserScopedStorage) ListUserDirs() ([]string, error) {
	usersDir := filepath.Join(s.baseDir, "users")
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read users directory: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && uuidRegex.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// ValidateUserID validates that a userID is safe for use in filesystem paths.
// Empty string is allowed (maps to root storage). Otherwise must be a valid UUID.
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

// ValidateAgentName validates that an agent name is safe (no namespace escape or path traversal).
func ValidateAgentName(name string) error {
	if name == "" {
		return fmt.Errorf("agent name is required")
	}
	if strings.ContainsAny(name, ":/\\\x00") || strings.Contains(name, "..") {
		return fmt.Errorf("agent name contains invalid characters (: / \\ .. or null bytes are not allowed)")
	}
	return nil
}
