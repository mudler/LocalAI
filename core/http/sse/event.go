package sse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrEncode wraps the error of a value WriteEvent could not encode.
var ErrEncode = errors.New("sse: encode event")

// eventBuffer holds the encoding of one event.
type eventBuffer struct {
	buf bytes.Buffer
	enc *json.Encoder
}

var eventBuffers = sync.Pool{New: func() any {
	b := &eventBuffer{}
	b.enc = json.NewEncoder(&b.buf)
	return b
}}

// maxPooledEvent is the largest buffer kept for reuse; a rare large event
// does not pin its memory.
const maxPooledEvent = 64 << 10

// WriteEvent writes v as one named event with a single Write:
//
//	event: <name>
//	data: <JSON of v>
//
// The bytes are those of fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name,
// json.Marshal(v)): json.Encoder writes what Marshal returns, then a newline.
// Nothing is written when v cannot be encoded, and the error wraps ErrEncode. A streaming endpoint writes an
// event per token, and the encoding reuses pooled buffers instead of
// allocating the JSON and two copies of it for each one.
func WriteEvent(w io.Writer, name string, v any) error {
	b := eventBuffers.Get().(*eventBuffer)
	defer func() {
		if b.buf.Cap() <= maxPooledEvent {
			eventBuffers.Put(b)
		}
	}()
	b.buf.Reset()
	b.buf.WriteString("event: ")
	b.buf.WriteString(name)
	b.buf.WriteString("\ndata: ")
	if err := b.enc.Encode(v); err != nil {
		return fmt.Errorf("%w: %w", ErrEncode, err)
	}
	b.buf.WriteByte('\n')
	_, err := w.Write(b.buf.Bytes())
	return err
}
