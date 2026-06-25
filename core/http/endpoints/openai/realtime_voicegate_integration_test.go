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

var _ = Describe("realtime speaker surfacing (commitUtterance)", func() {
	utt := make([]byte, 32)

	It("emits conversation.item.speaker for a confident match when announce is on", func() {
		session, _ := itSession(itGate("alice", "alice", []float32{1, 0, 0}, nil,
			config.VoiceGateWhenEvery, config.VoiceGateRejectEvent))
		session.voiceGate.cfg.Identity = &config.VoiceIdentityConfig{Announce: true}
		tr := &fakeTransport{}

		commitUtterance(context.Background(), utt, session, &Conversation{}, tr)

		Expect(tr.countEvents(types.ServerEventTypeConversationItemSpeaker)).To(Equal(1))
	})

	It("does not emit the speaker event for an unknown speaker unless announce_unknown is set", func() {
		// match distance above threshold => not matched
		gate := &voiceGate{
			cfg: config.PipelineVoiceRecognition{
				Mode: config.VoiceGateModeIdentify, Threshold: 0.25,
				When: config.VoiceGateWhenEvery, OnReject: config.VoiceGateRejectEvent,
				Enforce:  boolPtr(false),
				Identity: &config.VoiceIdentityConfig{Announce: true},
			},
			registry: &fakeRegistry{matches: []voicerecognition.Match{
				{Distance: 0.9, Metadata: voicerecognition.Metadata{Name: "alice"}},
			}},
			embedFn: func(context.Context, string) ([]float32, error) { return []float32{1, 0, 0}, nil },
		}
		session, _ := itSession(gate)
		tr := &fakeTransport{}

		commitUtterance(context.Background(), utt, session, &Conversation{}, tr)
		Expect(tr.countEvents(types.ServerEventTypeConversationItemSpeaker)).To(Equal(0))

		gate.cfg.Identity.AnnounceUnknown = true
		tr2 := &fakeTransport{}
		commitUtterance(context.Background(), utt, session, &Conversation{}, tr2)
		Expect(tr2.countEvents(types.ServerEventTypeConversationItemSpeaker)).To(Equal(1))
	})

	It("never drops a turn when enforce is false even for a disallowed speaker", func() {
		session, _ := itSession(itGate("bob", "alice", []float32{1, 0, 0}, nil,
			config.VoiceGateWhenEvery, config.VoiceGateRejectEvent))
		session.voiceGate.cfg.Enforce = boolPtr(false)
		tr := &fakeTransport{}

		commitUtterance(context.Background(), utt, session, &Conversation{}, tr)

		Expect(hasSpeakerNotAuthorized(tr)).To(BeFalse())
		Expect(tr.countEvents(types.ServerEventTypeResponseDone)).To(BeNumerically(">=", 1))
	})
})

var _ = Describe("realtime speaker personalization (triggerResponseAtTurn)", func() {
	utt := make([]byte, 32)

	findRole := func(msgs schema.Messages, role string) *schema.Message {
		for i := range msgs {
			if msgs[i].Role == role {
				return &msgs[i]
			}
		}
		return nil
	}

	It("sets the user message name and a current-speaker system note", func() {
		session, m := itSession(itGate("alice", "alice", []float32{1, 0, 0}, nil,
			config.VoiceGateWhenEvery, config.VoiceGateRejectEvent))
		session.voiceGate.cfg.Identity = &config.VoiceIdentityConfig{
			Personalize: true, InjectName: true, InjectSystemNote: true,
		}
		session.Instructions = "You are helpful."
		tr := &fakeTransport{}

		commitUtterance(context.Background(), utt, session, &Conversation{}, tr)

		user := findRole(m.lastMessages, "user")
		Expect(user).ToNot(BeNil())
		Expect(user.Name).To(Equal("alice"))
		sys := findRole(m.lastMessages, "system")
		Expect(sys).ToNot(BeNil())
		Expect(sys.StringContent).To(ContainSubstring("The current speaker is alice."))
	})

	It("omits the unknown note unless note_unknown is set", func() {
		base := func() (*Session, *fakeModel) {
			gate := &voiceGate{
				cfg: config.PipelineVoiceRecognition{
					Mode: config.VoiceGateModeIdentify, Threshold: 0.25,
					When: config.VoiceGateWhenEvery, OnReject: config.VoiceGateRejectEvent,
					Enforce:  boolPtr(false),
					Identity: &config.VoiceIdentityConfig{Personalize: true, InjectSystemNote: true},
				},
				registry: &fakeRegistry{matches: []voicerecognition.Match{
					{Distance: 0.9, Metadata: voicerecognition.Metadata{Name: "alice"}},
				}},
				embedFn: func(context.Context, string) ([]float32, error) { return []float32{1, 0, 0}, nil },
			}
			s, m := itSession(gate)
			s.Instructions = "You are helpful."
			return s, m
		}

		s1, m1 := base()
		commitUtterance(context.Background(), utt, s1, &Conversation{}, &fakeTransport{})
		Expect(findRole(m1.lastMessages, "system").StringContent).ToNot(ContainSubstring("unknown"))

		s2, m2 := base()
		s2.voiceGate.cfg.Identity.NoteUnknown = true
		commitUtterance(context.Background(), utt, s2, &Conversation{}, &fakeTransport{})
		Expect(findRole(m2.lastMessages, "system").StringContent).To(ContainSubstring("The current speaker is unknown."))
	})
})

var _ = Describe("realtime when:first with identity (commitUtterance)", func() {
	utt := make([]byte, 32)

	// statefulIdentityGate builds a when:first identify gate with an Identity
	// block (so identity is resolved every turn) whose embedFn is driven by a
	// per-turn counter: the failOnSecond flag makes the second and later embeds
	// return an error, exercising the stricter fail-closed path on a re-resolve.
	statefulIdentityGate := func(failOnSecond bool) *voiceGate {
		calls := 0
		return &voiceGate{
			cfg: config.PipelineVoiceRecognition{
				Mode:      config.VoiceGateModeIdentify,
				Threshold: 0.25,
				When:      config.VoiceGateWhenFirst,
				OnReject:  config.VoiceGateRejectEvent,
				Allow:     config.VoiceRecognitionAllow{Names: []string{"alice"}},
				Identity:  &config.VoiceIdentityConfig{Announce: true, Personalize: true, InjectName: true},
			},
			registry: &fakeRegistry{matches: []voicerecognition.Match{
				{Distance: 0.1, Metadata: voicerecognition.Metadata{Name: "alice"}},
			}},
			embedFn: func(context.Context, string) ([]float32, error) {
				calls++
				if failOnSecond && calls > 1 {
					return nil, errors.New("embed backend down")
				}
				return []float32{1, 0, 0}, nil
			},
		}
	}

	It("re-resolves identity every turn and fails closed when a later embed errors", func() {
		gate := statefulIdentityGate(true)
		session, _ := itSession(gate)
		conv := &Conversation{} // shared so voiceVerified persists across turns

		// Turn 1: authorized; identity resolved, speaker surfaced, response runs.
		tr1 := &fakeTransport{}
		commitUtterance(context.Background(), utt, session, conv, tr1)
		Expect(hasSpeakerNotAuthorized(tr1)).To(BeFalse())
		Expect(tr1.countEvents(types.ServerEventTypeConversationItemSpeaker)).To(Equal(1))
		Expect(tr1.countEvents(types.ServerEventTypeResponseDone)).To(BeNumerically(">=", 1))

		// Turn 2: when:first would skip re-authorization, but the Identity block
		// forces a fresh resolve. That resolve now errors, and because the gate
		// enforces, the turn is dropped fail-closed rather than riding on the
		// cached first verification.
		tr2 := &fakeTransport{}
		commitUtterance(context.Background(), utt, session, conv, tr2)
		Expect(hasSpeakerNotAuthorized(tr2)).To(BeTrue())
		Expect(tr2.countEvents(types.ServerEventTypeResponseDone)).To(Equal(0))
	})

	It("re-resolves identity every turn so a later turn still surfaces and names the speaker", func() {
		gate := statefulIdentityGate(false)
		session, m := itSession(gate)
		conv := &Conversation{}

		tr1 := &fakeTransport{}
		commitUtterance(context.Background(), utt, session, conv, tr1)
		Expect(hasSpeakerNotAuthorized(tr1)).To(BeFalse())
		Expect(tr1.countEvents(types.ServerEventTypeResponseDone)).To(BeNumerically(">=", 1))

		// Turn 2: authorization is skipped (when:first, already verified) but the
		// speaker event still fires and the per-message name is set, proving the
		// per-turn re-resolution (not the cached first verification) drove it.
		tr2 := &fakeTransport{}
		commitUtterance(context.Background(), utt, session, conv, tr2)
		Expect(tr2.countEvents(types.ServerEventTypeConversationItemSpeaker)).To(Equal(1))
		var lastUser *schema.Message
		for i := range m.lastMessages {
			if m.lastMessages[i].Role == "user" {
				lastUser = &m.lastMessages[i]
			}
		}
		Expect(lastUser).ToNot(BeNil())
		Expect(lastUser.Name).To(Equal("alice"))
	})
})

var _ = Describe("realtime multi-speaker history attribution (triggerResponse)", func() {
	userAudioItem := func(name, transcript string) *types.MessageItemUnion {
		return &types.MessageItemUnion{
			User: &types.MessageItemUser{
				ID:      generateItemID(),
				Status:  types.ItemStatusCompleted,
				Speaker: &types.Speaker{Name: name, Matched: true},
				Content: []types.MessageContentInput{
					{Type: types.MessageContentTypeInputAudio, Transcript: transcript},
				},
			},
		}
	}

	It("attributes each user turn to its own speaker and notes the latest one", func() {
		session, m := itSession(itGate("alice", "alice", []float32{1, 0, 0}, nil,
			config.VoiceGateWhenEvery, config.VoiceGateRejectEvent))
		session.Instructions = "You are helpful."
		session.MaxHistoryItems = 10 // keep both items; 0 would mean "no trim" too
		session.voiceGate.cfg.Identity = &config.VoiceIdentityConfig{
			Personalize: true, InjectName: true, InjectSystemNote: true,
		}

		conv := &Conversation{Items: []*types.MessageItemUnion{
			userAudioItem("alice", "hello there"),
			userAudioItem("bob", "what is the weather"),
		}}
		tr := &fakeTransport{}

		triggerResponse(context.Background(), session, conv, tr, nil)

		var users []*schema.Message
		var sys *schema.Message
		for i := range m.lastMessages {
			switch m.lastMessages[i].Role {
			case "user":
				users = append(users, &m.lastMessages[i])
			case "system":
				if sys == nil {
					sys = &m.lastMessages[i]
				}
			}
		}
		Expect(users).To(HaveLen(2))
		Expect(users[0].Name).To(Equal("alice"))
		Expect(users[1].Name).To(Equal("bob"))

		Expect(sys).ToNot(BeNil())
		Expect(sys.StringContent).To(ContainSubstring("The current speaker is bob."))
		Expect(sys.StringContent).ToNot(ContainSubstring("alice"))
	})
})

func boolPtr(b bool) *bool { return &b }
