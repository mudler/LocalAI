package e2e_test

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// --- helpers: DC-biased PCM/WAV for the voice-recognition gate e2e ---
//
// The mock-backend embeds audio to one of two orthogonal "speaker" vectors
// based on the signed DC offset of the samples (see voiceEmbedFromWAV in the
// mock-backend). A positive bias is the authorized speaker (matches the
// enrolled reference); a negative bias is an unauthorized one.

// pcmWithDC returns 16-bit LE mono PCM of a sine wave plus a constant DC bias.
func pcmWithDC(freq float64, sampleRate, durationMs int, dc int16) []byte {
	numSamples := sampleRate * durationMs / 1000
	pcm := make([]byte, numSamples*2)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		v := float64(dc) + math.MaxInt16/4*math.Sin(2*math.Pi*freq*t)
		if v > math.MaxInt16 {
			v = math.MaxInt16
		}
		if v < math.MinInt16 {
			v = math.MinInt16
		}
		s := int16(v)
		pcm[2*i] = byte(s)
		pcm[2*i+1] = byte(s >> 8)
	}
	return pcm
}

// wavFromPCM wraps 16-bit LE mono PCM in a canonical 44-byte WAV header.
func wavFromPCM(pcm []byte, sampleRate int) []byte {
	var hdr [44]byte
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+len(pcm)))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(hdr[20:22], 1)  // audio format = PCM
	binary.LittleEndian.PutUint16(hdr[22:24], 1)  // channels = mono
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(hdr[28:32], uint32(sampleRate*2)) // byte rate
	binary.LittleEndian.PutUint16(hdr[32:34], 2)                    // block align
	binary.LittleEndian.PutUint16(hdr[34:36], 16)                   // bits per sample
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(len(pcm)))
	return append(hdr[:], pcm...)
}

var _ = Describe("Realtime voice recognition gate", Label("Realtime"), func() {
	// open connects to the gated pipeline and disables server VAD so we can
	// commit manually.
	open := func() *websocket.Conn {
		c := connectWS("realtime-pipeline-gated")
		created := readServerEvent(c, 30*time.Second)
		Expect(created["type"]).To(Equal("session.created"))
		sendClientEvent(c, disableVADEvent())
		drainUntil(c, "session.updated", 10*time.Second)
		return c
	}

	// commit appends raw PCM (base64) and commits the input buffer.
	commit := func(c *websocket.Conn, pcm []byte) {
		sendClientEvent(c, map[string]any{
			"type":  "input_audio_buffer.append",
			"audio": base64.StdEncoding.EncodeToString(pcm),
		})
		sendClientEvent(c, map[string]any{"type": "input_audio_buffer.commit"})
	}

	It("admits an authorized speaker through to a full response", func() {
		c := open()
		defer c.Close()

		// Positive DC bias matches the enrolled reference speaker.
		commit(c, pcmWithDC(300, 16000, 1000, 8000))
		drainUntil(c, "input_audio_buffer.committed", 30*time.Second)

		var gotDone, gotReject bool
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			evt := readServerEvent(c, time.Until(deadline))
			if evt["type"] == "error" {
				if e, ok := evt["error"].(map[string]any); ok && e["code"] == "speaker_not_authorized" {
					gotReject = true
				}
			}
			if evt["type"] == "response.done" {
				gotDone = true
				break
			}
		}
		Expect(gotReject).To(BeFalse(), "authorized speaker must not be rejected")
		Expect(gotDone).To(BeTrue(), "authorized speaker should reach response.done")
	})

	It("drops an unauthorized speaker before the LLM with a reject event", func() {
		c := open()
		defer c.Close()

		// Negative DC bias is a different speaker, not within threshold.
		commit(c, pcmWithDC(300, 16000, 1000, -8000))
		drainUntil(c, "input_audio_buffer.committed", 30*time.Second)

		var gotReject, gotDone bool
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			evt := readServerEvent(c, time.Until(deadline))
			switch evt["type"] {
			case "error":
				if e, ok := evt["error"].(map[string]any); ok && e["code"] == "speaker_not_authorized" {
					gotReject = true
				}
			case "response.done":
				gotDone = true
			}
			if gotReject {
				break
			}
		}
		Expect(gotReject).To(BeTrue(), "unauthorized speaker should get a speaker_not_authorized event")
		Expect(gotDone).To(BeFalse(), "unauthorized speaker must not reach the LLM/response.done")
	})
})
