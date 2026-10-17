package openai

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
)

// Regression test: ASR backends like Nemotron-3.5 append end-of-utterance
// language tags to transcripts ("<de-DE>", "<en-US>", ...). The committed
// transcript feeds the LLM conversation (the user item's Transcript field).
// The per-utterance tag is unreliable for short/ambiguous utterances (a
// German "Hallo." tagged <en-US>) and the LLM treats it as a language
// signal, switching the reply language against the session instructions.
// The tags must be stripped from the LLM context while the emitted
// transcription event keeps the raw transcript (debugging / client display).
var _ = Describe("stripEOULanguageTags", func() {
	It("removes a trailing EOU tag", func() {
		Expect(stripEOULanguageTags("Hallo. <en-US>")).To(Equal("Hallo."))
	})

	It("removes mid-text EOU tags and normalizes whitespace", func() {
		Expect(stripEOULanguageTags("My name is Benjamin Zimmer. <en-US> Ich hatte gerne eine Festnetzrufnummer. <de-DE>")).
			To(Equal("My name is Benjamin Zimmer. Ich hatte gerne eine Festnetzrufnummer."))
	})

	It("handles 3-letter primary subtags", func() {
		Expect(stripEOULanguageTags("Hello. <eng>")).To(Equal("Hello."))
	})

	It("leaves plain text untouched", func() {
		Expect(stripEOULanguageTags("Guten Tag, wie kann ich helfen?")).
			To(Equal("Guten Tag, wie kann ich helfen?"))
	})
})

var _ = Describe("client commit with an EOU-tagged transcript", func() {
	utt := []byte{1, 2, 3, 4}

	newModel := func(text string) *heldTranscribeModel {
		return &heldTranscribeModel{
			fakeModel: &fakeModel{
				cfg:             &config.ModelConfig{},
				predictTokens:   []string{"ok"},
				predictResp:     backend.LLMResponse{Response: "ok"},
				ttsStreamChunks: [][]byte{{1}},
				ttsStreamRate:   24000,
			},
			texts:    []string{text},
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

	It("keeps the raw tag in the emitted event but strips it from the LLM context", func() {
		m := newModel("Hallo. <en-US>")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		session.issueClientCommit(utt, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))
		close(m.release)
		session.respSink.wait()

		// The emitted transcription event keeps the raw transcript.
		var emitted string
		for _, ev := range tr.events() {
			if c, ok := ev.(types.ConversationItemInputAudioTranscriptionCompletedEvent); ok {
				emitted = c.Transcript
			}
		}
		Expect(emitted).To(Equal("Hallo. <en-US>"))

		// The LLM context (user message) must be tag-free.
		var llmUser string
		for _, msg := range m.lastMessages {
			if msg.Role == string(types.MessageRoleUser) {
				llmUser = msg.StringContent
			}
		}
		Expect(llmUser).To(Equal("Hallo."))
	})
})
