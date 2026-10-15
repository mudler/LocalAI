// Package slotcache holds the checks for the slot prompt-cache save and
// restore API. A request names a file; this package decides whether that
// name is safe to hand to a backend that writes or reads it on disk.
package slotcache

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// OptionName is the llama-cpp model option that sets the save directory.
const OptionName = "slot_save_path"

// maxFilenameLen is the common largest file name on Linux file systems.
const maxFilenameLen = 255

// ErrInvalidFilename is returned for a name that is not a single, plain file name.
var ErrInvalidFilename = errors.New("invalid filename: use a single file name without directories")

// ValidateFilename accepts a single path component and nothing else. The rules
// follow llama.cpp's own filename check, with path separators refused, and
// also refuse "." and "..". A name that passes cannot be empty, absolute or
// carry a separator, a NUL byte or a parent reference.
func ValidateFilename(name string) error {
	if name == "" || len(name) > maxFilenameLen || name == "." || name == ".." {
		return ErrInvalidFilename
	}
	if !utf8.ValidString(name) {
		return ErrInvalidFilename
	}
	for _, r := range name {
		switch {
		case r <= 0x1F, r == 0x7F, r >= 0x80 && r <= 0x9F: // control characters, NUL included
			return ErrInvalidFilename
		case r == 0xFF0E, r == 0x2215, r == 0x2216: // look-alikes of '.', '/' and '\'
			return ErrInvalidFilename
		case r == utf8.RuneError, r == 0xFEFF: // replacement character, BOM
			return ErrInvalidFilename
		case strings.ContainsRune(`/\:*?"<>|`, r):
			return ErrInvalidFilename
		}
	}
	// Windows strips a leading or trailing space and a trailing dot.
	if name[0] == ' ' || name[len(name)-1] == ' ' || name[len(name)-1] == '.' {
		return ErrInvalidFilename
	}
	// Belt and braces: the platform's own view of the name must be the name.
	if filepath.IsAbs(name) || filepath.Base(name) != name || filepath.Clean(name) != name {
		return ErrInvalidFilename
	}
	return nil
}

// Resolve validates name and returns the full path of that file inside dir.
// It fails if the result is not a direct child of dir. When dir or the file
// exist, symbolic links are resolved first, so a link inside dir that points
// elsewhere is refused. When they do not exist here (for example the backend
// runs on another host) only the lexical check applies, and the backend
// repeats the check on its own disk.
func Resolve(dir, name string) (string, error) {
	if dir == "" {
		return "", errors.New("slot_save_path is not set for this model")
	}
	if err := ValidateFilename(name); err != nil {
		return "", err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve slot save directory: %w", err)
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	target := filepath.Join(root, name)
	if real, err := filepath.EvalSymlinks(target); err == nil {
		target = real
	}
	if filepath.Dir(target) != root {
		return "", errors.New("filename resolves outside the slot save directory")
	}
	return target, nil
}

// DirFromOptions returns the slot_save_path value from a model's options, or
// "" when it is not set. Options have the form name:value. Everything after
// the first colon is the value, so a path that contains a colon is kept whole.
func DirFromOptions(options []string) string {
	dir := ""
	for _, o := range options {
		if k, v, ok := strings.Cut(o, ":"); ok && k == OptionName {
			dir = strings.TrimSpace(v)
		}
	}
	return dir
}
