package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
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

// invalidFileRequest is the refusal every body-reading file verb sends for a
// body it cannot decode; the frontend matches on the error text only.
const invalidFileRequest = "invalid request"

// registerFileStagingVerbs serves the file staging verbs, backed by the
// configured object storage.
func (cfg *Config) registerFileStagingVerbs(srv controlServer, capacity *EphemeralCapacityGuard, supervisors ...*backendSupervisor) error {
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
	if err := registerFileReleaseVerb(srv, fm, cacheDir, capacity); err != nil {
		return err
	}

	v := &fileStagingVerbs{cfg: cfg, fm: fm, cacheDir: cacheDir, capacity: capacity}
	if len(supervisors) > 0 {
		v.supervisor = supervisors[0]
	}
	if err := srv.handle(verbFilesEnsure, unary(decodeJSON[workerctl.FileEnsureRequest], func(error) workerctl.FileEnsureReply {
		return workerctl.FileEnsureReply{Error: invalidFileRequest}
	}, v.ensure)); err != nil {
		return err
	}
	if err := srv.handle(verbFilesStage, unary(decodeJSON[workerctl.FileStageRequest], func(error) workerctl.FileStageReply {
		return workerctl.FileStageReply{Error: invalidFileRequest}
	}, v.stage)); err != nil {
		return err
	}
	if err := srv.handle(verbFilesTemp, unary(ignoreBody[workerctl.FileTempRequest], refuseNever[workerctl.FileTempReply], v.temp)); err != nil {
		return err
	}
	if err := srv.handle(verbFilesListDir, unary(decodeJSON[workerctl.FileListDirRequest], func(error) workerctl.FileListDirReply {
		return workerctl.FileListDirReply{Error: invalidFileRequest}
	}, v.listDir)); err != nil {
		return err
	}

	xlog.Info("Serving file staging verbs")
	return nil
}

// fileStagingVerbs holds what the file staging verbs share for the lifetime
// of the worker.
type fileStagingVerbs struct {
	supervisor *backendSupervisor
	cfg        *Config
	fm         *storage.FileManager
	cacheDir   string
	capacity   *EphemeralCapacityGuard
	// ensureGroup lives as long as the verbs so concurrent ensures of one key
	// share a single download and a single capacity reservation.
	ensureGroup singleflight.Group
}

// ensure downloads an object storage key into the local cache.
func (v *fileStagingVerbs) ensure(ctx context.Context, req workerctl.FileEnsureRequest) workerctl.FileEnsureReply {
	if req.Operation != nil {
		if v.supervisor == nil {
			return workerctl.FileEnsureReply{Error: "operation tracking unavailable"}
		}
		if err := v.supervisor.beginStaging(req.Operation, req.ProcessKey); err != nil {
			return workerctl.FileEnsureReply{Error: err.Error()}
		}
		defer v.supervisor.endStaging(req.ProcessKey)
	}
	value, err, _ := v.ensureGroup.Do(req.Key, func() (any, error) {
		return ensureWorkerFile(ctx, v.fm, v.capacity, req.Key)
	})
	if err != nil {
		xlog.Error("File ensure failed", "key", req.Key, "error", err)
		return workerctl.FileEnsureReply{Error: err.Error()}
	}
	localPath, ok := value.(string)
	if !ok {
		return workerctl.FileEnsureReply{Error: fmt.Sprintf("unexpected file ensure result %T", value)}
	}

	xlog.Debug("File ensured locally", "key", req.Key, "path", localPath)
	return workerctl.FileEnsureReply{LocalPath: localPath}
}

// stage uploads a local file to object storage.
func (v *fileStagingVerbs) stage(ctx context.Context, req workerctl.FileStageRequest) workerctl.FileStageReply {
	if req.Operation != nil {
		if v.supervisor == nil {
			return workerctl.FileStageReply{Error: "operation tracking unavailable"}
		}
		if err := v.supervisor.beginStaging(req.Operation, req.ProcessKey); err != nil {
			return workerctl.FileStageReply{Error: err.Error()}
		}
		defer v.supervisor.endStaging(req.ProcessKey)
	}
	allowedDirs := []string{v.cacheDir}
	if v.cfg.ModelsPath != "" {
		allowedDirs = append(allowedDirs, v.cfg.ModelsPath)
	}
	if !isPathAllowed(req.LocalPath, allowedDirs) {
		return workerctl.FileStageReply{Error: "path outside allowed directories"}
	}

	if err := v.fm.Upload(ctx, req.Key, req.LocalPath); err != nil {
		xlog.Error("File stage failed", "path", req.LocalPath, "key", req.Key, "error", err)
		return workerctl.FileStageReply{Error: err.Error()}
	}

	xlog.Debug("File staged to S3", "path", req.LocalPath, "key", req.Key)
	return workerctl.FileStageReply{Key: req.Key}
}

// temp allocates an empty temporary file in the staging cache.
func (v *fileStagingVerbs) temp(context.Context, workerctl.FileTempRequest) workerctl.FileTempReply {
	tmpDir := filepath.Join(v.cacheDir, "staging-tmp")
	if err := os.MkdirAll(tmpDir, 0750); err != nil {
		return workerctl.FileTempReply{Error: fmt.Sprintf("creating temp dir: %v", err)}
	}

	f, err := os.CreateTemp(tmpDir, "localai-staging-*.tmp")
	if err != nil {
		return workerctl.FileTempReply{Error: fmt.Sprintf("creating temp file: %v", err)}
	}
	localPath := f.Name()
	if err := f.Close(); err != nil {
		return workerctl.FileTempReply{Error: fmt.Sprintf("closing temp file: %v", err)}
	}

	xlog.Debug("Allocated temp file", "path", localPath)
	return workerctl.FileTempReply{LocalPath: localPath}
}

// listDir lists the files below a key prefix, relative to its directory.
func (v *fileStagingVerbs) listDir(_ context.Context, req workerctl.FileListDirRequest) workerctl.FileListDirReply {
	cacheDir := v.cacheDir
	// Resolve key prefix to local directory
	dirPath := filepath.Join(cacheDir, req.KeyPrefix)
	if rel, ok := strings.CutPrefix(req.KeyPrefix, storage.ModelKeyPrefix); ok && v.cfg.ModelsPath != "" {
		dirPath = filepath.Join(v.cfg.ModelsPath, rel)
	} else if rel, ok := strings.CutPrefix(req.KeyPrefix, storage.DataKeyPrefix); ok {
		dirPath = filepath.Join(cacheDir, "..", "data", rel)
	}

	// Sanitize to prevent directory traversal via crafted key_prefix
	dirPath = filepath.Clean(dirPath)
	cleanCache := filepath.Clean(cacheDir)
	cleanModels := filepath.Clean(v.cfg.ModelsPath)
	cleanData := filepath.Clean(filepath.Join(cacheDir, "..", "data"))
	if !(strings.HasPrefix(dirPath, cleanCache+string(filepath.Separator)) ||
		dirPath == cleanCache ||
		(cleanModels != "." && strings.HasPrefix(dirPath, cleanModels+string(filepath.Separator))) ||
		dirPath == cleanModels ||
		strings.HasPrefix(dirPath, cleanData+string(filepath.Separator)) ||
		dirPath == cleanData) {
		return workerctl.FileListDirReply{Error: "invalid key prefix"}
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
		return workerctl.FileListDirReply{Error: err.Error()}
	}

	xlog.Debug("Listed remote dir", "keyPrefix", req.KeyPrefix, "dirPath", dirPath, "fileCount", len(files))
	return workerctl.FileListDirReply{Files: files}
}

// registerFileReleaseVerb serves files.release, which evicts one exact
// ephemeral key or every key staged for one request. capacity may be nil.
func registerFileReleaseVerb(srv controlServer, fm *storage.FileManager, cacheDir string, capacity *EphemeralCapacityGuard) error {
	return srv.handle(verbFilesRelease, unary(decodeJSON[workerctl.FileReleaseRequest], func(error) workerctl.FileReleaseReply {
		return workerctl.FileReleaseReply{Error: invalidFileRequest}
	}, func(ctx context.Context, req workerctl.FileReleaseRequest) workerctl.FileReleaseReply {
		var err error
		if req.RequestID != "" {
			// Beginning a request release can wait on the capacity guard; the
			// bound keeps one stuck request from holding up the verb.
			releaseCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = releaseEphemeralCacheRequest(releaseCtx, cacheDir, req.RequestID, capacity)
			cancel()
		} else {
			cachePath, cacheErr := fm.CachePath(req.Key)
			err = cacheErr
			if err == nil {
				err = releaseEphemeralCachePathWithCapacity(cacheDir, req.Key, cachePath, capacity)
			}
		}
		if err != nil {
			return workerctl.FileReleaseReply{Error: err.Error()}
		}
		return workerctl.FileReleaseReply{}
	}))
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
