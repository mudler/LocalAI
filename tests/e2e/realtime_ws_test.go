package e2e_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	laudio "github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/sound"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// --- WebSocket test helpers ---

func connectWS(model string) *websocket.Conn {
	u := url.URL{
		Scheme:   "ws",
		Host:     fmt.Sprintf("127.0.0.1:%d", apiPort),
		Path:     "/v1/realtime",
		RawQuery: "model=" + url.QueryEscape(model),
	}
	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "websocket dial failed")
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	return conn
}

func readServerEvent(conn *websocket.Conn, timeout time.Duration) map[string]any {
	conn.SetReadDeadline(time.Now().Add(timeout))
	_, msg, err := conn.ReadMessage()
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "read server event")
	var evt map[string]any
	ExpectWithOffset(1, json.Unmarshal(msg, &evt)).To(Succeed())
	return evt
}

func sendClientEvent(conn *websocket.Conn, event any) {
	data, err := json.Marshal(event)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, conn.WriteMessage(websocket.TextMessage, data)).To(Succeed())
}

// drainUntil reads events until it finds one with the given type, or times out.
func drainUntil(conn *websocket.Conn, eventType string, timeout time.Duration) map[string]any {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		evt := readServerEvent(conn, time.Until(deadline))
		if evt["type"] == eventType {
			return evt
		}
	}
	Fail("timed out waiting for event: " + eventType)
	return nil
}

// generatePCMBase64 creates base64-encoded 16-bit LE PCM of a sine wave.
func generatePCMBase64(freq float64, sampleRate, durationMs int) string {
	numSamples := sampleRate * durationMs / 1000
	pcm := make([]byte, numSamples*2)
	for i := range numSamples {
		t := float64(i) / float64(sampleRate)
		sample := int16(math.MaxInt16 / 2 * math.Sin(2*math.Pi*freq*t))
		pcm[2*i] = byte(sample)
		pcm[2*i+1] = byte(sample >> 8)
	}
	return base64.StdEncoding.EncodeToString(pcm)
}

// padPCMBase64 prepends and appends the given milliseconds of silence to a
// base64-encoded 16-bit LE PCM buffer. Used to give VAD a clear lead-in /
// lead-out so Silero can reliably detect utterance boundaries.
func padPCMBase64(pcmB64 string, sampleRate, leadingMs, trailingMs int) string {
	raw, err := base64.StdEncoding.DecodeString(pcmB64)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	lead := make([]byte, sampleRate*leadingMs/1000*2)
	trail := make([]byte, sampleRate*trailingMs/1000*2)
	padded := make([]byte, 0, len(lead)+len(raw)+len(trail))
	padded = append(padded, lead...)
	padded = append(padded, raw...)
	padded = append(padded, trail...)
	return base64.StdEncoding.EncodeToString(padded)
}

// ttsPCMBase64 drives the /v1/audio/speech endpoint to render `text` through
// the given TTS model, strips the returned WAV header, resamples to the
// requested sample rate if needed, and returns base64-encoded 16-bit LE PCM.
// Fails the test on any transport / format error — there's no useful fallback.
func ttsPCMBase64(model, text string, targetSampleRate int) string {
	body, err := json.Marshal(map[string]any{
		"model":  model,
		"input":  text,
		"format": "wav",
	})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	req, err := http.NewRequest(http.MethodPost, apiURL+"/audio/speech", bytes.NewReader(body))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	defer resp.Body.Close()

	wav, err := io.ReadAll(resp.Body)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, resp.StatusCode).To(Equal(http.StatusOK),
		"tts returned %d: %s", resp.StatusCode, string(wav))

	pcm, srcRate := laudio.ParseWAV(wav)
	ExpectWithOffset(1, srcRate).To(BeNumerically(">", 0),
		"tts response is not a valid WAV (body=%d bytes)", len(wav))

	if srcRate != targetSampleRate {
		samples := sound.BytesToInt16sLE(pcm)
		pcm = sound.Int16toBytesLE(sound.ResampleInt16(samples, srcRate, targetSampleRate))
	}
	return base64.StdEncoding.EncodeToString(pcm)
}

// isRealTTS returns true when REALTIME_TTS names a real backend-backed model,
// as opposed to the default mock-tts. Used to gate test behavior that only
// makes sense with a real TTS — e.g. driving the session with a real
// utterance and asserting the transcription contains recognisable words.
func isRealTTS() bool {
	m := os.Getenv("REALTIME_TTS")
	return m != "" && m != "mock-tts"
}

// pipelineModel returns the model name to use for realtime tests.
func pipelineModel() string {
	if m := os.Getenv("REALTIME_TEST_MODEL"); m != "" {
		return m
	}
	return "realtime-pipeline"
}

// disableVADEvent returns a session.update event that disables server VAD.
func disableVADEvent() map[string]any {
	return map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"audio": map[string]any{
				"input": map[string]any{
					"turn_detection": nil,
				},
			},
		},
	}
}

// --- Tests ---

var _ = Describe("Realtime WebSocket API", Label("Realtime"), func() {
	Context("Session management", func() {
		It("should return session.created on connect", func() {
			conn := connectWS(pipelineModel())
			defer conn.Close()

			evt := readServerEvent(conn, 30*time.Second)
			Expect(evt["type"]).To(Equal("session.created"))

			session, ok := evt["session"].(map[string]any)
			Expect(ok).To(BeTrue(), "session field should be an object")
			Expect(session["id"]).ToNot(BeEmpty())
		})

		It("should return session.updated after session.update", func() {
			conn := connectWS(pipelineModel())
			defer conn.Close()

			// Read session.created
			created := readServerEvent(conn, 30*time.Second)
			Expect(created["type"]).To(Equal("session.created"))

			// Send session.update to disable VAD
			sendClientEvent(conn, disableVADEvent())

			evt := drainUntil(conn, "session.updated", 10*time.Second)
			Expect(evt["type"]).To(Equal("session.updated"))
		})
	})

	Context("Manual audio commit", func() {
		It("should produce a response with audio when audio is committed", func() {
			conn := connectWS(pipelineModel())
			defer conn.Close()

			// Read session.created
			created := readServerEvent(conn, 30*time.Second)
			Expect(created["type"]).To(Equal("session.created"))

			// Disable server VAD so we can manually commit
			sendClientEvent(conn, disableVADEvent())
			drainUntil(conn, "session.updated", 10*time.Second)

			// Real TTS: synthesise an utterance the ASR should be able to
			// recognise, and pad with silence so Silero-VAD has a clear
			// lead-in/out. Fallback: 1s of 440Hz sine wave — the mock
			// transcriber returns a static string anyway, so this only
			// needs to exercise the pipeline plumbing.
			const inputText = "The quick brown fox jumps over the lazy dog."
			var audio string
			if isRealTTS() {
				audio = ttsPCMBase64(os.Getenv("REALTIME_TTS"), inputText, 24000)
				audio = padPCMBase64(audio, 24000, 500, 500)
			} else {
				audio = generatePCMBase64(440, 24000, 1000)
			}
			sendClientEvent(conn, map[string]any{
				"type":  "input_audio_buffer.append",
				"audio": audio,
			})

			// Commit the audio buffer
			sendClientEvent(conn, map[string]any{
				"type": "input_audio_buffer.commit",
			})

			// We should receive the response event sequence.
			// The exact events depend on the pipeline, but we expect at least:
			// - input_audio_buffer.committed
			// - conversation.item.input_audio_transcription.completed
			// - response.output_audio.delta (with base64 audio)
			// - response.done

			committed := drainUntil(conn, "input_audio_buffer.committed", 30*time.Second)
			Expect(committed).ToNot(BeNil())

			// Drain the response cycle, capturing the input transcription
			// event as it arrives so we can sanity-check it alongside the
			// real-TTS path.
			var transcript string
			deadline := time.Now().Add(90 * time.Second)
			for time.Now().Before(deadline) {
				evt := readServerEvent(conn, time.Until(deadline))
				if evt["type"] == "conversation.item.input_audio_transcription.completed" {
					if t, ok := evt["transcript"].(string); ok {
						transcript = t
					}
				}
				if evt["type"] == "response.done" {
					Expect(evt).ToNot(BeNil())
					break
				}
			}

			if isRealTTS() {
				lower := strings.ToLower(transcript)
				matched := strings.Contains(lower, "fox") || strings.Contains(lower, "dog")
				Expect(matched).To(BeTrue(),
					"expected real-TTS transcript to contain 'fox' or 'dog' (got %q)", transcript)
			}
		})
	})

	Context("Text conversation item", func() {
		It("should create a text item and trigger a response", func() {
			conn := connectWS(pipelineModel())
			defer conn.Close()

			// Read session.created
			created := readServerEvent(conn, 30*time.Second)
			Expect(created["type"]).To(Equal("session.created"))

			// Disable VAD
			sendClientEvent(conn, disableVADEvent())
			drainUntil(conn, "session.updated", 10*time.Second)

			// Create a text conversation item
			sendClientEvent(conn, map[string]any{
				"type": "conversation.item.create",
				"item": map[string]any{
					"type": "message",
					"role": "user",
					"content": []map[string]any{
						{
							"type": "input_text",
							"text": "Hello, how are you?",
						},
					},
				},
			})

			// Wait for item to be added
			added := drainUntil(conn, "conversation.item.added", 10*time.Second)
			Expect(added).ToNot(BeNil())

			// Trigger a response
			sendClientEvent(conn, map[string]any{
				"type": "response.create",
			})

			// Wait for response to complete
			done := drainUntil(conn, "response.done", 60*time.Second)
			Expect(done).ToNot(BeNil())
		})
	})

	Context("Audio integrity", func() {
		It("should return non-empty audio data in response.output_audio.delta", Label("real-models"), func() {
			if os.Getenv("REALTIME_TEST_MODEL") == "" {
				Skip("REALTIME_TEST_MODEL not set")
			}

			conn := connectWS(pipelineModel())
			defer conn.Close()

			created := readServerEvent(conn, 30*time.Second)
			Expect(created["type"]).To(Equal("session.created"))

			// Disable VAD
			sendClientEvent(conn, disableVADEvent())
			drainUntil(conn, "session.updated", 10*time.Second)

			// Create a text item and trigger response
			sendClientEvent(conn, map[string]any{
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
			drainUntil(conn, "conversation.item.added", 10*time.Second)

			sendClientEvent(conn, map[string]any{
				"type": "response.create",
			})

			// Collect audio deltas
			var totalAudioBytes int
			deadline := time.Now().Add(60 * time.Second)
			for time.Now().Before(deadline) {
				evt := readServerEvent(conn, time.Until(deadline))
				if evt["type"] == "response.output_audio.delta" {
					if delta, ok := evt["delta"].(string); ok {
						decoded, err := base64.StdEncoding.DecodeString(delta)
						Expect(err).ToNot(HaveOccurred())
						totalAudioBytes += len(decoded)
					}
				}
				if evt["type"] == "response.done" {
					break
				}
			}

			Expect(totalAudioBytes).To(BeNumerically(">", 0), "expected non-empty audio in response")
		})
	})
})
