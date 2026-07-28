package openai

import (
	"sync"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/ttscoord"
)

// ttsPipeline decouples speech synthesis from LLM token generation.
//
// The LLM token callback runs on the same goroutine that drains the model's
// gRPC stream, so anything it does serially — including a blocking TTS call —
// stops the stream from being read and stalls generation (and, since the same
// goroutine also sends the assistant transcript, freezes the transcript the
// client sees). ttsPipeline lets the callback hand each completed clause to a
// single worker goroutine that synthesizes them in order, concurrently with
// continued generation. One worker preserves clause — and therefore audio —
// ordering.
//
// The clause queue is intentionally unbounded: clauses are short strings and a
// reply has a bounded number of them, while the expensive product (audio) is
// paced by the TTS backend regardless. So enqueue never blocks the callback,
// and the transcript streams to the client at generation speed while audio is
// produced behind it.
type ttsPipeline struct {
	speak func(clause string) ([]byte, error)

	mu    sync.Mutex
	queue []string
	wake  chan struct{} // buffered(1) wakeup signal for the worker

	// coord owns the open->closing->closed lifecycle (machine M5). It replaces the
	// legacy `closed bool`: the producer raises Close (wait()), the worker raises
	// WorkerExited. See ttscoord/ and realtime-state-machines.md.
	coord *ttscoord.Coordinator

	done   chan struct{}
	failed atomic.Bool

	// audio and firstErr are owned by the worker goroutine and only safe to
	// read after wait() has returned (it joins on the worker via done).
	audio    []byte
	firstErr error
}

// newTTSPipeline starts the worker. speak performs the actual synthesis and
// returns the PCM accumulated for the conversation-item record (empty for
// transports that stream audio out-of-band, e.g. WebRTC).
func newTTSPipeline(speak func(clause string) ([]byte, error)) *ttsPipeline {
	p := &ttsPipeline{
		speak: speak,
		wake:  make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
	p.coord = ttscoord.New(p)
	go p.run()
	return p
}

// closing reports whether wait() has been called (lifecycle past Open). Read
// under p.mu in the worker so the queue-empty check and the close check are
// consistent.
func (p *ttsPipeline) closing() bool {
	_, open := p.coord.State().(ttscoord.Open)
	return !open
}

// Perform executes a coordinator effect. Wake nudges the worker (non-blocking).
func (p *ttsPipeline) Perform(e ttscoord.Effect) {
	if _, ok := e.(ttscoord.Wake); ok {
		p.signal()
	}
}

func (p *ttsPipeline) run() {
	defer close(p.done)
	for {
		p.mu.Lock()
		for len(p.queue) == 0 && !p.closing() {
			p.mu.Unlock()
			<-p.wake
			p.mu.Lock()
		}
		if len(p.queue) == 0 && p.closing() {
			p.mu.Unlock()
			// Drained and closed: advance the lifecycle to Closed, then exit
			// (the deferred close(p.done) joins the producer's wait()).
			_ = p.coord.Apply(ttscoord.WorkerExited{})
			return
		}
		clause := p.queue[0]
		p.queue = p.queue[1:]
		p.mu.Unlock()

		// Once a clause has failed, keep draining the queue without speaking so
		// the producer's wait() returns promptly and the first error is kept.
		if p.failed.Load() {
			continue
		}
		a, err := p.speak(clause)
		if err != nil {
			p.firstErr = err
			p.failed.Store(true)
			continue
		}
		p.audio = append(p.audio, a...)
	}
}

// enqueue offers a clause for synthesis. It never blocks; it returns false once
// synthesis has failed, signalling the caller to stop the prediction.
func (p *ttsPipeline) enqueue(clause string) bool {
	if p.failed.Load() {
		return false
	}
	p.mu.Lock()
	// Reject once closing/closed: the worker may have already drained and exited,
	// so a clause queued now would be silently dropped. The lifecycle (Open) and
	// the append are checked under the same lock, so the worker cannot exit between
	// the gate and the enqueue (it takes p.mu to observe the empty queue).
	if p.closing() {
		p.mu.Unlock()
		return false
	}
	p.queue = append(p.queue, clause)
	p.mu.Unlock()
	p.signal()
	return true
}

// signal wakes the worker without blocking; the buffered channel coalesces
// signals, which is safe because the worker drains the whole queue per wake.
func (p *ttsPipeline) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// wait closes the queue and blocks until the worker has spoken every enqueued
// clause, then returns the accumulated audio and the first synthesis error. It
// is idempotent: calling it again returns the same result without blocking, so
// callers can drain it explicitly to read the audio and still defer a wait() as
// a leak-proof backstop. No clause may be enqueued after the first wait().
func (p *ttsPipeline) wait() ([]byte, error) {
	// Close the lifecycle (Open->Closing) and wake the worker. Idempotent: a
	// second Close is absorbed (no second wake), and <-p.done returns immediately
	// once the worker has exited.
	_ = p.coord.Apply(ttscoord.Close{})
	<-p.done
	return p.audio, p.firstErr
}
