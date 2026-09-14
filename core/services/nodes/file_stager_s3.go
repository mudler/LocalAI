package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// S3FileStager implements FileStager using an object store for the bytes and
// the worker's tunnelled control plane for coordination. Both frontend and
// worker share the same bucket. The flow is:
//
//  1. Frontend uploads the file to the object store
//  2. Frontend calls POST /v1/control/files/ensure on the worker's tunnel
//  3. Worker downloads from the store to its local cache and replies with the
//     local path
type S3FileStager struct {
	fm      *storage.FileManager
	control *ControlClient
}

// NewS3FileStager creates a file stager that moves bytes through fm and
// commands workers over control.
func NewS3FileStager(fm *storage.FileManager, control *ControlClient) *S3FileStager {
	return &S3FileStager{fm: fm, control: control}
}

// ForgetNode has nothing of its own to drop: this stager keeps no per-node
// state at all. Bytes travel through the object store, and the only per-node
// client involved is the ControlClient's, which the deployment shares with
// every other control verb and registers on the departure notifier itself.
//
// Deliberately NOT forwarded to that client. Dropping it from here as well
// would close a cache this stager does not own, on a departure it would then
// be reacting to twice, and the second drop would be invisible in the
// subscriber names the wiring spec reads.
func (s *S3FileStager) ForgetNode(string) {}

// The two budgets a file-staging RPC gets. They are the ones the NATS
// request-reply timeouts carried, kept verbatim: a transfer verb waits out a
// multi-gigabyte copy, and a metadata verb does not.
//
// They are CEILINGS on the caller's own budget rather than budgets of their
// own. Every call below derives its deadline from the caller's context, so a
// caller that has already given up stops the RPC too; deriving from a fresh
// background context would keep commanding a worker nobody is listening to and
// would let a late answer be read as a live one.
const (
	fileTransferRPCTimeout = 10 * time.Minute
	fileMetadataRPCTimeout = 30 * time.Second
)

// fileRPCBudget is the ceiling one file-staging verb's RPC gets.
//
// The mapping lives here rather than at the call sites, and that is the same
// argument callWorker is written under: five sites each naming their own
// constant is five chances to name the wrong one, and a stage verb given the
// metadata ceiling would abandon a multi-gigabyte copy after thirty seconds
// while the worker went on making it.
//
// A path this function does not know gets the SHORTER ceiling. That is the safe
// direction: a verb wrongly given 30 seconds fails visibly and is retried,
// while one wrongly given ten minutes parks a caller on a verb that was never
// meant to be slow.
func fileRPCBudget(path string) time.Duration {
	switch path {
	case workerctl.PathFilesEnsure, workerctl.PathFilesStage:
		return fileTransferRPCTimeout
	case workerctl.PathFilesTemp, workerctl.PathFilesListDir:
		return fileMetadataRPCTimeout
	default:
		return fileMetadataRPCTimeout
	}
}

// Control request/reply message types. Their JSON is the shape the
// nodes.<id>.files.* subjects carried, so the worker's handler bodies did not
// have to change when the carrier did.

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

type fileReleaseRequest struct {
	Key       string `json:"key,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type fileReleaseReply struct {
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

// callWorker issues one file-staging RPC under a deadline derived from the
// caller's context and bounded by the verb's own ceiling.
//
// It exists so the derivation is written ONCE. Five call sites each repeating
// context.WithTimeout is five chances for one of them to start from a
// background context instead, and that one site would then keep commanding a
// worker after its caller had gone, with nothing else in the suite any redder
// for it. The budget comes from the path rather than from an argument for the
// same reason; see fileRPCBudget.
func (s *S3FileStager) callWorker(ctx context.Context, nodeID, path string, req, reply any) error {
	rpcCtx, cancel := context.WithTimeout(ctx, fileRPCBudget(path))
	defer cancel()
	return s.control.Call(rpcCtx, nodeID, path, req, reply)
}

// EnsureRemote uploads a local file to the object store (if not already there)
// and tells the worker to fetch it.
func (s *S3FileStager) EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error) {
	// Upload to the store if not already present
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
			return "", fmt.Errorf("uploading %s to the object store: %w", localPath, err)
		}
	}

	var reply fileEnsureReply
	if err := s.callWorker(ctx, nodeID, workerctl.PathFilesEnsure, fileEnsureRequest{Key: key}, &reply); err != nil {
		return "", err
	}
	if reply.Error != "" {
		return "", fmt.Errorf("backend ensure failed: %s", reply.Error)
	}

	xlog.Debug("File ensured on remote node", "nodeID", nodeID, "key", key, "remotePath", reply.LocalPath)
	return reply.LocalPath, nil
}

// FetchRemote tells the worker to upload a file to the object store, then
// downloads it locally.
func (s *S3FileStager) FetchRemote(ctx context.Context, nodeID, remotePath, localDst string) error {
	key := storage.EphemeralKey(remotePath, "fetch", "output")
	return s.fetchRemoteWithKey(ctx, nodeID, remotePath, key, localDst, true)
}

// FetchRemoteByKey tells the worker to upload a file (identified by key) to the
// object store, then downloads it locally. The key is used as-is for routing.
func (s *S3FileStager) FetchRemoteByKey(ctx context.Context, nodeID, key, localDst string) error {
	// The remote path is derived from the key: the worker serves the file from
	// its data dir based on the key prefix.
	remotePath := "/" + key // e.g. "/data/quantization/{jobID}/model.gguf"
	return s.fetchRemoteWithKey(ctx, nodeID, remotePath, key, localDst, true)
}

func (s *S3FileStager) fetchRemoteWithKey(ctx context.Context, nodeID, remotePath, key, localDst string, cleanup bool) error {
	var reply fileStageReply
	if err := s.callWorker(ctx, nodeID, workerctl.PathFilesStage, fileStageRequest{LocalPath: remotePath, Key: key}, &reply); err != nil {
		return err
	}
	if reply.Error != "" {
		return fmt.Errorf("backend stage failed: %s", reply.Error)
	}

	// Download from the store to the local cache. The CALLER's context bounds
	// this rather than the RPC's, because it is this frontend's own work and
	// the RPC it belonged to has already finished.
	cachedPath, err := s.fm.Download(ctx, key)
	if err != nil {
		return fmt.Errorf("downloading %s from the object store: %w", key, err)
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

// AllocRemoteTemp asks the worker to allocate a temp file.
func (s *S3FileStager) AllocRemoteTemp(ctx context.Context, nodeID string) (string, error) {
	var reply fileTempReply
	if err := s.callWorker(ctx, nodeID, workerctl.PathFilesTemp, fileTempRequest{}, &reply); err != nil {
		return "", err
	}
	if reply.Error != "" {
		return "", fmt.Errorf("backend temp alloc failed: %s", reply.Error)
	}

	return reply.LocalPath, nil
}

// ListRemoteDir returns the relative paths of every file under keyPrefix on the
// worker.
//
// Nothing truncates the answer, at either end. The bus this used to ride put a
// ceiling on how big a reply could be, and a wide model directory was the case
// that pushed against it; a response body has no such ceiling, and a short
// listing would read to the caller as files the worker does not have.
func (s *S3FileStager) ListRemoteDir(ctx context.Context, nodeID, keyPrefix string) ([]string, error) {
	var reply fileListDirReply
	if err := s.callWorker(ctx, nodeID, workerctl.PathFilesListDir, fileListDirRequest{KeyPrefix: keyPrefix}, &reply); err != nil {
		return nil, err
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("backend listdir failed: %s", reply.Error)
	}

	return reply.Files, nil
}

// StageRemoteToStore tells the worker to upload a local file to shared storage.
func (s *S3FileStager) StageRemoteToStore(ctx context.Context, nodeID, remotePath, key string) error {
	var reply fileStageReply
	if err := s.callWorker(ctx, nodeID, workerctl.PathFilesStage, fileStageRequest{LocalPath: remotePath, Key: key}, &reply); err != nil {
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
	if err := s.releaseWorkerKeys(ctx, nodeID, fileReleaseRequest{Key: key}); err != nil {
		return err
	}
	if err := s.fm.Delete(ctx, key); err != nil {
		return fmt.Errorf("deleting shared object %q: %w", key, err)
	}
	return nil
}

// ReleaseRemoteRequest evicts one inference's inputs with one control request.
// A worker that only understands the exact-key payload returns an error, so the
// frontend retries each key during a rolling upgrade.
func (s *S3FileStager) ReleaseRemoteRequest(ctx context.Context, nodeID, requestID string, keys []string) error {
	if err := validateEphemeralRequestRelease(requestID, keys); err != nil {
		return err
	}
	if err := s.releaseWorkerKeys(ctx, nodeID, fileReleaseRequest{RequestID: requestID}); err != nil {
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

func (s *S3FileStager) releaseWorkerKeys(ctx context.Context, nodeID string, request fileReleaseRequest) error {
	var reply fileReleaseReply
	if err := s.callWorker(ctx, nodeID, workerctl.PathFilesRelease, request, &reply); err != nil {
		return err
	}
	if reply.Error != "" {
		return fmt.Errorf("backend release failed: %s", reply.Error)
	}
	return nil
}
