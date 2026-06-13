package openai

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
)

// These specs drive the REAL commitUtterance path end to end (gate goroutine,
// the hard join before the LLM, the reject event, and when:first session
// trust) using the existing fakeTransport/fakeModel doubles. They are the
// integration counterpart to the unit specs in realtime_voicegate_test.go:
// here the gate is wired into a Session exactly as runRealtimeSession wires it.

// itGate builds an identify-mode gate whose registry always returns a single
// match named matchName, and whose embedFn returns embed/embErr. allowName is
// the authorized identity. when/onReject select the policy.
func itGate(allowName, matchName string, embed []float32, embErr error, when, onReject string) *voiceGate {
	return &voiceGate{
		cfg: config.PipelineVoiceRecognition{
			Mode:      config.VoiceGateModeIdentify,
			Threshold: 0.25,
			When:      when,
			OnReject:  onReject,
			Allow:     config.VoiceRecognitionAllow{Names: []string{allowName}},
		},
		registry: &fakeRegistry{matches: []voicerecognition.Match{
			{Distance: 0.1, Metadata: voicerecognition.Metadata{Name: matchName}},
		}},
		embedFn: func(context.Context, string) ([]float32, error) { return embed, embErr },
	}
}

// itSession returns a Session + fakeModel wired for a full pipeline turn, with
// the given gate attached. The fakeModel mirrors the streaming-LLM setup used
// by realtime_stream_test.go so triggerResponse runs to a response.done.
func itSession(gate *voiceGate) (*Session, *fakeModel) {
	on := true
	m := &fakeModel{
		cfg:             &config.ModelConfig{},
		transcribeFinal: &schema.TranscriptionResult{Text: "hello"},
		predictTokens:   []string{"Hi", " there."},
		predictResp:     backend.LLMResponse{Response: "Hi there."},
		ttsStreamChunks: [][]byte{{1}},
		ttsStreamRate:   24000,
	}
	session := &Session{
		OutputSampleRate:        24000,
		InputAudioTranscription: &types.AudioTranscription{},
		ModelInterface:          m,
		ModelConfig: &config.ModelConfig{
			Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{LLM: &on, TTS: &on}},
		},
		voiceGate: gate,
	}
	return session, m
}

// hasSpeakerNotAuthorized reports whether a speaker_not_authorized error event
// was emitted to the client.
func hasSpeakerNotAuthorized(tr *fakeTransport) bool {
	for _, e := range tr.events {
		if ev, ok := e.(types.ErrorEvent); ok && ev.Error.Code == "speaker_not_authorized" {
			return true
		}
	}
	return false
}

var _ = Describe("realtime voice gate integration (commitUtterance)", func() {
	utt := make([]byte, 32) // non-empty PCM so commitUtterance proceeds

	It("allows an authorized speaker through to a full response", func() {
		session, _ := itSession(itGate("alice", "alice", []float32{1, 0, 0}, nil,
			config.VoiceGateWhenEvery, config.VoiceGateRejectEvent))
		tr := &fakeTransport{}

		commitUtterance(context.Background(), utt, session, &Conversation{}, tr)

		Expect(hasSpeakerNotAuthorized(tr)).To(BeFalse())
		// The LLM/TTS pipeline ran to completion.
		Expect(tr.countEvents(types.ServerEventTypeResponseDone)).To(BeNumerically(">=", 1))
		// Transcription still happened (parallel with the gate).
		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
	})

	It("drops an unauthorized speaker before the LLM and emits a reject event", func() {
		// match name "mallory" is not in the allow list → deny.
		session, _ := itSession(itGate("alice", "mallory", []float32{1, 0, 0}, nil,
			config.VoiceGateWhenEvery, config.VoiceGateRejectEvent))
		tr := &fakeTransport{}

		commitUtterance(context.Background(), utt, session, &Conversation{}, tr)

		// Hard barrier: the LLM/TTS pipeline never ran.
		Expect(tr.countEvents(types.ServerEventTypeResponseDone)).To(Equal(0))
		// The client was told why.
		Expect(hasSpeakerNotAuthorized(tr)).To(BeTrue())
		// Transcription of the rejected utterance still emitted (sent only to the
		// peer that produced the audio; reveals nothing new).
		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
	})

	It("fails closed on a gate backend error", func() {
		session, _ := itSession(itGate("alice", "alice", nil, errors.New("backend down"),
			config.VoiceGateWhenEvery, config.VoiceGateRejectEvent))
		tr := &fakeTransport{}

		commitUtterance(context.Background(), utt, session, &Conversation{}, tr)

		Expect(tr.countEvents(types.ServerEventTypeResponseDone)).To(Equal(0))
		Expect(hasSpeakerNotAuthorized(tr)).To(BeTrue())
	})

	It("drops silently when on_reject is drop_silent (no error event)", func() {
		session, _ := itSession(itGate("alice", "mallory", []float32{1, 0, 0}, nil,
			config.VoiceGateWhenEvery, config.VoiceGateRejectSilent))
		tr := &fakeTransport{}

		commitUtterance(context.Background(), utt, session, &Conversation{}, tr)

		Expect(tr.countEvents(types.ServerEventTypeResponseDone)).To(Equal(0))
		Expect(hasSpeakerNotAuthorized(tr)).To(BeFalse())
	})

	It("when:first trusts the session after one match, even if later embeds fail", func() {
		gate := itGate("alice", "alice", []float32{1, 0, 0}, nil,
			config.VoiceGateWhenFirst, config.VoiceGateRejectEvent)
		session, _ := itSession(gate)

		// First utterance: authorized, marks the session verified.
		tr1 := &fakeTransport{}
		commitUtterance(context.Background(), utt, session, &Conversation{}, tr1)
		Expect(hasSpeakerNotAuthorized(tr1)).To(BeFalse())
		Expect(tr1.countEvents(types.ServerEventTypeResponseDone)).To(BeNumerically(">=", 1))

		// Break the gate: any further Authorize would now error.
		gate.embedFn = func(context.Context, string) ([]float32, error) { return nil, errors.New("boom") }

		// Second utterance still proceeds because when:first skips re-verification.
		tr2 := &fakeTransport{}
		commitUtterance(context.Background(), utt, session, &Conversation{}, tr2)
		Expect(hasSpeakerNotAuthorized(tr2)).To(BeFalse())
		Expect(tr2.countEvents(types.ServerEventTypeResponseDone)).To(BeNumerically(">=", 1))
	})
})
