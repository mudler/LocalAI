package nodes

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// StagingStatus represents the current progress of a model staging operation.
type StagingStatus struct {
	ModelID    string  `json:"model_id"`
	NodeName   string  `json:"node_name"`
	FileName   string  `json:"file_name"`
	BytesSent  int64   `json:"bytes_sent"`
	TotalBytes int64   `json:"total_bytes"`
	Progress   float64 `json:"progress"` // 0-100 overall progress
	Speed      string  `json:"speed"`
	FileIndex  int     `json:"file_index"`
	TotalFiles int     `json:"total_files"`
	Message    string  `json:"message"`
	StartedAt  time.Time `json:"started_at"`
}

// StagingTracker tracks active file staging operations in-memory.
// Used by SmartRouter to publish progress and by /api/operations to surface it.
type StagingTracker struct {
	mu     sync.RWMutex
	active map[string]*StagingStatus
}

// NewStagingTracker creates a new tracker.
func NewStagingTracker() *StagingTracker {
	return &StagingTracker{
		active: make(map[string]*StagingStatus),
	}
}

// Start registers a new staging operation for the given model.
func (t *StagingTracker) Start(modelID, nodeName string, totalFiles int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active[modelID] = &StagingStatus{
		ModelID:    modelID,
		NodeName:   nodeName,
		TotalFiles: totalFiles,
		StartedAt:  time.Now(),
		Message:    "Preparing to stage model files",
	}
}

// UpdateFile updates the tracker with current file transfer progress.
func (t *StagingTracker) UpdateFile(modelID, fileName string, fileIndex int, bytesSent, totalBytes int64, speed string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.active[modelID]
	if !ok {
		return
	}
	s.FileName = fileName
	s.FileIndex = fileIndex
	s.BytesSent = bytesSent
	s.TotalBytes = totalBytes
	s.Speed = speed

	// Calculate overall progress across all files
	if s.TotalFiles > 0 && totalBytes > 0 {
		filePct := float64(bytesSent) / float64(totalBytes) * 100
		s.Progress = (float64(fileIndex-1)*100 + filePct) / float64(s.TotalFiles)
	}

	// Build human-readable message
	if totalBytes > 0 {
		s.Message = fmt.Sprintf("%s (%s / %s", fileName, humanFileSize(bytesSent), humanFileSize(totalBytes))
		if speed != "" {
			s.Message += ", " + speed
		}
		s.Message += ")"
	} else {
		s.Message = fmt.Sprintf("Staging %s", fileName)
	}
}

// FileComplete marks a single file as done within a staging operation.
func (t *StagingTracker) FileComplete(modelID string, fileIndex, totalFiles int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.active[modelID]
	if !ok {
		return
	}
	if totalFiles > 0 {
		s.Progress = float64(fileIndex) / float64(totalFiles) * 100
	}
	s.BytesSent = 0
	s.TotalBytes = 0
	s.Speed = ""
}

// Complete removes a staging operation (it's done).
func (t *StagingTracker) Complete(modelID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.active, modelID)
}

// GetAll returns a snapshot of all active staging operations.
func (t *StagingTracker) GetAll() map[string]StagingStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make(map[string]StagingStatus, len(t.active))
	for k, v := range t.active {
		result[k] = *v
	}
	return result
}

// Get returns the status of a specific staging operation, or nil if not active.
func (t *StagingTracker) Get(modelID string) *StagingStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	s, ok := t.active[modelID]
	if !ok {
		return nil
	}
	copy := *s
	return &copy
}

// StagingProgressCallback is called by file stagers to report byte-level progress.
type StagingProgressCallback func(fileName string, bytesSent, totalBytes int64)

type stagingProgressKey struct{}

// WithStagingProgress attaches a progress callback to a context.
func WithStagingProgress(ctx context.Context, cb StagingProgressCallback) context.Context {
	return context.WithValue(ctx, stagingProgressKey{}, cb)
}

// StagingProgressFromContext extracts a progress callback from a context.
// Returns nil if no callback is set.
func StagingProgressFromContext(ctx context.Context) StagingProgressCallback {
	cb, _ := ctx.Value(stagingProgressKey{}).(StagingProgressCallback)
	return cb
}
