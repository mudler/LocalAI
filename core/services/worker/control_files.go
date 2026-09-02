package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// The worker's file-staging control plane: four verbs that move model and job
// artifacts between the object store both sides share and this worker's disk.
// They replace the four nodes.<id>.files.* NATS subjects.
//
// The reply shapes are the ones those subjects already carried, unchanged, so
// an operator reading the wire sees the same fields. What DID change is that a
// listing is no longer sized against a bus payload: it is a response body the
// caller is already reading, which is why nothing here truncates one.
//
// The 200-with-error-field shape is the same one the lifecycle verbs take, and
// for the same reason: "that file is not there" is the worker's own ANSWER, and
// a reap guard may act on it, while a non-2xx is this frontend failing to reach
// the worker, which nothing may act on. A handler that answered 500 for a
// failed upload would move its verdict into the bucket reserved for a broken
// link.

// The file-staging reply bodies. They mirror the frontend's decode structs in
// core/services/nodes/file_stager_s3.go field for field; the two are written
// separately because neither package may import the other, and the roundtrip
// spec is what holds them together.
type fileEnsureReply struct {
	LocalPath string `json:"local_path,omitempty"`
	Error     string `json:"error,omitempty"`
}

type fileStageReply struct {
	Key   string `json:"key,omitempty"`
	Error string `json:"error,omitempty"`
}

type fileTempReply struct {
	LocalPath string `json:"local_path,omitempty"`
	Error     string `json:"error,omitempty"`
}

type fileListDirReply struct {
	Files []string `json:"files,omitempty"`
	Error string   `json:"error,omitempty"`
}

// stagingCacheDir is the one place the worker's staging cache directory is
// derived from its configuration.
//
// One place and not two, because the FileManager caches INTO this directory and
// the listdir and temp verbs resolve paths AGAINST it. Two derivations that
// drifted would give a worker that downloads a file to one directory and then
// reports it missing from another, and both halves would still be self
// consistent.
func (cfg *Config) stagingCacheDir() string {
	return filepath.Join(cfg.ModelsPath, "..", "cache")
}

// stagingDataDir is where keys under storage.DataKeyPrefix resolve, and it is
// derived from the cache directory for the same single-source reason.
func (cfg *Config) stagingDataDir() string {
	return filepath.Join(cfg.stagingCacheDir(), "..", "data")
}

// NewStagingFileManager builds the FileManager the file-staging verbs serve
// from, over the same object store the frontend uses.
//
// It returns an error rather than degrading, because a worker whose deployment
// asked for object storage and could not reach it would otherwise mount four
// verbs that fail every call, which the frontend cannot tell from a worker out
// of disk.
func (cfg *Config) NewStagingFileManager(ctx context.Context) (*storage.FileManager, error) {
	s3Store, err := storage.NewS3Store(ctx, storage.S3Config{
		Endpoint:        cfg.StorageURL,
		Region:          cfg.StorageRegion,
		Bucket:          cfg.StorageBucket,
		AccessKeyID:     cfg.StorageAccessKey,
		SecretAccessKey: cfg.StorageSecretKey,
		ForcePathStyle:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("initializing S3 store: %w", err)
	}
	fm, err := storage.NewFileManager(s3Store, cfg.stagingCacheDir())
	if err != nil {
		return nil, fmt.Errorf("initializing file manager: %w", err)
	}
	return fm, nil
}

// RegisterFileControlRoutes mounts the four file-staging verbs on mux.
//
// The caller is responsible for putting mux behind authentication; see
// nodes.AuthenticatedRoutes, which is how the worker mounts this so the file
// verbs share one bearer check with the lifecycle verbs and the file routes
// rather than growing a third one.
func (cfg *Config) RegisterFileControlRoutes(mux *http.ServeMux, fm *storage.FileManager) {
	cacheDir := cfg.stagingCacheDir()

	// files.ensure: download an object-store key into this worker's cache and
	// say where it landed.
	//
	// It takes the caller's context. Nothing is terminated and no resource is
	// held if it is abandoned half way: the download simply stops, and the next
	// attempt starts over. So the caller's budget is the operation's budget.
	postControlVerb(mux, workerctl.PathFilesEnsure, func(ctx context.Context, body []byte) (any, error) {
		var req struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("invalid files.ensure request: %w", err)
		}
		localPath, err := fm.Download(ctx, req.Key)
		if err != nil {
			xlog.Error("File ensure failed", "key", req.Key, "error", err)
			return fileEnsureReply{Error: err.Error()}, nil
		}
		xlog.Debug("File ensured locally", "key", req.Key, "path", localPath)
		return fileEnsureReply{LocalPath: localPath}, nil
	})

	// files.stage: upload one of this worker's files to the object store.
	//
	// The path allow-list is what keeps this verb from being an exfiltration
	// primitive: the token holder can name any absolute path, so only the
	// directories this worker stages out of are served.
	postControlVerb(mux, workerctl.PathFilesStage, func(ctx context.Context, body []byte) (any, error) {
		var req struct {
			LocalPath string `json:"local_path"`
			Key       string `json:"key"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("invalid files.stage request: %w", err)
		}
		allowedDirs := []string{cacheDir}
		if cfg.ModelsPath != "" {
			allowedDirs = append(allowedDirs, cfg.ModelsPath)
		}
		if !isPathAllowed(req.LocalPath, allowedDirs) {
			return fileStageReply{Error: "path outside allowed directories"}, nil
		}
		if err := fm.Upload(ctx, req.Key, req.LocalPath); err != nil {
			xlog.Error("File stage failed", "path", req.LocalPath, "key", req.Key, "error", err)
			return fileStageReply{Error: err.Error()}, nil
		}
		xlog.Debug("File staged to the object store", "path", req.LocalPath, "key", req.Key)
		return fileStageReply{Key: req.Key}, nil
	})

	// files.temp: allocate an empty file the frontend may then upload into.
	postControlVerb(mux, workerctl.PathFilesTemp, func(context.Context, []byte) (any, error) {
		tmpDir := filepath.Join(cacheDir, "staging-tmp")
		if err := os.MkdirAll(tmpDir, 0750); err != nil {
			return fileTempReply{Error: fmt.Sprintf("creating temp dir: %v", err)}, nil
		}
		f, err := os.CreateTemp(tmpDir, "localai-staging-*.tmp")
		if err != nil {
			return fileTempReply{Error: fmt.Sprintf("creating temp file: %v", err)}, nil
		}
		localPath := f.Name()
		if err := f.Close(); err != nil {
			return fileTempReply{Error: fmt.Sprintf("closing temp file: %v", err)}, nil
		}
		xlog.Debug("Allocated temp file", "path", localPath)
		return fileTempReply{LocalPath: localPath}, nil
	})

	// files.listdir: the relative paths of every file under one key prefix.
	//
	// Nothing here caps the answer. Over NATS the reply had to fit a payload
	// the bus was willing to carry, and a wide model directory was the case
	// that risked it; over HTTP the listing is written into a body the caller
	// is already reading, so its size is no longer a property of the carrier. A
	// cap would silently return a SHORT listing, which the frontend reads as
	// files that are not there.
	postControlVerb(mux, workerctl.PathFilesListDir, func(ctx context.Context, body []byte) (any, error) {
		var req struct {
			KeyPrefix string `json:"key_prefix"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("invalid files.listdir request: %w", err)
		}
		dirPath, ok := cfg.resolveStagingDir(req.KeyPrefix)
		if !ok {
			return fileListDirReply{Error: "invalid key prefix"}, nil
		}
		files, err := listStagedFiles(ctx, dirPath)
		if err != nil {
			xlog.Error("Failed to list staged files", "keyPrefix", req.KeyPrefix, "dirPath", dirPath, "error", err)
			return fileListDirReply{Error: err.Error()}, nil
		}
		xlog.Debug("Listed remote dir", "keyPrefix", req.KeyPrefix, "dirPath", dirPath, "fileCount", len(files))
		return fileListDirReply{Files: files}, nil
	})
}

// resolveStagingDir maps a storage key prefix onto the local directory it names,
// and reports whether that directory is one this worker serves.
//
// The second return is not an error string on purpose: a prefix that climbs out
// of the served directories is refused before anything touches the filesystem,
// so a crafted key_prefix cannot turn this verb into a directory reader for the
// whole host.
func (cfg *Config) resolveStagingDir(keyPrefix string) (string, bool) {
	cacheDir := cfg.stagingCacheDir()
	dataDir := cfg.stagingDataDir()

	dirPath := filepath.Join(cacheDir, keyPrefix)
	if rel, ok := strings.CutPrefix(keyPrefix, storage.ModelKeyPrefix); ok && cfg.ModelsPath != "" {
		dirPath = filepath.Join(cfg.ModelsPath, rel)
	} else if rel, ok := strings.CutPrefix(keyPrefix, storage.DataKeyPrefix); ok {
		dirPath = filepath.Join(dataDir, rel)
	}

	dirPath = filepath.Clean(dirPath)
	cleanCache := filepath.Clean(cacheDir)
	cleanModels := filepath.Clean(cfg.ModelsPath)
	cleanData := filepath.Clean(dataDir)
	within := func(root string) bool {
		return dirPath == root || strings.HasPrefix(dirPath, root+string(filepath.Separator))
	}
	if within(cleanCache) || (cleanModels != "." && within(cleanModels)) || within(cleanData) {
		return dirPath, true
	}
	return "", false
}

// listStagedFiles walks dirPath and returns every file's path relative to it.
//
// The walk honours ctx because a very wide directory is real work, and a caller
// that has already given up should not keep this worker stat-ing files. The
// context error is returned as the walk's error, so it travels back as a
// FAILURE of the listing rather than as an empty listing, which the frontend
// would read as a directory with nothing in it.
func listStagedFiles(ctx context.Context, dirPath string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dirPath, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
