package workerctl

// The file staging verbs let the controller move model and request files
// between shared object storage and a worker's local cache. The success
// fields carry omitempty so a reply holds only the key that the outcome sets,
// which matches the single-key replies that workers already send.

// FileEnsureRequest asks a worker to download an object storage key into its
// local cache.
type FileEnsureRequest struct {
	Operation  *OperationIdentity `json:"operation,omitempty"`
	ProcessKey string             `json:"process_key,omitempty"`
	Key        string             `json:"key"`
}

// FileEnsureReply carries the local path of the cached file, or an error.
type FileEnsureReply struct {
	LocalPath string `json:"local_path,omitempty"`
	Error     string `json:"error,omitempty"`
}

// FileStageRequest asks a worker to upload a local file to object storage
// under Key.
type FileStageRequest struct {
	Operation  *OperationIdentity `json:"operation,omitempty"`
	ProcessKey string             `json:"process_key,omitempty"`
	LocalPath  string             `json:"local_path"`
	Key        string             `json:"key"`
}

// FileStageReply carries the key the file was uploaded under, or an error.
type FileStageReply struct {
	Key   string `json:"key,omitempty"`
	Error string `json:"error,omitempty"`
}

// FileTempRequest asks a worker to allocate a temporary file.
type FileTempRequest struct{}

// FileTempReply carries the path of the allocated temporary file, or an error.
type FileTempReply struct {
	LocalPath string `json:"local_path,omitempty"`
	Error     string `json:"error,omitempty"`
}

// FileListDirRequest asks a worker to list the files under a key prefix.
type FileListDirRequest struct {
	KeyPrefix string `json:"key_prefix"`
}

// FileListDirReply carries the listed files, or an error.
type FileListDirReply struct {
	Files []string `json:"files,omitempty"`
	Error string   `json:"error,omitempty"`
}

// FileReleaseRequest asks a worker to evict ephemeral cache entries. Key names
// one exact key; RequestID names every key staged for one inference. A worker
// takes the RequestID form whenever RequestID is set, so a sender fills one.
type FileReleaseRequest struct {
	Key       string `json:"key,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// FileReleaseReply carries an error, or nothing on success.
type FileReleaseReply struct {
	Error string `json:"error,omitempty"`
}
