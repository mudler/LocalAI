package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/pkg/safefile"
	"github.com/mudler/xlog"
	"golang.org/x/sync/singleflight"
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
		if strings.HasPrefix(resolved, absDir+string(filepath.Separator)) || resolved == absDir {
			return true
		}
	}
	return false
}

// subscribeFileStaging subscribes to NATS file staging subjects for this node.
func (cfg *Config) subscribeFileStaging(natsClient messaging.MessagingClient, nodeID string, capacity *EphemeralCapacityGuard) error {
	// Create FileManager with same S3 config as the frontend
	// TODO: propagate a caller-provided context once Config carries one
	s3Store, err := storage.NewS3Store(context.Background(), storage.S3Config{
		Endpoint:        cfg.StorageURL,
		Region:          cfg.StorageRegion,
		Bucket:          cfg.StorageBucket,
		AccessKeyID:     cfg.StorageAccessKey,
		SecretAccessKey: cfg.StorageSecretKey,
		ForcePathStyle:  true,
	})
	if err != nil {
		return fmt.Errorf("initializing S3 store: %w", err)
	}

	cacheDir := filepath.Join(cfg.ModelsPath, "..", "cache")
	fm, err := storage.NewFileManager(s3Store, cacheDir)
	if err != nil {
		return fmt.Errorf("initializing file manager: %w", err)
	}
	if err := subscribeFileReleaseWithCapacity(natsClient, nodeID, fm, cacheDir, capacity); err != nil {
		return err
	}
	var ensureGroup singleflight.Group

	// Subscribe: files.ensure — download S3 key to local, reply with local path
	if _, err := natsClient.SubscribeReply(messaging.SubjectNodeFilesEnsure(nodeID), func(data []byte, reply func([]byte)) {
		var req struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			replyJSON(reply, map[string]string{"error": "invalid request"})
			return
		}

		value, err, _ := ensureGroup.Do(req.Key, func() (any, error) {
			return ensureWorkerFile(context.Background(), fm, capacity, req.Key)
		})
		if err != nil {
			xlog.Error("File ensure failed", "key", req.Key, "error", err)
			replyJSON(reply, map[string]string{"error": err.Error()})
			return
		}
		localPath, ok := value.(string)
		if !ok {
			replyJSON(reply, map[string]string{"error": fmt.Sprintf("unexpected file ensure result %T", value)})
			return
		}

		xlog.Debug("File ensured locally", "key", req.Key, "path", localPath)
		replyJSON(reply, map[string]string{"local_path": localPath})
	}); err != nil {
		return fmt.Errorf("subscribing to files.ensure events: %w", err)
	}

	// Subscribe: files.stage — upload local path to S3, reply with key
	if _, err := natsClient.SubscribeReply(messaging.SubjectNodeFilesStage(nodeID), func(data []byte, reply func([]byte)) {
		var req struct {
			LocalPath string `json:"local_path"`
			Key       string `json:"key"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			replyJSON(reply, map[string]string{"error": "invalid request"})
			return
		}

		allowedDirs := []string{cacheDir}
		if cfg.ModelsPath != "" {
			allowedDirs = append(allowedDirs, cfg.ModelsPath)
		}
		if !isPathAllowed(req.LocalPath, allowedDirs) {
			replyJSON(reply, map[string]string{"error": "path outside allowed directories"})
			return
		}

		if err := fm.Upload(context.Background(), req.Key, req.LocalPath); err != nil {
			xlog.Error("File stage failed", "path", req.LocalPath, "key", req.Key, "error", err)
			replyJSON(reply, map[string]string{"error": err.Error()})
			return
		}

		xlog.Debug("File staged to S3", "path", req.LocalPath, "key", req.Key)
		replyJSON(reply, map[string]string{"key": req.Key})
	}); err != nil {
		return fmt.Errorf("subscribing to files.stage events: %w", err)
	}

	// Subscribe: files.temp — allocate temp file, reply with local path
	if _, err := natsClient.SubscribeReply(messaging.SubjectNodeFilesTemp(nodeID), func(data []byte, reply func([]byte)) {
		tmpDir := filepath.Join(cacheDir, "staging-tmp")
		if err := os.MkdirAll(tmpDir, 0750); err != nil {
			replyJSON(reply, map[string]string{"error": fmt.Sprintf("creating temp dir: %v", err)})
			return
		}

		f, err := os.CreateTemp(tmpDir, "localai-staging-*.tmp")
		if err != nil {
			replyJSON(reply, map[string]string{"error": fmt.Sprintf("creating temp file: %v", err)})
			return
		}
		localPath := f.Name()
		if err := f.Close(); err != nil {
			replyJSON(reply, map[string]string{"error": fmt.Sprintf("closing temp file: %v", err)})
			return
		}

		xlog.Debug("Allocated temp file", "path", localPath)
		replyJSON(reply, map[string]string{"local_path": localPath})
	}); err != nil {
		return fmt.Errorf("subscribing to files.temp events: %w", err)
	}

	// Subscribe: files.listdir — list files in a local directory, reply with relative paths
	if _, err := natsClient.SubscribeReply(messaging.SubjectNodeFilesListDir(nodeID), func(data []byte, reply func([]byte)) {
		var req struct {
			KeyPrefix string `json:"key_prefix"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			replyJSON(reply, map[string]any{"error": "invalid request"})
			return
		}

		// Resolve key prefix to local directory
		dirPath := filepath.Join(cacheDir, req.KeyPrefix)
		if rel, ok := strings.CutPrefix(req.KeyPrefix, storage.ModelKeyPrefix); ok && cfg.ModelsPath != "" {
			dirPath = filepath.Join(cfg.ModelsPath, rel)
		} else if rel, ok := strings.CutPrefix(req.KeyPrefix, storage.DataKeyPrefix); ok {
			dirPath = filepath.Join(cacheDir, "..", "data", rel)
		}

		// Sanitize to prevent directory traversal via crafted key_prefix
		dirPath = filepath.Clean(dirPath)
		cleanCache := filepath.Clean(cacheDir)
		cleanModels := filepath.Clean(cfg.ModelsPath)
		cleanData := filepath.Clean(filepath.Join(cacheDir, "..", "data"))
		if !(strings.HasPrefix(dirPath, cleanCache+string(filepath.Separator)) ||
			dirPath == cleanCache ||
			(cleanModels != "." && strings.HasPrefix(dirPath, cleanModels+string(filepath.Separator))) ||
			dirPath == cleanModels ||
			strings.HasPrefix(dirPath, cleanData+string(filepath.Separator)) ||
			dirPath == cleanData) {
			replyJSON(reply, map[string]any{"error": "invalid key prefix"})
			return
		}

		var files []string
		if err := filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				rel, err := filepath.Rel(dirPath, path)
				if err != nil {
					return err
				}
				files = append(files, rel)
			}
			return nil
		}); err != nil {
			xlog.Error("Failed to list staged files", "keyPrefix", req.KeyPrefix, "dirPath", dirPath, "error", err)
			replyJSON(reply, map[string]any{"error": err.Error()})
			return
		}

		xlog.Debug("Listed remote dir", "keyPrefix", req.KeyPrefix, "dirPath", dirPath, "fileCount", len(files))
		replyJSON(reply, map[string]any{"files": files})
	}); err != nil {
		return fmt.Errorf("subscribing to files.listdir events: %w", err)
	}

	xlog.Info("Subscribed to file staging NATS subjects", "nodeID", nodeID)
	return nil
}

func subscribeFileRelease(natsClient messaging.MessagingClient, nodeID string, fm *storage.FileManager, cacheDir string) error {
	return subscribeFileReleaseWithCapacity(natsClient, nodeID, fm, cacheDir, nil)
}

func subscribeFileReleaseWithCapacity(natsClient messaging.MessagingClient, nodeID string, fm *storage.FileManager, cacheDir string, capacity *EphemeralCapacityGuard) error {
	if _, err := natsClient.SubscribeReply(messaging.SubjectNodeFilesRelease(nodeID), func(data []byte, reply func([]byte)) {
		var req struct {
			Key       string `json:"key"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			replyJSON(reply, map[string]string{"error": "invalid request"})
			return
		}
		var err error
		if req.RequestID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err = releaseEphemeralCacheRequest(ctx, cacheDir, req.RequestID, capacity)
			cancel()
		} else {
			cachePath, cacheErr := fm.CachePath(req.Key)
			err = cacheErr
			if err == nil {
				err = releaseEphemeralCachePathWithCapacity(cacheDir, req.Key, cachePath, capacity)
			}
		}
		if err != nil {
			replyJSON(reply, map[string]string{"error": err.Error()})
			return
		}
		replyJSON(reply, map[string]string{})
	}); err != nil {
		return fmt.Errorf("subscribing to files.release events: %w", err)
	}
	return nil
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
