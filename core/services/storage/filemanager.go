package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/xlog"
	"golang.org/x/sync/singleflight"
)

// FileManager provides a unified file access layer that abstracts over
// local filesystem and object storage (S3). In distributed mode, files
// are stored in S3 with local caching on each node. In single-node mode,
// it operates directly on the filesystem.
type FileManager struct {
	store    ObjectStore
	cacheDir string // local cache directory for downloaded files
	flight singleflight.Group
}

// NewFileManager creates a new FileManager.
// If store is nil, all operations fall through to local filesystem only.
func NewFileManager(store ObjectStore, cacheDir string) (*FileManager, error) {
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0750); err != nil {
			return nil, fmt.Errorf("creating cache directory %s: %w", cacheDir, err)
		}
	}
	return &FileManager{
		store:    store,
		cacheDir: cacheDir,
	}, nil
}

// Upload stores a file in object storage under the given key.
// The file is read from the local path.
func (fm *FileManager) Upload(ctx context.Context, key, localPath string) error {
	if fm.store == nil {
		return nil // no-op in single-node mode
	}

	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("opening %s for upload: %w", localPath, err)
	}
	defer f.Close()

	if err := fm.store.Put(ctx, key, f); err != nil {
		return fmt.Errorf("uploading %s to %s: %w", localPath, key, err)
	}

	xlog.Debug("Uploaded file to object storage", "key", key, "localPath", localPath)
	return nil
}

// Download retrieves a file from object storage and caches it locally.
// Returns the local file path. If the file is already cached, returns immediately.
func (fm *FileManager) Download(ctx context.Context, key string) (string, error) {
	if fm.store == nil {
		return "", fmt.Errorf("no object store configured")
	}

	localPath := fm.cachePath(key)

	// Fast path: check local cache without any locking
	if _, err := os.Stat(localPath); err == nil {
		xlog.Debug("File found in local cache", "key", key, "path", localPath)
		return localPath, nil
	}

	// singleflight deduplicates concurrent downloads for the same key
	v, err, _ := fm.flight.Do(key, func() (any, error) {
		// Re-check cache (another goroutine may have just finished)
		if _, err := os.Stat(localPath); err == nil {
			return localPath, nil
		}

		r, err := fm.store.Get(ctx, key)
		if err != nil {
			return "", fmt.Errorf("downloading %s: %w", key, err)
		}
		defer r.Close()

		if err := os.MkdirAll(filepath.Dir(localPath), 0750); err != nil {
			return "", fmt.Errorf("creating cache dir for %s: %w", key, err)
		}

		tmpPath := localPath + ".tmp"
		f, err := os.Create(tmpPath)
		if err != nil {
			return "", fmt.Errorf("creating temp file for %s: %w", key, err)
		}

		if _, err := io.Copy(f, r); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return "", fmt.Errorf("writing %s to cache: %w", key, err)
		}
		f.Close()

		if err := os.Rename(tmpPath, localPath); err != nil {
			os.Remove(tmpPath)
			return "", fmt.Errorf("renaming temp file for %s: %w", key, err)
		}

		xlog.Debug("Downloaded file from object storage", "key", key, "path", localPath)
		return localPath, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// Head returns metadata about an object in storage without downloading it.
func (fm *FileManager) Head(ctx context.Context, key string) (*ObjectMeta, error) {
	if fm.store == nil {
		return nil, fmt.Errorf("no object store configured")
	}
	return fm.store.Head(ctx, key)
}

// Exists checks if a file exists in object storage.
func (fm *FileManager) Exists(ctx context.Context, key string) (bool, error) {
	if fm.store == nil {
		return false, nil
	}
	return fm.store.Exists(ctx, key)
}

// Delete removes a file from object storage and the local cache.
func (fm *FileManager) Delete(ctx context.Context, key string) error {
	if fm.store == nil {
		return nil
	}

	// Remove from local cache
	localPath := fm.cachePath(key)
	os.Remove(localPath)

	return fm.store.Delete(ctx, key)
}

// List returns keys matching the given prefix from object storage.
func (fm *FileManager) List(ctx context.Context, prefix string) ([]string, error) {
	if fm.store == nil {
		return nil, nil
	}
	return fm.store.List(ctx, prefix)
}

// CacheExists checks if a file is in the local cache.
func (fm *FileManager) CacheExists(key string) bool {
	_, err := os.Stat(fm.cachePath(key))
	return err == nil
}

// CachePath returns the local cache path for a key.
func (fm *FileManager) CachePath(key string) string {
	return fm.cachePath(key)
}

// EvictCache removes a file from the local cache (but keeps it in object storage).
func (fm *FileManager) EvictCache(key string) error {
	return os.Remove(fm.cachePath(key))
}

// IsConfigured returns true if an object store is configured.
func (fm *FileManager) IsConfigured() bool {
	return fm.store != nil
}

func (fm *FileManager) cachePath(key string) string {
	// Convert key to safe filesystem path
	safe := strings.ReplaceAll(key, "/", string(filepath.Separator))
	return filepath.Join(fm.cacheDir, safe)
}

// EphemeralKey returns an S3 key for ephemeral (per-request) files.
func EphemeralKey(requestID, category, filename string) string {
	return "ephemeral/" + category + "/" + requestID + "/" + filename
}

// --- Namespace helpers for organizing files in object storage ---

// ModelKeyPrefix is the key prefix used for model files in object storage
// and HTTP file transfer routing.
const ModelKeyPrefix = "models/"

// DataKeyPrefix is the key prefix used for data files (e.g. quantization output)
// in object storage and HTTP file transfer routing.
const DataKeyPrefix = "data/"

// ModelKey returns the object storage key for a model file.
func ModelKey(modelName string) string {
	return ModelKeyPrefix + modelName
}

// DataKey returns the object storage key for a data file.
func DataKey(name string) string {
	return DataKeyPrefix + name
}

// UserAssetKey returns the object storage key for a user asset.
func UserAssetKey(userID, filename string) string {
	return "users/" + userID + "/assets/" + filename
}

// UserOutputKey returns the object storage key for a user output file.
func UserOutputKey(userID, filename string) string {
	return "users/" + userID + "/outputs/" + filename
}

// FineTuneDatasetKey returns the object storage key for a fine-tune dataset.
func FineTuneDatasetKey(jobID, filename string) string {
	return "finetune/datasets/" + jobID + "/" + filename
}

// FineTuneCheckpointKey returns the object storage key for a fine-tune checkpoint.
func FineTuneCheckpointKey(jobID, checkpoint string) string {
	return "finetune/" + jobID + "/checkpoints/" + checkpoint
}

// SkillKey returns the object storage key for a skill file.
func SkillKey(userID, skillName, filename string) string {
	if userID != "" {
		return "skills/" + userID + "/" + skillName + "/" + filename
	}
	return "skills/global/" + skillName + "/" + filename
}
