package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/pkg/safefile"
)

// isPathAllowed checks if path is within one of the allowed directories.
func isPathAllowed(path string, allowedDirs []string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Path may not exist yet; use the absolute path
		resolved = absPath
	}
	for _, dir := range allowedDirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		// Compare both sides after resolving aliases such as macOS /var.
		if resolvedDir, err := filepath.EvalSymlinks(absDir); err == nil {
			absDir = resolvedDir
		}
		if strings.HasPrefix(resolved, absDir+string(filepath.Separator)) || resolved == absDir {
			return true
		}
	}
	return false
}

func releaseEphemeralCacheKey(cacheDir, key string) error {
	return releaseEphemeralCachePath(cacheDir, key, filepath.Join(cacheDir, filepath.FromSlash(key)))
}

func releaseEphemeralCachePath(cacheDir, key, filePath string) error {
	return releaseEphemeralCachePathWithCapacity(cacheDir, key, filePath, nil)
}

func releaseEphemeralCachePathWithCapacity(cacheDir, key, filePath string, capacity *EphemeralCapacityGuard) error {
	if err := validateEphemeralCacheKey(key); err != nil {
		return err
	}
	relativePath := filepath.FromSlash(key)
	expectedPath := filepath.Join(cacheDir, relativePath)
	if filepath.Clean(filePath) != expectedPath {
		return fmt.Errorf("release path %q does not match key %q", filePath, key)
	}
	if err := safefile.RemoveExact(cacheDir, relativePath, []string{".sha256", ".sha256.target"}, 2); err != nil {
		return err
	}
	for _, path := range []string{filePath, filePath + ".sha256", filePath + ".sha256.target"} {
		if capacity != nil {
			if err := capacity.Release(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func releaseEphemeralCacheRequest(ctx context.Context, cacheDir, requestID string, capacity *EphemeralCapacityGuard) error {
	if err := validateEphemeralCacheRequestID(requestID); err != nil {
		return err
	}
	if capacity != nil {
		if err := capacity.BeginRequestRelease(ctx, requestID); err != nil {
			return fmt.Errorf("beginning release for request %q: %w", requestID, err)
		}
		defer capacity.EndRequestRelease(requestID)
	}
	root := filepath.Join(cacheDir, "ephemeral")
	categories, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var releaseErrors []error
	for _, category := range categories {
		if !category.IsDir() || category.Type()&os.ModeSymlink != 0 {
			continue
		}
		requestDir := filepath.Join(root, category.Name(), requestID)
		info, err := os.Lstat(requestDir)
		if err != nil {
			if !os.IsNotExist(err) {
				releaseErrors = append(releaseErrors, fmt.Errorf("stating request directory %q: %w", requestDir, err))
			}
			continue
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			releaseErrors = append(releaseErrors, fmt.Errorf("ephemeral request path %q is not a real directory", requestDir))
			continue
		}
		entries, err := os.ReadDir(requestDir)
		if err != nil {
			if !os.IsNotExist(err) {
				releaseErrors = append(releaseErrors, fmt.Errorf("reading request directory %q: %w", requestDir, err))
			}
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				releaseErrors = append(releaseErrors, fmt.Errorf("unexpected directory in ephemeral request %q", filepath.Join(requestDir, entry.Name())))
				continue
			}
			key := filepath.ToSlash(filepath.Join("ephemeral", category.Name(), requestID, entry.Name()))
			filePath := filepath.Join(cacheDir, filepath.FromSlash(key))
			if err := releaseEphemeralCachePathWithCapacity(cacheDir, key, filePath, capacity); err != nil {
				releaseErrors = append(releaseErrors, fmt.Errorf("releasing %q: %w", key, err))
			}
		}
	}
	return errors.Join(releaseErrors...)
}

type ephemeralStagingCapacity interface {
	Reserve(path string, size int64) error
	Commit(path string) error
	Claim(path string) error
	Release(path string) error
}

func ensureWorkerFile(ctx context.Context, fm *storage.FileManager, capacity *EphemeralCapacityGuard, key string) (string, error) {
	if capacity == nil {
		return fm.Download(ctx, key)
	}
	if strings.HasPrefix(key, "ephemeral/") {
		if err := validateEphemeralCacheKey(key); err != nil {
			return "", err
		}
		requestID := strings.Split(key, "/")[2]
		if err := capacity.BeginRequestOperation(requestID); err != nil {
			return "", err
		}
		defer capacity.EndRequestOperation(requestID)
	}
	return ensureWorkerFileWithCapacity(ctx, fm, capacity, key)
}

func ensureWorkerFileWithCapacity(ctx context.Context, fm *storage.FileManager, capacity ephemeralStagingCapacity, key string) (string, error) {
	if capacity == nil || !strings.HasPrefix(key, "ephemeral/") {
		return fm.Download(ctx, key)
	}
	if err := validateEphemeralCacheKey(key); err != nil {
		return "", err
	}
	cachePath, err := fm.CachePath(key)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Lstat(cachePath); statErr == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("ephemeral cache path %q is not a regular file", cachePath)
		}
		if err := capacity.Claim(cachePath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		} else {
			return cachePath, nil
		}
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}

	meta, err := fm.Head(ctx, key)
	if err != nil {
		return "", fmt.Errorf("reading size for %s: %w", key, err)
	}
	if err := capacity.Reserve(cachePath, meta.Size); err != nil {
		return "", err
	}
	localPath, err := fm.Download(ctx, key)
	if err != nil {
		_ = capacity.Release(cachePath)
		return "", err
	}
	if err := capacity.Commit(cachePath); err != nil {
		_ = fm.EvictCache(key)
		_ = capacity.Release(cachePath)
		return "", err
	}
	return localPath, nil
}

func validateEphemeralCacheKey(key string) error {
	if strings.Contains(key, "\\") || path.Clean(key) != key {
		return fmt.Errorf("invalid ephemeral key %q", key)
	}
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != "ephemeral" {
		return fmt.Errorf("release key %q must identify one file below ephemeral/", key)
	}
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid ephemeral key %q", key)
		}
	}
	return nil
}

func validateEphemeralCacheRequestID(requestID string) error {
	if requestID == "" || strings.ContainsAny(requestID, "/\\") || path.Clean(requestID) != requestID || requestID == "." || requestID == ".." {
		return fmt.Errorf("invalid ephemeral request ID %q", requestID)
	}
	return nil
}
