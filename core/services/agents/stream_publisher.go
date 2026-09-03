package agents

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// StreamPublisher is a messaging.Publisher that writes progress lines onto an
// in-flight control response instead of onto a bus.
//
// It exists because a worker has no database and therefore cannot NOTIFY, and
// because it does not need to: every message a worker sends is produced inside
// a handler the frontend invoked, so there is always an open response to write
// on.
//
// A subject written here is a REQUEST and not a publish. Nothing on this side
// is authorised: the frontend reading the stream checks the subject against the
// allow list for the node's type and refuses what the worker has no business
// on. That refusal is silent to this end on purpose, since a worker learning
// which subjects a frontend will carry is the beginning of a worker that probes
// for them.
//
// Writes are serialized: cogito's stream callbacks run on the goroutine that is
// executing the agent, but the status callback and the tool-result callback can
// interleave with a flush, and two concurrent json.Encoder writes on one
// http.ResponseWriter interleave bytes and corrupt the NDJSON framing.
type StreamPublisher struct {
	mu    sync.Mutex
	enc   *json.Encoder
	flush func()
}

// Compile-time proof that this is the Publisher a caller can hand to anything
// that publishes. Asserted here rather than at the first adopter so a change to
// either side fails to build in the file that owns the type.
var _ messaging.Publisher = (*StreamPublisher)(nil)

// NewStreamPublisher returns a StreamPublisher writing to w. flush may be nil,
// which is the right shape for a writer that has no buffering to push through.
func NewStreamPublisher(w io.Writer, flush func()) *StreamPublisher {
	return &StreamPublisher{enc: json.NewEncoder(w), flush: flush}
}

// Publish writes one Envelope carrying subject and the JSON encoding of data,
// then flushes, so a progress tick reaches the frontend while the handler is
// still running rather than when the response closes.
//
// data is encoded OUTSIDE the lock. Encoding is the expensive half and it
// touches nothing shared, so holding the lock across it would serialise every
// caller behind the slowest one's marshalling for no gain in framing.
func (p *StreamPublisher) Publish(subject string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encoding a stream message for %q: %w", subject, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.enc.Encode(workerctl.Envelope{Subject: subject, Progress: raw}); err != nil {
		return fmt.Errorf("writing a stream message for %q: %w", subject, err)
	}
	// The flush is inside the lock and belongs there. A flush concurrent with
	// an Encode pushes a partially written line at the reader, which is the
	// same torn frame the lock exists to prevent.
	if p.flush != nil {
		p.flush()
	}
	return nil
}
