package openai

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
)

// Regression tests for the client-commit LLM/STT race. With client-driven
// turns (turn_detection null, e.g. a client VAD / speaker gate) the client
// sends input_audio_buffer.commit and response.create back-to-back. Without the
// fix, response.create triggers the LLM immediately while the commit's STT is
// still in flight, so the LLM (fast) wins the race and answers an EMPTY
// transcript; the response on the real transcript is then discarded ("skipping
// response: turn context cancelled during transcription"). The fix makes the
// response wait for the in-flight commit's STT (and user-item append) before
// the LLM starts.
var _ = Describe("realtime client-commit LLM/STT ordering", func() {
	utt := []byte{1, 2, 3, 4}

	newModel := func(texts ...string) *heldTranscribeModel {
		return &heldTranscribeModel{
			fakeModel: &fakeModel{
				cfg:             &config.ModelConfig{},
				predictTokens:   []string{"ok"},
				predictResp:     backend.LLMResponse{Response: "ok"},
				ttsStreamChunks: [][]byte{{1}},
				ttsStreamRate:   24000,
			},
			texts:    texts,
			release:  make(chan struct{}),
			started:  make(chan int32, 8),
			finished: make(chan int32, 8),
		}
	}

	newSession := func(m *heldTranscribeModel) *Session {
		on := true
		session := &Session{
			OutputSampleRate:        24000,
			InputAudioTranscription: &types.AudioTranscription{},
			ModelInterface:          m,
			ModelConfig: &config.ModelConfig{
				Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{LLM: &on, TTS: &on}},
			},
			respSink: newResponseSink(),
		}
		session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
		return session
	}

	userMsgTextsOf := func(m *heldTranscribeModel) []string {
		var out []string
		for _, msg := range m.lastMessages {
			if msg.Role == string(types.MessageRoleUser) {
				out = append(out, msg.StringContent)
			}
		}
		return out
	}

	It("does not start the LLM until the client commit's STT is complete", func() {
		m := newModel("hello there")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		// Client commit — its transcription is held by the double.
		session.issueClientCommit(utt, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))

		// Back-to-back response.create (supersedes the commit's own response).
		// With the fix it WAITS for the held STT: no response is created while
		// the transcription is in flight.
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		Consistently(tr.countEvents(types.ServerEventTypeResponseCreated), 200*time.Millisecond).Should(BeZero())

		// Release the STT; the commit appends its user item and the waiting
		// response now starts on the real transcript.
		close(m.release)
		session.respSink.wait()

		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
		// The LLM saw the user's transcript, not an empty one.
		Expect(userMsgTextsOf(m)).To(Equal([]string{"hello there"}))
	})

	It("still responds to a standalone response.create with no commit in flight", func() {
		m := newModel()
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		// No commit in flight: the response must not wait (no deadlock).
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		session.respSink.wait()

		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
	})
})
