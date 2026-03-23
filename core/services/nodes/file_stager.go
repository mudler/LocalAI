package nodes

import "context"

// FileStager abstracts file transfer between frontend and backend nodes
// in distributed mode. Two implementations exist:
//
// 1. S3NATSFileStager (primary): Both sides have FileManager with same S3.
//    Frontend uploads to S3, sends NATS request-reply to backend to download locally.
//
// 2. HTTPFileStager (fallback): Frontend pushes/pulls files directly over
//    HTTP to a small file transfer server on the backend node (no S3 needed).
type FileStager interface {
	// EnsureRemote ensures a local file is available on the remote node.
	// Returns the remote-local path.
	EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error)

	// FetchRemote retrieves a file from the remote node to a local path.
	FetchRemote(ctx context.Context, nodeID, remotePath, localDst string) error

	// FetchRemoteByKey retrieves a file from the remote node using an explicit
	// storage key (e.g. "data/quantization/...") instead of deriving it from the path.
	FetchRemoteByKey(ctx context.Context, nodeID, key, localDst string) error

	// AllocRemoteTemp allocates a temp file on the remote node.
	// Returns the remote-local path.
	AllocRemoteTemp(ctx context.Context, nodeID string) (string, error)

	// StageRemoteToStore uploads a remote file to shared storage.
	StageRemoteToStore(ctx context.Context, nodeID, remotePath, key string) error

	// ListRemoteDir returns relative file paths within a directory on the remote node.
	// keyPrefix is a storage-style key prefix (e.g. "models/mymodel").
	ListRemoteDir(ctx context.Context, nodeID, keyPrefix string) ([]string, error)
}
