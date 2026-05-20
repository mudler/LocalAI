package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/xlog"
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
func (cfg *Config) subscribeFileStaging(natsClient messaging.MessagingClient, nodeID string) error {
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

	// Subscribe: files.ensure — download S3 key to local, reply with local path
	natsClient.SubscribeReply(messaging.SubjectNodeFilesEnsure(nodeID), func(data []byte, reply func([]byte)) {
		var req struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			replyJSON(reply, map[string]string{"error": "invalid request"})
			return
		}

		localPath, err := fm.Download(context.Background(), req.Key)
		if err != nil {
			xlog.Error("File ensure failed", "key", req.Key, "error", err)
			replyJSON(reply, map[string]string{"error": err.Error()})
			return
		}

		xlog.Debug("File ensured locally", "key", req.Key, "path", localPath)
		replyJSON(reply, map[string]string{"local_path": localPath})
	})

	// Subscribe: files.stage — upload local path to S3, reply with key
	natsClient.SubscribeReply(messaging.SubjectNodeFilesStage(nodeID), func(data []byte, reply func([]byte)) {
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
	})

	// Subscribe: files.temp — allocate temp file, reply with local path
	natsClient.SubscribeReply(messaging.SubjectNodeFilesTemp(nodeID), func(data []byte, reply func([]byte)) {
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
		f.Close()

		xlog.Debug("Allocated temp file", "path", localPath)
		replyJSON(reply, map[string]string{"local_path": localPath})
	})

	// Subscribe: files.listdir — list files in a local directory, reply with relative paths
	natsClient.SubscribeReply(messaging.SubjectNodeFilesListDir(nodeID), func(data []byte, reply func([]byte)) {
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
		filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				rel, err := filepath.Rel(dirPath, path)
				if err == nil {
					files = append(files, rel)
				}
			}
			return nil
		})

		xlog.Debug("Listed remote dir", "keyPrefix", req.KeyPrefix, "dirPath", dirPath, "fileCount", len(files))
		replyJSON(reply, map[string]any{"files": files})
	})

	xlog.Info("Subscribed to file staging NATS subjects", "nodeID", nodeID)
	return nil
}
