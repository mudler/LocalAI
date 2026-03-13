package openai

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
	"github.com/pion/webrtc/v4"
)

// RealtimeCallRequest is the JSON body for POST /v1/realtime/calls.
type RealtimeCallRequest struct {
	SDP   string `json:"sdp"`
	Model string `json:"model"`
}

// RealtimeCallResponse is the JSON response for POST /v1/realtime/calls.
type RealtimeCallResponse struct {
	SDP       string `json:"sdp"`
	SessionID string `json:"session_id"`
}

// RealtimeCalls handles POST /v1/realtime/calls for WebRTC signaling.
func RealtimeCalls(application *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req RealtimeCallRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
		if req.SDP == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "sdp is required"})
		}
		if req.Model == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "model is required"})
		}

		// Create a MediaEngine with Opus support
		m := &webrtc.MediaEngine{}
		if err := m.RegisterDefaultCodecs(); err != nil {
			xlog.Error("failed to register codecs", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "codec registration failed"})
		}

		api := webrtc.NewAPI(webrtc.WithMediaEngine(m))

		pc, err := api.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			xlog.Error("failed to create peer connection", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create peer connection"})
		}

		// Create outbound audio track (Opus, 48kHz).
		// We use TrackLocalStaticRTP (not TrackLocalStaticSample) so that
		// SendAudio can construct RTP packets directly and control the marker
		// bit. pion's WriteSample sets the marker bit on every Opus packet,
		// which causes Chrome's NetEq jitter buffer to reset for each frame.
		audioTrack, err := webrtc.NewTrackLocalStaticRTP(
			webrtc.RTPCodecCapability{
				MimeType:  webrtc.MimeTypeOpus,
				ClockRate: 48000,
				Channels:  2, // Opus in WebRTC is always signaled as 2 channels per RFC 7587
			},
			"audio",
			"localai",
		)
		if err != nil {
			pc.Close()
			xlog.Error("failed to create audio track", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create audio track"})
		}

		rtpSender, err := pc.AddTrack(audioTrack)
		if err != nil {
			pc.Close()
			xlog.Error("failed to add audio track", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to add audio track"})
		}

		// Drain RTCP (control protocol) packets we don't have anyting useful to do with
		go func() {
			buf := make([]byte, 1500)
			for {
				if _, _, err := rtpSender.Read(buf); err != nil {
					return
				}
			}
		}()

		// Load the Opus backend
		opusBackend, err := application.ModelLoader().Load(
			model.WithBackendString("opus"),
			model.WithModelID("__opus_codec__"),
			model.WithModel("opus"),
		)
		if err != nil {
			pc.Close()
			xlog.Error("failed to load opus backend", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "opus backend not available"})
		}

		// Create the transport (the data channel is created by the client and
		// received via pc.OnDataChannel inside NewWebRTCTransport)
		transport := NewWebRTCTransport(pc, audioTrack, opusBackend)

		// Handle incoming audio track from the client
		pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			codec := track.Codec()
			if codec.MimeType != webrtc.MimeTypeOpus {
				xlog.Warn("unexpected track codec, ignoring", "mime", codec.MimeType)
				return
			}
			xlog.Debug("Received audio track from client",
				"codec", codec.MimeType,
				"clock_rate", codec.ClockRate,
				"channels", codec.Channels,
				"sdp_fmtp", codec.SDPFmtpLine,
				"payload_type", codec.PayloadType,
			)

			handleIncomingAudioTrack(track, transport)
		})

		// Set the remote SDP (client's offer)
		if err := pc.SetRemoteDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  req.SDP,
		}); err != nil {
			transport.Close()
			xlog.Error("failed to set remote description", "error", err)
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid SDP offer"})
		}

		// Create answer
		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			transport.Close()
			xlog.Error("failed to create answer", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create answer"})
		}

		if err := pc.SetLocalDescription(answer); err != nil {
			transport.Close()
			xlog.Error("failed to set local description", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to set local description"})
		}

		// Wait for ICE gathering to complete (with timeout)
		gatherDone := webrtc.GatheringCompletePromise(pc)
		select {
		case <-gatherDone:
		case <-time.After(10 * time.Second):
			xlog.Warn("ICE gathering timed out, using partial candidates")
		}

		localDesc := pc.LocalDescription()
		if localDesc == nil {
			transport.Close()
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "no local description"})
		}

		sessionID := generateSessionID()

		// Start the realtime session in a goroutine
		evaluator := application.TemplatesEvaluator()
		go func() {
			defer transport.Close()
			runRealtimeSession(application, transport, req.Model, evaluator)
		}()

		return c.JSON(http.StatusCreated, RealtimeCallResponse{
			SDP:       localDesc.SDP,
			SessionID: sessionID,
		})
	}
}

// handleIncomingAudioTrack reads RTP packets from a remote WebRTC track
// and buffers the raw Opus payloads on the session. Decoding is done in
// batches by decodeOpusLoop in realtime.go.
func handleIncomingAudioTrack(track *webrtc.TrackRemote, transport *WebRTCTransport) {
	session := transport.WaitForSession()
	if session == nil {
		xlog.Error("could not find session for incoming audio track (transport closed)")
		sendError(transport, "session_error", "Session failed to start — check server logs", "", "")
		return
	}

	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			xlog.Debug("audio track read ended", "error", err)
			return
		}

		// Copy the payload — pion's ReadRTP may back it by a reusable buffer
		payload := make([]byte, len(pkt.Payload))
		copy(payload, pkt.Payload)

		session.OpusFramesLock.Lock()
		session.OpusFrames = append(session.OpusFrames, payload)
		session.OpusFramesLock.Unlock()
	}
}
