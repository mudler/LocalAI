package model

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emirpasic/gods/v2/queues/circularbuffer"
)

// replicaSeparator separates a model ID from the replica index in the
// supervisor's process key (e.g. "qwen3-0.6b#0"). Mirrored from the
// worker's buildProcessKey — duplicated as a constant here to keep this
// package free of CLI imports.
const replicaSeparator = "#"

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
//
// When modelID contains no replica suffix (no `#`), it's treated as a model
// prefix and the lines from all `modelID#N` replicas are merged in
// timestamp order. This keeps the existing per-model logs UI working in
// distributed mode after the worker started using `modelID#replicaIndex`
// as its process key (multi-replica refactor) — the UI asks for "qwen3-0.6b"
// and gets the union of all replicas' logs.
//
// When modelID contains a `#` (e.g. "qwen3-0.6b#0"), it's treated as an
// exact process key for per-replica filtering by callers that need it.
func (s *BackendLogStore) GetLines(modelID string) []BackendLogLine {
	s.mu.RLock()
	exactBuf, exactOK := s.buffers[modelID]
	s.mu.RUnlock()

	// Exact match — single key. Caller knew the full process key.
	if exactOK {
		exactBuf.mu.Lock()
		lines := exactBuf.queue.Values()
		exactBuf.mu.Unlock()
		return lines
	}

	// No exact match: aggregate any replicas if modelID looks like a model prefix.
	if strings.Contains(modelID, replicaSeparator) {
		return []BackendLogLine{}
	}

	prefix := modelID + replicaSeparator
	var matching []*backendLogBuffer
	s.mu.RLock()
	for k, b := range s.buffers {
		if strings.HasPrefix(k, prefix) {
			matching = append(matching, b)
		}
	}
	s.mu.RUnlock()

	if len(matching) == 0 {
		return []BackendLogLine{}
	}

	// Merge the per-replica buffers and sort by timestamp so the operator
	// sees a single coherent timeline rather than per-replica blocks.
	var merged []BackendLogLine
	for _, b := range matching {
		b.mu.Lock()
		merged = append(merged, b.queue.Values()...)
		b.mu.Unlock()
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Timestamp.Before(merged[j].Timestamp) })
	return merged
}

// ListModels returns a sorted list of model IDs that have log buffers.
// Replica suffixes (`#N`) are stripped and the result is deduplicated, so
// callers see one entry per loaded model regardless of replica count.
func (s *BackendLogStore) ListModels() []string {
	s.mu.RLock()
	seen := make(map[string]struct{}, len(s.buffers))
	for id := range s.buffers {
		base := id
		if i := strings.Index(id, replicaSeparator); i >= 0 {
			base = id[:i]
		}
		seen[base] = struct{}{}
	}
	s.mu.RUnlock()

	models := make([]string, 0, len(seen))
	for id := range seen {
		models = append(models, id)
	}
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
		for id, ch := range buf.subscribers {
			close(ch)
			delete(buf.subscribers, id)
		}
		buf.mu.Unlock()
		delete(s.buffers, modelID)
	}
	s.mu.Unlock()
}

// Subscribe returns a channel that receives new log lines for the given model
// in real-time, plus an unsubscribe function. The channel has a buffer of 100
// lines to absorb short bursts without blocking the writer.
//
// Like GetLines, a modelID without a `#` separator subscribes to every
// matching `modelID#N` replica buffer that exists at subscribe time, so the
// stream merges all replicas. Subscribers are NOT auto-attached to replicas
// that come up later — callers needing dynamic membership should resubscribe.
func (s *BackendLogStore) Subscribe(modelID string) (chan BackendLogLine, func()) {
	ch := make(chan BackendLogLine, 100)

	// Per-replica caller (full process key) — exact subscription.
	if strings.Contains(modelID, replicaSeparator) {
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

	// Aggregated caller: subscribe to the bare-modelID buffer (for back-compat
	// with single-replica writers that still write to the un-suffixed key) AND
	// to every existing `modelID#N` replica buffer. Each per-buffer subscription
	// receives lines into its own channel; we fan them in to `ch` here.
	type subRef struct {
		buf *backendLogBuffer
		id  int
		ch  chan BackendLogLine
	}
	var refs []subRef

	subscribe := func(buf *backendLogBuffer) {
		bufCh := make(chan BackendLogLine, 100)
		buf.mu.Lock()
		id := buf.nextSubID
		buf.nextSubID++
		buf.subscribers[id] = bufCh
		buf.mu.Unlock()
		refs = append(refs, subRef{buf: buf, id: id, ch: bufCh})
	}

	if buf, ok := func() (*backendLogBuffer, bool) {
		s.mu.RLock()
		b, ok := s.buffers[modelID]
		s.mu.RUnlock()
		return b, ok
	}(); ok {
		subscribe(buf)
	}

	prefix := modelID + replicaSeparator
	s.mu.RLock()
	for k, b := range s.buffers {
		if strings.HasPrefix(k, prefix) {
			subscribe(b)
		}
	}
	s.mu.RUnlock()

	// Fan-in goroutine: forward every per-buffer channel into the merged
	// channel until all source channels close, then close the merged channel.
	if len(refs) == 0 {
		// No source buffers yet: still return a channel so callers don't crash;
		// it'll close on unsubscribe.
		unsubscribe := func() { close(ch) }
		return ch, unsubscribe
	}

	var fanWG sync.WaitGroup
	for _, r := range refs {
		fanWG.Add(1)
		go func(c chan BackendLogLine) {
			defer fanWG.Done()
			for line := range c {
				select {
				case ch <- line:
				default: // drop on slow consumer to match non-aggregated behavior
				}
			}
		}(r.ch)
	}
	// `ch` is closed by exactly one goroutine — the one that observes all
	// fan-in goroutines finish. unsubscribe() closes the per-buffer source
	// channels which causes the fan-in loops to exit; the waiter then
	// closes `ch`. Closing `ch` from anywhere else races with `ch <- line`.
	go func() { fanWG.Wait(); close(ch) }()

	unsubscribe := func() {
		for _, r := range refs {
			r.buf.mu.Lock()
			if c, exists := r.buf.subscribers[r.id]; exists {
				delete(r.buf.subscribers, r.id)
				close(c) // closes the per-buffer source channel; fan-in goroutine exits
			}
			r.buf.mu.Unlock()
		}
	}

	return ch, unsubscribe
}
