package nodes

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/xlog"
)

// S3NATSFileStager implements FileStager using S3 for storage and NATS
// request-reply for coordination with backend nodes. Both frontend and
// backend nodes share the same S3 bucket. The flow is:
//
//  1. Frontend uploads file to S3
//  2. Frontend sends NATS request to nodes.{nodeID}.files.ensure
//  3. Backend downloads from S3 to local cache, replies with local path
type S3NATSFileStager struct {
	fm   *storage.FileManager
	nats *messaging.Client
}

// NewS3NATSFileStager creates a new S3+NATS file stager.
func NewS3NATSFileStager(fm *storage.FileManager, nats *messaging.Client) *S3NATSFileStager {
	return &S3NATSFileStager{fm: fm, nats: nats}
}

// NATS request/reply message types

type fileEnsureRequest struct {
	Key string `json:"key"`
}

type fileEnsureReply struct {
	LocalPath string `json:"local_path"`
	Error     string `json:"error,omitempty"`
}

type fileStageRequest struct {
	LocalPath string `json:"local_path"`
	Key       string `json:"key"`
}

type fileStageReply struct {
	Key   string `json:"key"`
	Error string `json:"error,omitempty"`
}

type fileTempRequest struct{}

type fileTempReply struct {
	LocalPath string `json:"local_path"`
	Error     string `json:"error,omitempty"`
}

type fileListDirRequest struct {
	KeyPrefix string `json:"key_prefix"`
}

type fileListDirReply struct {
	Files []string `json:"files"`
	Error string   `json:"error,omitempty"`
}

// EnsureRemote uploads a local file to S3 (if not already there) and sends
// a NATS request-reply to the backend node to download it locally.
func (s *S3NATSFileStager) EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error) {
	// Upload to S3 if not already present
	exists, _ := s.fm.Exists(ctx, key)
	if !exists {
		if err := s.fm.Upload(ctx, key, localPath); err != nil {
			return "", fmt.Errorf("uploading %s to S3: %w", localPath, err)
		}
	}

	// Send NATS request-reply to backend
	subject := messaging.SubjectNodeFilesEnsure(nodeID)
	reply, err := messaging.RequestJSON[fileEnsureRequest, fileEnsureReply](s.nats, subject, fileEnsureRequest{Key: key}, 10*time.Minute)
	if err != nil {
		return "", err
	}
	if reply.Error != "" {
		return "", fmt.Errorf("backend ensure failed: %s", reply.Error)
	}

	xlog.Debug("File ensured on remote node", "nodeID", nodeID, "key", key, "remotePath", reply.LocalPath)
	return reply.LocalPath, nil
}

// FetchRemote tells the backend to upload a file to S3, then downloads it locally.
func (s *S3NATSFileStager) FetchRemote(ctx context.Context, nodeID, remotePath, localDst string) error {
	// Tell backend to upload to S3
	key := storage.EphemeralKey(remotePath, "fetch", "output")
	return s.fetchRemoteWithKey(ctx, nodeID, remotePath, key, localDst, true)
}

// FetchRemoteByKey tells the backend to upload a file (identified by key) to S3,
// then downloads it locally. The key is used as-is for S3 routing.
func (s *S3NATSFileStager) FetchRemoteByKey(ctx context.Context, nodeID, key, localDst string) error {
	// For S3 mode, we still need the remote path — derive it from the key.
	// The backend serves the file from its data dir based on the key prefix.
	remotePath := "/" + key // e.g. "/data/quantization/{jobID}/model.gguf"
	return s.fetchRemoteWithKey(ctx, nodeID, remotePath, key, localDst, true)
}

func (s *S3NATSFileStager) fetchRemoteWithKey(ctx context.Context, nodeID, remotePath, key, localDst string, cleanup bool) error {
	subject := messaging.SubjectNodeFilesStage(nodeID)
	reply, err := messaging.RequestJSON[fileStageRequest, fileStageReply](s.nats, subject, fileStageRequest{LocalPath: remotePath, Key: key}, 10*time.Minute)
	if err != nil {
		return err
	}
	if reply.Error != "" {
		return fmt.Errorf("backend stage failed: %s", reply.Error)
	}

	// Download from S3 to local cache
	cachedPath, err := s.fm.Download(ctx, key)
	if err != nil {
		return fmt.Errorf("downloading %s from S3: %w", key, err)
	}

	// Copy from cache to destination
	if err := copyFile(cachedPath, localDst); err != nil {
		return fmt.Errorf("copying to %s: %w", localDst, err)
	}

	// Cleanup ephemeral key
	if cleanup {
		s.fm.Delete(ctx, key)
	}

	return nil
}

// AllocRemoteTemp asks the backend to allocate a temp file via NATS request-reply.
func (s *S3NATSFileStager) AllocRemoteTemp(ctx context.Context, nodeID string) (string, error) {
	subject := messaging.SubjectNodeFilesTemp(nodeID)
	reply, err := messaging.RequestJSON[fileTempRequest, fileTempReply](s.nats, subject, fileTempRequest{}, 30*time.Second)
	if err != nil {
		return "", err
	}
	if reply.Error != "" {
		return "", fmt.Errorf("backend temp alloc failed: %s", reply.Error)
	}

	return reply.LocalPath, nil
}

func (s *S3NATSFileStager) ListRemoteDir(ctx context.Context, nodeID, keyPrefix string) ([]string, error) {
	subject := messaging.SubjectNodeFilesListDir(nodeID)
	reply, err := messaging.RequestJSON[fileListDirRequest, fileListDirReply](s.nats, subject, fileListDirRequest{KeyPrefix: keyPrefix}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("backend listdir failed: %s", reply.Error)
	}

	return reply.Files, nil
}

// StageRemoteToStore tells the backend to upload a local file to S3.
func (s *S3NATSFileStager) StageRemoteToStore(ctx context.Context, nodeID, remotePath, key string) error {
	subject := messaging.SubjectNodeFilesStage(nodeID)
	reply, err := messaging.RequestJSON[fileStageRequest, fileStageReply](s.nats, subject, fileStageRequest{LocalPath: remotePath, Key: key}, 10*time.Minute)
	if err != nil {
		return err
	}
	if reply.Error != "" {
		return fmt.Errorf("backend stage failed: %s", reply.Error)
	}

	return nil
}
