package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// S3FileStager implements FileStager using S3 for storage and the file verbs of
// the control plane for coordination with backend nodes. Both frontend and
// backend nodes share the same S3 bucket. The flow is:
//
//  1. Frontend uploads file to S3
//  2. Frontend sends the files.ensure verb to the node
//  3. Backend downloads from S3 to local cache, replies with local path
//
// How the verbs travel is the link: a NATS request to the subject of the node, or
// an HTTP request to the control plane of the worker through its tunnel. The
// stager is the same for both, so a deployment with shared object storage stages
// its files the same way on either carrier. The link maps a node that nothing can
// reach onto ErrNoRoute.
type S3FileStager struct {
	fm   *storage.FileManager
	link controlLink
}

// NewS3NATSFileStager creates a file stager that sends the file verbs over NATS.
func NewS3NATSFileStager(fm *storage.FileManager, nats messaging.MessagingClient) *S3FileStager {
	return &S3FileStager{fm: fm, link: &natsLink{bus: nats}}
}

// NewS3TunnelFileStager creates a file stager that sends the file verbs to the
// HTTP control plane of a worker, through whatever dialer client has.
func NewS3TunnelFileStager(fm *storage.FileManager, client *ControlClient) *S3FileStager {
	return &S3FileStager{fm: fm, link: &httpLink{client: client}}
}

// fileVerb sends one file verb to a node and decodes the reply.
func fileVerb[Req, Reply any](ctx context.Context, s *S3FileStager, nodeID, verb string, req Req, timeout time.Duration) (*Reply, error) {
	var reply Reply
	if err := s.link.request(ctx, nodeID, verb, req, &reply, timeout); err != nil {
		return nil, err
	}
	return &reply, nil
}

// EnsureRemote uploads a local file to S3 (if not already there) and sends
// a NATS request-reply to the backend node to download it locally.
func (s *S3FileStager) EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error) {
	// Upload to S3 if not already present
	exists, _ := s.fm.Exists(ctx, key)
	if !exists {
		// Wrap with progress reporting if a staging callback is available
		var progressFn storage.UploadProgressFunc
		if cb := StagingProgressFromContext(ctx); cb != nil {
			progressFn = func(fileName string, bytesWritten, totalBytes int64) {
				cb(fileName, bytesWritten, totalBytes)
			}
		}
		if err := s.fm.UploadWithProgress(ctx, key, localPath, progressFn); err != nil {
			return "", fmt.Errorf("uploading %s to S3: %w", localPath, err)
		}
	}

	// Ask the backend to download it
	reply, err := fileVerb[workerctl.FileEnsureRequest, workerctl.FileEnsureReply](ctx, s, nodeID, workerctl.VerbFilesEnsure, workerctl.FileEnsureRequest{Key: key}, 10*time.Minute)
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
func (s *S3FileStager) FetchRemote(ctx context.Context, nodeID, remotePath, localDst string) error {
	// Tell backend to upload to S3
	key := storage.EphemeralKey(remotePath, "fetch", "output")
	return s.fetchRemoteWithKey(ctx, nodeID, remotePath, key, localDst, true)
}

// FetchRemoteByKey tells the backend to upload a file (identified by key) to S3,
// then downloads it locally. The key is used as-is for S3 routing.
func (s *S3FileStager) FetchRemoteByKey(ctx context.Context, nodeID, key, localDst string) error {
	// For S3 mode, we still need the remote path — derive it from the key.
	// The backend serves the file from its data dir based on the key prefix.
	remotePath := "/" + key // e.g. "/data/quantization/{jobID}/model.gguf"
	return s.fetchRemoteWithKey(ctx, nodeID, remotePath, key, localDst, true)
}

func (s *S3FileStager) fetchRemoteWithKey(ctx context.Context, nodeID, remotePath, key, localDst string, cleanup bool) error {
	reply, err := fileVerb[workerctl.FileStageRequest, workerctl.FileStageReply](ctx, s, nodeID, workerctl.VerbFilesStage, workerctl.FileStageRequest{LocalPath: remotePath, Key: key}, 10*time.Minute)
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

// AllocRemoteTemp asks the backend to allocate a temp file.
func (s *S3FileStager) AllocRemoteTemp(ctx context.Context, nodeID string) (string, error) {
	reply, err := fileVerb[workerctl.FileTempRequest, workerctl.FileTempReply](ctx, s, nodeID, workerctl.VerbFilesTemp, workerctl.FileTempRequest{}, 30*time.Second)
	if err != nil {
		return "", err
	}
	if reply.Error != "" {
		return "", fmt.Errorf("backend temp alloc failed: %s", reply.Error)
	}

	return reply.LocalPath, nil
}

func (s *S3FileStager) ListRemoteDir(ctx context.Context, nodeID, keyPrefix string) ([]string, error) {
	reply, err := fileVerb[workerctl.FileListDirRequest, workerctl.FileListDirReply](ctx, s, nodeID, workerctl.VerbFilesListDir, workerctl.FileListDirRequest{KeyPrefix: keyPrefix}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("backend listdir failed: %s", reply.Error)
	}

	return reply.Files, nil
}

// StageRemoteToStore tells the backend to upload a local file to S3.
func (s *S3FileStager) StageRemoteToStore(ctx context.Context, nodeID, remotePath, key string) error {
	reply, err := fileVerb[workerctl.FileStageRequest, workerctl.FileStageReply](ctx, s, nodeID, workerctl.VerbFilesStage, workerctl.FileStageRequest{LocalPath: remotePath, Key: key}, 10*time.Minute)
	if err != nil {
		return err
	}
	if reply.Error != "" {
		return fmt.Errorf("backend stage failed: %s", reply.Error)
	}

	return nil
}

// ReleaseRemote evicts one exact ephemeral key from the worker before deleting
// the shared object.
func (s *S3FileStager) ReleaseRemote(ctx context.Context, nodeID, key string) error {
	if err := validateEphemeralReleaseKey(key); err != nil {
		return err
	}
	if err := s.releaseWorkerKeys(ctx, nodeID, workerctl.FileReleaseRequest{Key: key}); err != nil {
		return err
	}
	if err := s.fm.Delete(ctx, key); err != nil {
		return fmt.Errorf("deleting shared object %q: %w", key, err)
	}
	return nil
}

// ReleaseRemoteRequest evicts one inference's inputs with one round trip.
// A worker that only understands the exact-key payload returns an error, so the
// frontend retries each key during a rolling upgrade.
func (s *S3FileStager) ReleaseRemoteRequest(ctx context.Context, nodeID, requestID string, keys []string) error {
	if err := validateEphemeralRequestRelease(requestID, keys); err != nil {
		return err
	}
	if err := s.releaseWorkerKeys(ctx, nodeID, workerctl.FileReleaseRequest{RequestID: requestID}); err != nil {
		var fallbackErrors []error
		for _, key := range keys {
			if fallbackErr := s.ReleaseRemote(ctx, nodeID, key); fallbackErr != nil {
				fallbackErrors = append(fallbackErrors, fallbackErr)
			}
		}
		if fallbackErr := errors.Join(fallbackErrors...); fallbackErr != nil {
			return errors.Join(err, fmt.Errorf("exact-key release fallback: %w", fallbackErr))
		}
		return nil
	}
	var deleteErrors []error
	for _, key := range keys {
		if err := s.fm.Delete(ctx, key); err != nil {
			deleteErrors = append(deleteErrors, fmt.Errorf("deleting shared object %q: %w", key, err))
		}
	}
	return errors.Join(deleteErrors...)
}

func (s *S3FileStager) releaseWorkerKeys(ctx context.Context, nodeID string, request workerctl.FileReleaseRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timeout := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		timeout = min(timeout, remaining)
	}
	reply, err := fileVerb[workerctl.FileReleaseRequest, workerctl.FileReleaseReply](ctx, s, nodeID, workerctl.VerbFilesRelease, request, timeout)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if reply.Error != "" {
		return fmt.Errorf("backend release failed: %s", reply.Error)
	}
	return nil
}
