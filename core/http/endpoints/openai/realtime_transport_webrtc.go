package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
	"github.com/pion/webrtc/v4"
)

// WebRTCTransport implements Transport over a pion/webrtc PeerConnection.
// Events travel via the "oai-events" DataChannel; audio goes over an RTP track.
type WebRTCTransport struct {
	pc          *webrtc.PeerConnection
	dc          *webrtc.DataChannel
	opusBackend grpc.Backend
	inEvents    chan []byte
	outEvents   chan []byte // buffered outbound event queue
	closed      chan struct{}
	closeDone   func()        // sync.OnceFunc that closes t.closed
	flushed     chan struct{} // closed when sender goroutine has drained outEvents
	dcReady     chan struct{} // closed when data channel is open
	dcDone      func()        // sync.OnceFunc that closes t.dcReady
	sessionCh   chan *Session // delivers session from runRealtimeSession to handleIncomingAudioTrack
	audio       *webRTCAudioSender
}

func NewWebRTCTransport(pc *webrtc.PeerConnection, audioTrack *webrtc.TrackLocalStaticRTP, opusBackend grpc.Backend) *WebRTCTransport {
	t := &WebRTCTransport{
		pc:          pc,
		opusBackend: opusBackend,
		inEvents:    make(chan []byte, 256),
		outEvents:   make(chan []byte, 256),
		closed:      make(chan struct{}),
		flushed:     make(chan struct{}),
		dcReady:     make(chan struct{}),
		sessionCh:   make(chan *Session, 1),
	}
	t.closeDone = sync.OnceFunc(func() { close(t.closed) })
	t.dcDone = sync.OnceFunc(func() { close(t.dcReady) })
	t.audio = newWebRTCAudioSender(opusBackend, audioTrack, t.closed)

	// The client creates the "oai-events" data channel (so m=application is
	// included in the SDP offer). We receive it here via OnDataChannel.
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != "oai-events" {
			return
		}
		t.dc = dc
		dc.OnOpen(func() {
			t.dcDone()
		})
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			select {
			case t.inEvents <- msg.Data:
			case <-t.closed:
			}
		})
		// The channel may already be open by the time OnDataChannel fires
		if dc.ReadyState() == webrtc.DataChannelStateOpen {
			t.dcDone()
		}
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		xlog.Debug("WebRTC connection state", "state", state.String())
		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateDisconnected {
			t.closeDone()
		}
	})

	go t.sendLoop()

	return t
}

// sendLoop is a dedicated goroutine that drains outEvents and sends them
// over the data channel. It waits for the data channel to open before
// sending, and drains any remaining events when closed is signalled.
func (t *WebRTCTransport) sendLoop() {
	defer close(t.flushed)

	// Wait for data channel to be ready
	select {
	case <-t.dcReady:
	case <-t.closed:
		return
	}

	for {
		select {
		case data, ok := <-t.outEvents:
			if !ok {
				return
			}
			if err := t.dc.SendText(string(data)); err != nil {
				// Drop just this event and keep the loop alive: a single
				// failed send (e.g. an event over the negotiated SCTP
				// max-message-size) must not tear down the session and
				// silently drop every subsequent event. A genuinely dead
				// transport is handled by the <-t.closed case.
				xlog.Error("data channel send failed, dropping event", "error", err)
				continue
			}
		case <-t.closed:
			// Drain any remaining queued events before exiting
			for {
				select {
				case data := <-t.outEvents:
					if err := t.dc.SendText(string(data)); err != nil {
						xlog.Error("data channel send failed while draining, dropping event", "error", err)
						continue
					}
				default:
					return
				}
			}
		}
	}
}

func (t *WebRTCTransport) SendEvent(event types.ServerEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	select {
	case t.outEvents <- data:
		return nil
	case <-t.closed:
		return fmt.Errorf("transport closed")
	}
}

func (t *WebRTCTransport) ReadEvent() ([]byte, error) {
	select {
	case msg := <-t.inEvents:
		return msg, nil
	case <-t.closed:
		return nil, fmt.Errorf("transport closed")
	}
}

// SendAudio admits PCM into the bounded media queue. A transport-scoped worker
// owns the Opus encoder, pacing clock, sequence number, timestamp, and RTP
// writer so independent TTS callbacks cannot create packet bursts.
func (t *WebRTCTransport) SendAudio(ctx context.Context, pcmData []byte, sampleRate int) error {
	return t.audio.Enqueue(ctx, pcmData, sampleRate)
}

func (t *WebRTCTransport) DrainAudio(ctx context.Context) error { return t.audio.Drain(ctx) }

func (t *WebRTCTransport) AbortAudio(ctx context.Context) error { return t.audio.Abort(ctx) }

// SetSession delivers the session to any goroutine waiting in WaitForSession.
func (t *WebRTCTransport) SetSession(s *Session) {
	select {
	case t.sessionCh <- s:
	case <-t.closed:
	}
}

// WaitForSession blocks until the session is available or the transport closes.
func (t *WebRTCTransport) WaitForSession() *Session {
	select {
	case s := <-t.sessionCh:
		return s
	case <-t.closed:
		return nil
	}
}

func (t *WebRTCTransport) Close() error {
	// Close the codec stream before signalling the connection closed; the
	// worker otherwise observes t.closed and can only perform best-effort cleanup.
	audioErr := t.audio.Close(context.Background())
	// Signal no more events and unblock the sender if it's waiting
	t.closeDone()
	// Wait for the sender to drain any remaining queued events
	<-t.flushed
	return errors.Join(audioErr, t.pc.Close())
}
