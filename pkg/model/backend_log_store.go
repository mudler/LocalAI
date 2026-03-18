package model

import (
	"sort"
	"sync"
	"time"

	"github.com/emirpasic/gods/v2/queues/circularbuffer"
)

// BackendLogLine represents a single line of output from a backend process.
type BackendLogLine struct {
	Timestamp time.Time `json:"timestamp"`
	Stream    string    `json:"stream"` // "stdout" or "stderr"
	Text      string    `json:"text"`
}

// backendLogBuffer wraps a circular buffer for a single model's logs
// and tracks subscribers for real-time streaming.
type backendLogBuffer struct {
	mu          sync.Mutex
	queue       *circularbuffer.Queue[BackendLogLine]
	subscribers map[int]chan BackendLogLine
	nextSubID   int
}

// BackendLogStore stores per-model backend process output in circular buffers
// and supports real-time subscriptions for WebSocket streaming.
type BackendLogStore struct {
	mu       sync.RWMutex // protects the buffers map only
	buffers  map[string]*backendLogBuffer
	maxLines int
}

// NewBackendLogStore creates a new BackendLogStore with a maximum number of
// lines retained per model.
func NewBackendLogStore(maxLinesPerModel int) *BackendLogStore {
	if maxLinesPerModel <= 0 {
		maxLinesPerModel = 1000
	}
	return &BackendLogStore{
		buffers:  make(map[string]*backendLogBuffer),
		maxLines: maxLinesPerModel,
	}
}

// getOrCreateBuffer returns the buffer for modelID, creating it if needed.
func (s *BackendLogStore) getOrCreateBuffer(modelID string) *backendLogBuffer {
	s.mu.RLock()
	buf, ok := s.buffers[modelID]
	s.mu.RUnlock()
	if ok {
		return buf
	}

	s.mu.Lock()
	buf, ok = s.buffers[modelID]
	if !ok {
		buf = &backendLogBuffer{
			queue:       circularbuffer.New[BackendLogLine](s.maxLines),
			subscribers: make(map[int]chan BackendLogLine),
		}
		s.buffers[modelID] = buf
	}
	s.mu.Unlock()
	return buf
}

// AppendLine adds a log line for the given model. The buffer is lazily created.
// All active subscribers for this model are notified (non-blocking).
func (s *BackendLogStore) AppendLine(modelID, stream, text string) {
	line := BackendLogLine{
		Timestamp: time.Now(),
		Stream:    stream,
		Text:      text,
	}

	buf := s.getOrCreateBuffer(modelID)
	buf.mu.Lock()
	buf.queue.Enqueue(line)
	for _, ch := range buf.subscribers {
		select {
		case ch <- line:
		default:
		}
	}
	buf.mu.Unlock()
}

// GetLines returns a copy of all log lines for a model, or an empty slice.
func (s *BackendLogStore) GetLines(modelID string) []BackendLogLine {
	s.mu.RLock()
	buf, ok := s.buffers[modelID]
	s.mu.RUnlock()
	if !ok {
		return []BackendLogLine{}
	}

	buf.mu.Lock()
	lines := buf.queue.Values()
	buf.mu.Unlock()
	return lines
}

// ListModels returns a sorted list of model IDs that have log buffers.
func (s *BackendLogStore) ListModels() []string {
	s.mu.RLock()
	models := make([]string, 0, len(s.buffers))
	for id := range s.buffers {
		models = append(models, id)
	}
	s.mu.RUnlock()

	sort.Strings(models)
	return models
}

// Clear removes all log lines for a model but keeps the buffer entry.
func (s *BackendLogStore) Clear(modelID string) {
	s.mu.RLock()
	buf, ok := s.buffers[modelID]
	s.mu.RUnlock()
	if !ok {
		return
	}
	buf.mu.Lock()
	buf.queue.Clear()
	buf.mu.Unlock()
}

// Remove deletes the buffer entry for a model entirely.
func (s *BackendLogStore) Remove(modelID string) {
	s.mu.Lock()
	if buf, ok := s.buffers[modelID]; ok {
		buf.mu.Lock()
		for _, ch := range buf.subscribers {
			close(ch)
		}
		buf.mu.Unlock()
		delete(s.buffers, modelID)
	}
	s.mu.Unlock()
}

// Subscribe returns a channel that receives new log lines for the given model
// in real-time, plus an unsubscribe function. The channel has a buffer of 100
// lines to absorb short bursts without blocking the writer.
func (s *BackendLogStore) Subscribe(modelID string) (chan BackendLogLine, func()) {
	ch := make(chan BackendLogLine, 100)

	buf := s.getOrCreateBuffer(modelID)
	buf.mu.Lock()
	id := buf.nextSubID
	buf.nextSubID++
	buf.subscribers[id] = ch
	buf.mu.Unlock()

	unsubscribe := func() {
		buf.mu.Lock()
		if _, exists := buf.subscribers[id]; exists {
			delete(buf.subscribers, id)
			close(ch)
		}
		buf.mu.Unlock()
	}

	return ch, unsubscribe
}
