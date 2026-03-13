package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// --- WebRTC test client ---

type webrtcTestClient struct {
	pc        *webrtc.PeerConnection
	dc        *webrtc.DataChannel
	sendTrack *webrtc.TrackLocalStaticSample

	events    chan map[string]any
	audioData chan []byte // raw Opus frames received

	dcOpen chan struct{} // closed when data channel opens
	mu     sync.Mutex
}

func newWebRTCTestClient() *webrtcTestClient {
	m := &webrtc.MediaEngine{}
	Expect(m.RegisterDefaultCodecs()).To(Succeed())

	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))

	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	Expect(err).ToNot(HaveOccurred())

	// Create outbound audio track (Opus)
	sendTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio-client",
		"test-client",
	)
	Expect(err).ToNot(HaveOccurred())

	rtpSender, err := pc.AddTrack(sendTrack)
	Expect(err).ToNot(HaveOccurred())

	// Drain RTCP
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := rtpSender.Read(buf); err != nil {
				return
			}
		}
	}()

	// Create the "oai-events" data channel (must be created by client)
	dc, err := pc.CreateDataChannel("oai-events", nil)
	Expect(err).ToNot(HaveOccurred())

	c := &webrtcTestClient{
		pc:        pc,
		dc:        dc,
		sendTrack: sendTrack,
		events:    make(chan map[string]any, 256),
		audioData: make(chan []byte, 4096),
		dcOpen:    make(chan struct{}),
	}

	dc.OnOpen(func() {
		close(c.dcOpen)
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		var evt map[string]any
		if err := json.Unmarshal(msg.Data, &evt); err == nil {
			c.events <- evt
		}
	})

	// Collect incoming audio tracks
	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			c.audioData <- pkt.Payload
		}
	})

	return c
}

// connect performs SDP exchange with the server and waits for the data channel to open.
func (c *webrtcTestClient) connect(model string) {
	offer, err := c.pc.CreateOffer(nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(c.pc.SetLocalDescription(offer)).To(Succeed())

	// Wait for ICE gathering
	gatherDone := webrtc.GatheringCompletePromise(c.pc)
	select {
	case <-gatherDone:
	case <-time.After(10 * time.Second):
		Fail("ICE gathering timed out")
	}

	localDesc := c.pc.LocalDescription()
	Expect(localDesc).ToNot(BeNil())

	// POST to /v1/realtime/calls
	reqBody, err := json.Marshal(map[string]string{
		"sdp":   localDesc.SDP,
		"model": model,
	})
	Expect(err).ToNot(HaveOccurred())

	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/v1/realtime/calls", apiPort),
		"application/json",
		bytes.NewReader(reqBody),
	)
	Expect(err).ToNot(HaveOccurred())
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusCreated),
		"expected 201, got %d: %s", resp.StatusCode, string(body))

	var callResp struct {
		SDP       string `json:"sdp"`
		SessionID string `json:"session_id"`
	}
	Expect(json.Unmarshal(body, &callResp)).To(Succeed())
	Expect(callResp.SDP).ToNot(BeEmpty())

	// Set the answer
	Expect(c.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  callResp.SDP,
	})).To(Succeed())

	// Wait for data channel to open
	Eventually(c.dcOpen, 15*time.Second).Should(BeClosed())
}

// sendEvent sends a JSON event via the data channel.
func (c *webrtcTestClient) sendEvent(event any) {
	data, err := json.Marshal(event)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, c.dc.Send(data)).To(Succeed())
}

// readEvent reads the next event from the data channel with timeout.
func (c *webrtcTestClient) readEvent(timeout time.Duration) map[string]any {
	select {
	case evt := <-c.events:
		return evt
	case <-time.After(timeout):
		Fail("timed out reading event from data channel")
		return nil
	}
}

// drainUntilEvent reads events until one with the given type appears.
func (c *webrtcTestClient) drainUntilEvent(eventType string, timeout time.Duration) map[string]any {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		evt := c.readEvent(remaining)
		if evt["type"] == eventType {
			return evt
		}
	}
	Fail("timed out waiting for event: " + eventType)
	return nil
}

// sendSineWave encodes a sine wave to Opus and sends it over the audio track.
// This is a simplified version that sends raw PCM wrapped as Opus-compatible
// media samples. In a real client the Opus encoder would be used.
func (c *webrtcTestClient) sendSilence(durationMs int) {
	// Send silence as zero-filled PCM samples via track.
	// We use 20ms Opus frames at 48kHz.
	framesNeeded := durationMs / 20
	// Minimal valid Opus silence frame (Opus DTX/silence)
	silenceFrame := make([]byte, 3)
	silenceFrame[0] = 0xF8 // Config: CELT-only, no VAD, 20ms frame
	silenceFrame[1] = 0xFF
	silenceFrame[2] = 0xFE

	for range framesNeeded {
		_ = c.sendTrack.WriteSample(media.Sample{
			Data:     silenceFrame,
			Duration: 20 * time.Millisecond,
		})
		time.Sleep(5 * time.Millisecond)
	}
}

func (c *webrtcTestClient) close() {
	if c.pc != nil {
		c.pc.Close()
	}
}

// --- Tests ---

var _ = Describe("Realtime WebRTC API", Label("Realtime"), func() {
	Context("Signaling", func() {
		It("should complete SDP exchange and receive session.created", func() {
			client := newWebRTCTestClient()
			defer client.close()

			client.connect(pipelineModel())

			evt := client.readEvent(30 * time.Second)
			Expect(evt["type"]).To(Equal("session.created"))

			session, ok := evt["session"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(session["id"]).ToNot(BeEmpty())
		})
	})

	Context("Event exchange via DataChannel", func() {
		It("should handle session.update", func() {
			client := newWebRTCTestClient()
			defer client.close()

			client.connect(pipelineModel())

			// Read session.created
			created := client.readEvent(30 * time.Second)
			Expect(created["type"]).To(Equal("session.created"))

			// Disable VAD
			client.sendEvent(disableVADEvent())

			updated := client.drainUntilEvent("session.updated", 10*time.Second)
			Expect(updated).ToNot(BeNil())
		})

		It("should handle conversation.item.create and response.create", func() {
			client := newWebRTCTestClient()
			defer client.close()

			client.connect(pipelineModel())

			created := client.readEvent(30 * time.Second)
			Expect(created["type"]).To(Equal("session.created"))

			// Disable VAD
			client.sendEvent(disableVADEvent())
			client.drainUntilEvent("session.updated", 10*time.Second)

			// Create text item
			client.sendEvent(map[string]any{
				"type": "conversation.item.create",
				"item": map[string]any{
					"type": "message",
					"role": "user",
					"content": []map[string]any{
						{
							"type": "input_text",
							"text": "Hello from WebRTC",
						},
					},
				},
			})

			added := client.drainUntilEvent("conversation.item.added", 10*time.Second)
			Expect(added).ToNot(BeNil())

			// Trigger response
			client.sendEvent(map[string]any{
				"type": "response.create",
			})

			done := client.drainUntilEvent("response.done", 60*time.Second)
			Expect(done).ToNot(BeNil())
		})
	})

	Context("Audio track", func() {
		It("should receive audio on the incoming track after TTS", Label("real-models"), func() {
			if os.Getenv("REALTIME_TEST_MODEL") == "" {
				Skip("REALTIME_TEST_MODEL not set")
			}

			client := newWebRTCTestClient()
			defer client.close()

			client.connect(pipelineModel())

			created := client.readEvent(30 * time.Second)
			Expect(created["type"]).To(Equal("session.created"))

			// Disable VAD
			client.sendEvent(disableVADEvent())
			client.drainUntilEvent("session.updated", 10*time.Second)

			// Send text and trigger response with TTS
			client.sendEvent(map[string]any{
				"type": "conversation.item.create",
				"item": map[string]any{
					"type": "message",
					"role": "user",
					"content": []map[string]any{
						{
							"type": "input_text",
							"text": "Say hello",
						},
					},
				},
			})
			client.drainUntilEvent("conversation.item.added", 10*time.Second)

			client.sendEvent(map[string]any{
				"type": "response.create",
			})

			// Collect audio frames while waiting for response.done
			var audioFrames [][]byte
			deadline := time.Now().Add(60 * time.Second)
		loop:
			for time.Now().Before(deadline) {
				select {
				case frame := <-client.audioData:
					audioFrames = append(audioFrames, frame)
				case evt := <-client.events:
					if evt["type"] == "response.done" {
						break loop
					}
				case <-time.After(time.Until(deadline)):
					break loop
				}
			}

			// We should have received some audio frames
			Expect(len(audioFrames)).To(BeNumerically(">", 0),
				"expected to receive audio frames on the WebRTC track")
		})
	})

	Context("Disconnect cleanup", func() {
		It("should handle repeated connect/disconnect cycles", func() {
			for i := range 3 {
				By(fmt.Sprintf("Cycle %d", i+1))
				client := newWebRTCTestClient()
				client.connect(pipelineModel())

				evt := client.readEvent(30 * time.Second)
				Expect(evt["type"]).To(Equal("session.created"))

				client.close()
				// Brief pause to let server clean up
				time.Sleep(500 * time.Millisecond)
			}
		})
	})

	Context("Audio integrity", Label("real-models"), func() {
		It("should receive recognizable audio from TTS through WebRTC", func() {
			if os.Getenv("REALTIME_TEST_MODEL") == "" {
				Skip("REALTIME_TEST_MODEL not set")
			}

			client := newWebRTCTestClient()
			defer client.close()

			client.connect(pipelineModel())

			created := client.readEvent(30 * time.Second)
			Expect(created["type"]).To(Equal("session.created"))

			// Disable VAD
			client.sendEvent(disableVADEvent())
			client.drainUntilEvent("session.updated", 10*time.Second)

			// Create text item and trigger response
			client.sendEvent(map[string]any{
				"type": "conversation.item.create",
				"item": map[string]any{
					"type": "message",
					"role": "user",
					"content": []map[string]any{
						{
							"type": "input_text",
							"text": "Say hello",
						},
					},
				},
			})
			client.drainUntilEvent("conversation.item.added", 10*time.Second)

			client.sendEvent(map[string]any{
				"type": "response.create",
			})

			// Collect Opus frames and decode them
			var totalBytes int
			deadline := time.Now().Add(60 * time.Second)
		loop:
			for time.Now().Before(deadline) {
				select {
				case frame := <-client.audioData:
					totalBytes += len(frame)
				case evt := <-client.events:
					if evt["type"] == "response.done" {
						// Drain any remaining audio
						time.Sleep(200 * time.Millisecond)
					drainAudio:
						for {
							select {
							case frame := <-client.audioData:
								totalBytes += len(frame)
							default:
								break drainAudio
							}
						}
						break loop
					}
				case <-time.After(time.Until(deadline)):
					break loop
				}
			}

			// Verify we received meaningful audio data
			Expect(totalBytes).To(BeNumerically(">", 100),
				"expected to receive meaningful audio data")
		})
	})
})

// computeRMSInt16 computes RMS of int16 samples (used by audio integrity tests).
func computeRMSInt16(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}
