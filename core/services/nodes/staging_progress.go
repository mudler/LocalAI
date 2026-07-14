package nodes

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// StagingStatus represents the current progress of a model staging operation.
type StagingStatus struct {
	ModelID    string    `json:"model_id"`
	NodeName   string    `json:"node_name"`
	FileName   string    `json:"file_name"`
	BytesSent  int64     `json:"bytes_sent"`
	TotalBytes int64     `json:"total_bytes"`
	Progress   float64   `json:"progress"` // 0-100 overall progress
	Speed      string    `json:"speed"`
	FileIndex  int       `json:"file_index"`
	TotalFiles int       `json:"total_files"`
	Message    string    `json:"message"`
	StartedAt  time.Time `json:"started_at"`
}

const (
	// stagingBroadcastInterval bounds how often byte-level UpdateFile ticks are
	// re-broadcast to peers (leading-edge debounce). State transitions (Start,
	// FileComplete, Complete) always publish so peers never miss them.
	stagingBroadcastInterval = time.Second
	// stagingRemoteTTL drops a mirrored (remote) op whose last update is older
	// than this. NATS pub/sub is fire-and-forget, so a missed Done event would
	// otherwise leave a phantom staging row on a peer forever; a live op
	// refreshes its mirror at least every stagingBroadcastInterval.
	stagingRemoteTTL = 60 * time.Second
)

// stagingEntry wraps a StagingStatus with the bookkeeping needed to keep peer
// replicas consistent: whether this op is mirrored from a peer (remote) vs.
// owned locally, when it was last updated (for remote-mirror expiry), and when
// its byte progress was last broadcast (for debounce).
type stagingEntry struct {
	status    StagingStatus
	remote    bool
	updatedAt time.Time
	lastPub   time.Time
}

// StagingTracker tracks active file staging operations in-memory.
// Used by SmartRouter to publish progress and by /api/operations to surface it.
//
// In distributed mode each frontend replica runs its own tracker. The replica
// performing a transfer owns the op locally and broadcasts progress over NATS
// (SetPublisher); peers mirror it via ApplyRemote (SubscribeBroadcasts) so a
// /api/operations poll that round-robins onto any replica surfaces the op.
type StagingTracker struct {
	mu        sync.RWMutex
	active    map[string]*stagingEntry
	publisher messaging.Publisher
}

// StagingProgressEvent is the wire payload a frontend replica broadcasts on
// SubjectStagingProgress so peer replicas can mirror a staging op they did not
// originate. Done signals the op finished (peers drop their mirrored copy).
type StagingProgressEvent struct {
	ModelID string         `json:"model_id"`
	Status  *StagingStatus `json:"status,omitempty"`
	Done    bool           `json:"done"`
}

// NewStagingTracker creates a new tracker.
func NewStagingTracker() *StagingTracker {
	return &StagingTracker{
		active: make(map[string]*stagingEntry),
	}
}

// SetPublisher wires the NATS publisher used to broadcast staging progress to
// peer replicas. No-op publisher (nil) keeps the tracker standalone.
func (t *StagingTracker) SetPublisher(p messaging.Publisher) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.publisher = p
}

// SubscribeBroadcasts subscribes to peer replicas' staging-progress broadcasts
// and mirrors them into this tracker, so /api/operations on any replica surfaces
// staging ops it did not originate. Returns the subscription for cleanup.
func (t *StagingTracker) SubscribeBroadcasts(nc messaging.MessagingClient) (messaging.Subscription, error) {
	return messaging.SubscribeJSON(nc, messaging.SubjectStagingProgressWildcard, func(evt StagingProgressEvent) {
		if evt.ModelID == "" {
			return
		}
		t.ApplyRemote(evt)
	})
}

// publishStaging emits an event to the per-model staging subject. The publisher
// is captured by the caller under the lock and passed in, so publishing happens
// outside the lock (a slow NATS link must not stall the staging copy loop).
func publishStaging(p messaging.Publisher, evt StagingProgressEvent) {
	if p == nil {
		return
	}
	_ = p.Publish(messaging.SubjectStagingProgress(evt.ModelID), evt)
}

// Start registers a new staging operation for the given model.
func (t *StagingTracker) Start(modelID, nodeName string, totalFiles int) {
	t.mu.Lock()
	e := &stagingEntry{
		status: StagingStatus{
			ModelID:    modelID,
			NodeName:   nodeName,
			TotalFiles: totalFiles,
			StartedAt:  time.Now(),
			Message:    "Preparing to stage model files",
		},
		updatedAt: time.Now(),
		// lastPub stays zero so the first UpdateFile tick always broadcasts.
	}
	t.active[modelID] = e
	pub := t.publisher
	snap := e.status
	t.mu.Unlock()

	publishStaging(pub, StagingProgressEvent{ModelID: modelID, Status: &snap})
}

// UpdateFile updates the tracker with current file transfer progress.
func (t *StagingTracker) UpdateFile(modelID, fileName string, fileIndex int, bytesSent, totalBytes int64, speed string) {
	t.mu.Lock()
	e, ok := t.active[modelID]
	if !ok {
		t.mu.Unlock()
		return
	}
	s := &e.status
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

	e.updatedAt = time.Now()
	// Leading-edge debounce: byte ticks fire many times per second; only
	// re-broadcast at most once per stagingBroadcastInterval.
	var pub messaging.Publisher
	var snap StagingStatus
	if time.Since(e.lastPub) >= stagingBroadcastInterval {
		e.lastPub = time.Now()
		pub = t.publisher
		snap = e.status
	}
	t.mu.Unlock()

	if pub != nil {
		publishStaging(pub, StagingProgressEvent{ModelID: modelID, Status: &snap})
	}
}

// FileComplete marks a single file as done within a staging operation.
func (t *StagingTracker) FileComplete(modelID string, fileIndex, totalFiles int) {
	t.mu.Lock()
	e, ok := t.active[modelID]
	if !ok {
		t.mu.Unlock()
		return
	}
	s := &e.status
	if totalFiles > 0 {
		s.Progress = float64(fileIndex) / float64(totalFiles) * 100
	}
	s.BytesSent = 0
	s.TotalBytes = 0
	s.Speed = ""
	e.updatedAt = time.Now()
	e.lastPub = time.Now()
	pub := t.publisher
	snap := e.status
	t.mu.Unlock()

	// Always broadcast a per-file completion so peers' progress bars advance.
	publishStaging(pub, StagingProgressEvent{ModelID: modelID, Status: &snap})
}

// Complete removes a staging operation (it's done).
func (t *StagingTracker) Complete(modelID string) {
	t.mu.Lock()
	_, ok := t.active[modelID]
	delete(t.active, modelID)
	pub := t.publisher
	t.mu.Unlock()

	if ok {
		// Tell peers to drop their mirrored copy.
		publishStaging(pub, StagingProgressEvent{ModelID: modelID, Done: true})
	}
}

// ApplyRemote merges a peer replica's staging broadcast into this tracker. It
// never re-broadcasts (no echo loop). A locally-owned op is authoritative: a
// remote event for the same model is ignored, so the origin replica receiving
// its own broadcast (and any stray peer event) cannot clobber or delete it.
func (t *StagingTracker) ApplyRemote(evt StagingProgressEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.active[evt.ModelID]; ok && !existing.remote {
		// We own this op locally — ignore peer chatter about it.
		return
	}
	if evt.Done {
		delete(t.active, evt.ModelID)
		return
	}
	if evt.Status == nil {
		return
	}
	t.active[evt.ModelID] = &stagingEntry{
		status:    *evt.Status,
		remote:    true,
		updatedAt: time.Now(),
	}
}

// GetAll returns a snapshot of all active staging operations. Stale remote
// mirrors (a peer op whose Done event was missed) are pruned here so they don't
// linger in the UI.
func (t *StagingTracker) GetAll() map[string]StagingStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	result := make(map[string]StagingStatus, len(t.active))
	for k, e := range t.active {
		if e.remote && now.Sub(e.updatedAt) > stagingRemoteTTL {
			delete(t.active, k)
			continue
		}
		result[k] = e.status
	}
	return result
}

// Get returns the status of a specific staging operation, or nil if not active
// (or a stale remote mirror).
func (t *StagingTracker) Get(modelID string) *StagingStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.active[modelID]
	if !ok {
		return nil
	}
	if e.remote && time.Since(e.updatedAt) > stagingRemoteTTL {
		return nil
	}
	s := e.status
	return &s
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
