package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/xlog"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// WebRTCTransport implements Transport over a pion/webrtc PeerConnection.
// Events travel via the "oai-events" DataChannel; audio goes over an RTP track.
type WebRTCTransport struct {
	pc         *webrtc.PeerConnection
	dc         *webrtc.DataChannel
	audioTrack *webrtc.TrackLocalStaticRTP
	encoder    *OpusEncoder
	inEvents   chan []byte
	outEvents  chan []byte   // buffered outbound event queue
	closed     chan struct{}
	closeOnce  sync.Once
	flushed    chan struct{} // closed when sender goroutine has drained outEvents
	dcReady    chan struct{} // closed when data channel is open
	dcReadyOnce sync.Once
	sessionCh  chan *Session // delivers session from runRealtimeSession to handleIncomingAudioTrack

	// RTP state for outbound audio — protected by rtpMu
	rtpMu        sync.Mutex
	rtpSeqNum    uint16
	rtpTimestamp uint32
	rtpMarker    bool // true → next packet gets marker bit set
}

func NewWebRTCTransport(pc *webrtc.PeerConnection, audioTrack *webrtc.TrackLocalStaticRTP) (*WebRTCTransport, error) {
	enc, err := NewOpusEncoder()
	if err != nil {
		return nil, fmt.Errorf("webrtc transport: %w", err)
	}

	t := &WebRTCTransport{
		pc:           pc,
		audioTrack:   audioTrack,
		encoder:      enc,
		inEvents:     make(chan []byte, 256),
		outEvents:    make(chan []byte, 256),
		closed:       make(chan struct{}),
		flushed:      make(chan struct{}),
		dcReady:      make(chan struct{}),
		sessionCh:    make(chan *Session, 1),
		rtpSeqNum:    uint16(rand.UintN(65536)),
		rtpTimestamp: rand.Uint32(),
		rtpMarker:    true, // first packet of the stream gets marker
	}

	// The client creates the "oai-events" data channel (so m=application is
	// included in the SDP offer). We receive it here via OnDataChannel.
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != "oai-events" {
			return
		}
		t.dc = dc
		dc.OnOpen(func() {
			t.dcReadyOnce.Do(func() { close(t.dcReady) })
		})
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			select {
			case t.inEvents <- msg.Data:
			case <-t.closed:
			}
		})
		// The channel may already be open by the time OnDataChannel fires
		if dc.ReadyState() == webrtc.DataChannelStateOpen {
			t.dcReadyOnce.Do(func() { close(t.dcReady) })
		}
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		xlog.Debug("WebRTC connection state", "state", state.String())
		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateDisconnected {
			t.closeOnce.Do(func() { close(t.closed) })
		}
	})

	go t.sendLoop()

	return t, nil
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
				xlog.Error("data channel send failed", "error", err)
				return
			}
		case <-t.closed:
			// Drain any remaining queued events before exiting
			for {
				select {
				case data := <-t.outEvents:
					if err := t.dc.SendText(string(data)); err != nil {
						return
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

// SendAudio encodes raw PCM int16 LE to Opus and writes RTP packets to the
// audio track. The encoder resamples from the given sampleRate to 48kHz
// internally. Frames are paced at real-time intervals (20ms per frame) to
// avoid overwhelming the browser's jitter buffer with a burst of packets.
//
// The context allows callers to cancel mid-stream for barge-in support.
// When cancelled, the marker bit is set so the next audio segment starts
// cleanly in the browser's jitter buffer.
//
// RTP packets are constructed manually (rather than via WriteSample) so we
// can control the marker bit. pion's WriteSample sets the marker bit on
// every Opus packet, which causes Chrome's NetEq jitter buffer to reset
// its timing estimation for each frame, producing severe audio distortion.
func (t *WebRTCTransport) SendAudio(ctx context.Context, pcmData []byte, sampleRate int) error {
	frames, err := t.encoder.Encode(pcmData, sampleRate)
	if err != nil {
		return err
	}

	const frameDuration = 20 * time.Millisecond
	const samplesPerFrame = 960 // 20ms at 48kHz

	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	for i, frame := range frames {
		t.rtpMu.Lock()
		pkt := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				Marker:         t.rtpMarker,
				SequenceNumber: t.rtpSeqNum,
				Timestamp:      t.rtpTimestamp,
				// SSRC and PayloadType are overridden by pion's writeRTP
			},
			Payload: frame,
		}
		t.rtpSeqNum++
		t.rtpTimestamp += samplesPerFrame
		t.rtpMarker = false // only the first packet gets marker
		t.rtpMu.Unlock()

		if err := t.audioTrack.WriteRTP(pkt); err != nil {
			return fmt.Errorf("write rtp: %w", err)
		}

		// Pace output at ~real-time so the browser's jitter buffer
		// receives packets at the expected rate. Skip wait after last frame.
		if i < len(frames)-1 {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				// Barge-in: mark the next packet so the browser knows
				// a new audio segment is starting after the interruption.
				t.rtpMu.Lock()
				t.rtpMarker = true
				t.rtpMu.Unlock()
				return ctx.Err()
			case <-t.closed:
				return fmt.Errorf("transport closed during audio send")
			}
		}
	}
	return nil
}

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
	// Signal no more events and unblock the sender if it's waiting
	t.closeOnce.Do(func() { close(t.closed) })
	// Wait for the sender to drain any remaining queued events
	<-t.flushed
	t.encoder.Close()
	return t.pc.Close()
}
