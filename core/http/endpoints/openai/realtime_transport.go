package openai

import (
	"context"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
)

// Transport abstracts event and audio I/O so the same session logic
// can serve both WebSocket and WebRTC connections.
type Transport interface {
	// SendEvent marshals and sends a server event to the client.
	SendEvent(event types.ServerEvent) error
	// ReadEvent reads the next raw client event (JSON bytes).
	ReadEvent() ([]byte, error)
	// SendAudio sends raw PCM audio to the client at the given sample rate.
	// For WebSocket this is a no-op (audio is sent via JSON events).
	// For WebRTC this encodes to Opus and writes to the media track.
	// The context allows cancellation for barge-in support.
	SendAudio(ctx context.Context, pcmData []byte, sampleRate int) error
	// DrainAudio waits until all accepted audio has been encoded and written.
	DrainAudio(ctx context.Context) error
	// AbortAudio discards queued audio and waits until the old response can no
	// longer write media. This is the barge-in acknowledgement boundary.
	AbortAudio(ctx context.Context) error
	// Close tears down the underlying connection.
	Close() error
}
