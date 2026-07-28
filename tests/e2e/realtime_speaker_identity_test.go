package e2e_test

import (
	"encoding/base64"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs drive the speaker-identity surfacing end to end against a real
// LocalAI server over a real WebSocket, using the mock backend's VoiceEmbed
// (DC-biased PCM -> one of two orthogonal speaker vectors). The pipeline is
// realtime-pipeline-identity: verify mode with enforce:false plus an identity
// block, so the server resolves the speaker, emits a conversation.item.speaker
// event, and never drops a turn.
var _ = Describe("Realtime speaker identity surfacing", Label("Realtime"), func() {
	// open connects to the identity pipeline and disables server VAD so the
	// test can commit the input buffer manually.
	open := func() *websocket.Conn {
		c := connectWS("realtime-pipeline-identity")
		created := readServerEvent(c, 30*time.Second)
		Expect(created["type"]).To(Equal("session.created"))
		sendClientEvent(c, disableVADEvent())
		drainUntil(c, "session.updated", 10*time.Second)
		return c
	}

	commit := func(c *websocket.Conn, pcm []byte) {
		sendClientEvent(c, map[string]any{
			"type":  "input_audio_buffer.append",
			"audio": base64.StdEncoding.EncodeToString(pcm),
		})
		sendClientEvent(c, map[string]any{"type": "input_audio_buffer.commit"})
	}

	// collectUntilDone reads events until response.done (or timeout), returning
	// the conversation.item.speaker event (nil if none) and whether the turn
	// reached response.done.
	collectUntilDone := func(c *websocket.Conn, timeout time.Duration) (speaker map[string]any, gotDone bool) {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			evt := readServerEvent(c, time.Until(deadline))
			switch evt["type"] {
			case "conversation.item.speaker":
				speaker = evt
			case "response.done":
				return speaker, true
			}
		}
		return speaker, false
	}

	It("emits conversation.item.speaker naming an authorized speaker and still responds", func() {
		c := open()
		defer func() { _ = c.Close() }()

		// Positive DC bias matches the enrolled reference speaker.
		commit(c, pcmWithDC(300, 16000, 1000, 8000))
		drainUntil(c, "input_audio_buffer.committed", 30*time.Second)

		speaker, gotDone := collectUntilDone(c, 60*time.Second)
		Expect(speaker).ToNot(BeNil(), "expected a conversation.item.speaker event")
		Expect(speaker["item_id"]).ToNot(BeEmpty())

		spk, ok := speaker["speaker"].(map[string]any)
		Expect(ok).To(BeTrue(), "speaker payload should be an object")
		Expect(spk["matched"]).To(Equal(true))
		Expect(spk["name"]).To(Equal("e2e-speaker"))

		Expect(gotDone).To(BeTrue(), "enforce:false should let the turn reach response.done")
	})

	It("emits an unknown speaker event and still responds when enforce is false", func() {
		c := open()
		defer func() { _ = c.Close() }()

		// Negative DC bias is a different speaker that matches no reference.
		commit(c, pcmWithDC(300, 16000, 1000, -8000))
		drainUntil(c, "input_audio_buffer.committed", 30*time.Second)

		speaker, gotDone := collectUntilDone(c, 60*time.Second)
		Expect(speaker).ToNot(BeNil(), "announce_unknown should still emit the event")

		spk, ok := speaker["speaker"].(map[string]any)
		Expect(ok).To(BeTrue(), "speaker payload should be an object")
		Expect(spk["matched"]).To(Equal(false))
		// name is omitted for an unidentified speaker.
		_, hasName := spk["name"]
		Expect(hasName).To(BeFalse())

		Expect(gotDone).To(BeTrue(), "enforce:false must not drop an unauthorized speaker")
	})
})
