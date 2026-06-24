//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package safefile

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ErrUnsafePath reports a path shape or file type that exact removal refuses.
var ErrUnsafePath = errors.New("unsafe removal path")

// RemoveExact removes a file and its named sidecars below root without
// following symbolic links. It prunes up to pruneParents empty parent
// directories, but never removes root itself.
func RemoveExact(root, relativePath string, sidecarSuffixes []string, pruneParents int) error {
	return removeExact(root, relativePath, sidecarSuffixes, pruneParents, nil)
}

func removeExact(root, relativePath string, sidecarSuffixes []string, pruneParents int, parentsOpened func()) error {
	parts, err := cleanRelativeParts(relativePath)
	if err != nil {
		return err
	}
	if pruneParents < 0 {
		return fmt.Errorf("%w: prune parent count must not be negative", ErrUnsafePath)
	}

	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("opening removal root %q: %w", root, err)
	}
	handles := []int{rootFD}
	defer func() {
		for i := len(handles) - 1; i >= 0; i-- {
			_ = unix.Close(handles[i])
		}
	}()

	for _, component := range parts[:len(parts)-1] {
		fd, openErr := unix.Openat(handles[len(handles)-1], component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) {
			return nil
		}
		if openErr != nil {
			if errors.Is(openErr, unix.ELOOP) || errors.Is(openErr, unix.ENOTDIR) {
				return fmt.Errorf("%w: path component %q is not a directory: %v", ErrUnsafePath, component, openErr)
			}
			return fmt.Errorf("opening removal path component %q: %w", component, openErr)
		}
		handles = append(handles, fd)
	}
	if parentsOpened != nil {
		parentsOpened()
	}

	parentFD := handles[len(handles)-1]
	leaf := parts[len(parts)-1]
	names := make([]string, 0, len(sidecarSuffixes)+1)
	names = append(names, leaf)
	for _, suffix := range sidecarSuffixes {
		if suffix == "" || strings.ContainsAny(suffix, `/\\`) {
			return fmt.Errorf("%w: invalid sidecar suffix %q", ErrUnsafePath, suffix)
		}
		names = append(names, leaf+suffix)
	}

	for _, name := range names {
		var stat unix.Stat_t
		statErr := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(statErr, unix.ENOENT) {
			continue
		}
		if statErr != nil {
			return fmt.Errorf("stating removal entry %q: %w", name, statErr)
		}
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFLNK:
			return fmt.Errorf("%w: refusing to remove symbolic link %q", ErrUnsafePath, name)
		case unix.S_IFDIR:
			return fmt.Errorf("%w: release key identifies a directory", ErrUnsafePath)
		}
	}

	for _, name := range names {
		if unlinkErr := unix.Unlinkat(parentFD, name, 0); unlinkErr != nil && !errors.Is(unlinkErr, unix.ENOENT) {
			return fmt.Errorf("removing entry %q: %w", name, unlinkErr)
		}
	}

	maxPrune := min(pruneParents, len(handles)-1)
	for childIndex := len(handles) - 1; childIndex >= len(handles)-maxPrune; childIndex-- {
		parentIndex := childIndex - 1
		name := parts[childIndex-1]
		same, identityErr := sameDirectoryEntry(handles[parentIndex], name, handles[childIndex])
		if identityErr != nil {
			if errors.Is(identityErr, unix.ENOENT) {
				break
			}
			return fmt.Errorf("checking directory %q before pruning: %w", name, identityErr)
		}
		if !same {
			break
		}
		removeErr := unix.Unlinkat(handles[parentIndex], name, unix.AT_REMOVEDIR)
		if removeErr == nil {
			continue
		}
		if errors.Is(removeErr, unix.ENOENT) || errors.Is(removeErr, unix.ENOTEMPTY) || errors.Is(removeErr, unix.EEXIST) {
			break
		}
		return fmt.Errorf("pruning directory %q: %w", name, removeErr)
	}
	return nil
}

func cleanRelativeParts(relativePath string) ([]string, error) {
	if relativePath == "" || filepath.IsAbs(relativePath) || filepath.Clean(relativePath) != relativePath {
		return nil, fmt.Errorf("%w: %q is not a clean relative path", ErrUnsafePath, relativePath)
	}
	parts := strings.Split(relativePath, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("%w: %q is not a clean relative path", ErrUnsafePath, relativePath)
		}
	}
	return parts, nil
}

func sameDirectoryEntry(parentFD int, name string, openedFD int) (bool, error) {
	var opened unix.Stat_t
	if err := unix.Fstat(openedFD, &opened); err != nil {
		return false, err
	}
	var current unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false, err
	}
	return current.Mode&unix.S_IFMT == unix.S_IFDIR && current.Dev == opened.Dev && current.Ino == opened.Ino, nil
}
