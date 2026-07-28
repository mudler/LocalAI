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
)

var _ = Describe("eagernessMaxSilenceSec", func() {
	DescribeTable("maps eagerness to the no-EOU fallback window",
		func(eagerness string, want float64) {
			Expect(eagernessMaxSilenceSec(eagerness)).To(Equal(want))
		},
		Entry("low", "low", 8.0),
		Entry("medium", "medium", 4.0),
		Entry("high", "high", 2.0),
		Entry("auto equals medium", "auto", 4.0),
		Entry("empty equals medium", "", 4.0),
		Entry("case and space insensitive", " High ", 2.0),
		Entry("unknown equals medium", "frantic", 4.0),
	)
})

var _ = Describe("turnDetectionActive", func() {
	It("is active for server and semantic VAD, inactive otherwise", func() {
		Expect(turnDetectionActive(nil)).To(BeFalse())
		Expect(turnDetectionActive(&types.TurnDetectionUnion{})).To(BeFalse())
		Expect(turnDetectionActive(&types.TurnDetectionUnion{ServerVad: &types.ServerVad{}})).To(BeTrue())
		Expect(turnDetectionActive(&types.TurnDetectionUnion{SemanticVad: &types.RealtimeSessionSemanticVad{}})).To(BeTrue())
	})
})

var _ = Describe("defaultTurnDetection", func() {
	It("keeps the historical server_vad defaults for non-semantic pipelines", func() {
		td := defaultTurnDetection(&config.ModelConfig{})
		Expect(td.ServerVad).NotTo(BeNil())
		Expect(td.SemanticVad).To(BeNil())
		Expect(td.ServerVad.SilenceDurationMs).To(Equal(int64(500)))
		Expect(td.ServerVad.CreateResponse).To(BeTrue())
	})

	It("seeds semantic_vad with the pipeline's eagerness", func() {
		cfg := &config.ModelConfig{}
		cfg.Pipeline.TurnDetection.Type = "semantic_vad"
		cfg.Pipeline.TurnDetection.Eagerness = "high"
		td := defaultTurnDetection(cfg)
		Expect(td.SemanticVad).NotTo(BeNil())
		Expect(td.ServerVad).To(BeNil())
		Expect(td.SemanticVad.Eagerness).To(Equal("high"))
		Expect(td.SemanticVad.CreateResponse).To(BeTrue())
	})

	It("treats a nil config as server_vad", func() {
		Expect(defaultTurnDetection(nil).ServerVad).NotTo(BeNil())
	})
})

var _ = Describe("int16sToFloat32", func() {
	It("scales like the VAD conversion", func() {
		out := int16sToFloat32([]int16{0, 16384, -32768})
		Expect(out).To(HaveLen(3))
		Expect(out[0]).To(BeNumerically("~", 0.0, 1e-6))
		Expect(out[1]).To(BeNumerically("~", 0.5, 1e-6))
		Expect(out[2]).To(BeNumerically("~", -1.0, 1e-6))
	})
})

var _ = Describe("liveTurnState", func() {
	var (
		m   *fakeModel
		lts *liveTurnState
		ftr *fakeTransport
	)

	newSemanticSession := func(m *fakeModel) *Session {
		return &Session{
			InputAudioTranscription: &types.AudioTranscription{},
			ModelInterface:          m,
		}
	}

	BeforeEach(func() {
		m = &fakeModel{}
		ftr = &fakeTransport{}
		lts = newLiveTurnState(newSemanticSession(m), ftr)
	})

	Describe("openTurn", func() {
		It("opens once per turn and reports open()", func() {
			Expect(lts.open()).To(BeFalse())
			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue())
			Expect(lts.open()).To(BeTrue())
			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue(), "idempotent while open")
			Expect(m.liveOpened).To(Equal(1))
		})

		It("degrades stickily when the backend cannot do live transcription", func() {
			m.liveErr = errors.New("rpc error: code = Unimplemented desc = live transcription unsupported")
			Expect(lts.openTurn(context.Background(), "item1")).To(BeFalse())
			Expect(lts.unavailable).To(BeTrue())

			// Later turns never retry: the failure is per-session sticky.
			m.liveErr = nil
			Expect(lts.openTurn(context.Background(), "item1")).To(BeFalse())
			Expect(m.liveOpened).To(Equal(0))
		})
	})

	Describe("feedNewAudio", func() {
		It("feeds only the unfed tail and holds back the final resampled sample", func() {
			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue())

			lts.feedNewAudio([]int16{1, 2, 3, 4})
			Expect(m.liveSession.fed).To(HaveLen(1))
			Expect(m.liveSession.fed[0]).To(HaveLen(3), "last sample held back")

			// Same buffer grown by two samples: only the delta is fed.
			lts.feedNewAudio([]int16{1, 2, 3, 4, 5, 6})
			Expect(m.liveSession.fed).To(HaveLen(2))
			Expect(m.liveSession.fed[1]).To(HaveLen(2))

			// No growth past the holdback: nothing fed.
			lts.feedNewAudio([]int16{1, 2, 3, 4, 5, 6})
			Expect(m.liveSession.fed).To(HaveLen(2))
		})

		It("degrades and closes the turn when a feed fails", func() {
			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue())
			m.liveSession.feedErr = errors.New("backend gone")
			sess := m.liveSession

			lts.feedNewAudio([]int16{1, 2, 3, 4})

			Expect(lts.open()).To(BeFalse())
			Expect(lts.unavailable).To(BeTrue())
			Expect(sess.closed).To(Equal(1))
		})
	})

	Describe("event handling and the dynamic threshold", func() {
		sv := &types.RealtimeSessionSemanticVad{Eagerness: "high"}

		It("uses the eagerness fallback until an EOU is recorded, then commits without an extra window", func() {
			Expect(lts.thresholdSec(false, sv)).To(Equal(2.0))
			Expect(lts.thresholdSec(true, sv)).To(Equal(semanticEouSilenceSec))

			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue())
			lts.session.ModelInterface.(*fakeModel).liveSession.onEvent(backend.LiveTranscriptionEvent{Delta: "hello ", Eou: false})
			lts.session.ModelInterface.(*fakeModel).liveSession.onEvent(backend.LiveTranscriptionEvent{Eou: true})
			lts.drainEvents(3.3)

			Expect(lts.eouAtSec).To(BeNumerically("~", 3.3, 1e-9))
			Expect(lts.previewText()).To(Equal("hello"))
		})

		// The bug this replaces: the (predictive) EOU routinely arrives while
		// silero is still padding the speech segment open. eouPending must NOT
		// read that as resumed speech.
		It("keeps the EOU pending while silero is still closing the same segment", func() {
			lts.eouAtSec = 3.3
			Expect(lts.eouPending([]schema.VADSegment{{Start: 0, End: 0}})).To(BeTrue(), "segment began before the EOU and is merely unclosed")
			Expect(lts.eouPending([]schema.VADSegment{{Start: 0, End: 3.0}})).To(BeTrue(), "and still pending once it closes")
		})

		It("drops the EOU only when a new utterance starts after it (resumed speech)", func() {
			lts.eouAtSec = 3.3
			Expect(lts.eouPending([]schema.VADSegment{{Start: 0, End: 3.0}, {Start: 4.0, End: 0}})).To(BeFalse())
			Expect(lts.eouPending([]schema.VADSegment{{Start: 0, End: 3.0}, {Start: 4.0, End: 5.0}})).To(BeFalse())
		})

		It("has no pending EOU before one is recorded", func() {
			Expect(lts.eouPending([]schema.VADSegment{{Start: 0, End: 3.0}})).To(BeFalse())
			Expect(lts.eouPending(nil)).To(BeFalse())
		})

		It("does not arm the commit threshold on an EOB backchannel", func() {
			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue())
			lts.session.ModelInterface.(*fakeModel).liveSession.onEvent(backend.LiveTranscriptionEvent{Delta: "uh-huh", Eob: true})
			lts.drainEvents(2.0)

			Expect(lts.eouAtSec).To(BeZero(), "a backchannel is not the user yielding the turn")
			Expect(lts.eouPending([]schema.VADSegment{{Start: 0, End: 1.8}})).To(BeFalse(), "still on the eagerness fallback")
			Expect(lts.previewText()).To(Equal("uh-huh"), "the backchannel text still lands in the transcript")
		})

		It("reports the commit trigger and the EOU token's lag behind speech end", func() {
			trigger, lag := lts.commitTrigger(false, 3.2)
			Expect(trigger).To(Equal("timeout"))
			Expect(lag).To(BeZero())

			lts.eouAtSec = 3.5
			trigger, lag = lts.commitTrigger(true, 3.2)
			Expect(trigger).To(Equal("eou"))
			Expect(lag).To(BeNumerically("~", 0.3, 1e-9))
		})
	})

	Describe("finishTurn", func() {
		It("finalizes the stream, prefers the Final text, and resets for the next turn", func() {
			m.liveCloseEvents = []backend.LiveTranscriptionEvent{
				{Delta: " world"},
				{Final: &schema.TranscriptionResult{Text: "hello world", Eou: true}},
			}
			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue())
			sess := m.liveSession
			sess.onEvent(backend.LiveTranscriptionEvent{Delta: "hello", Eou: true})
			lts.drainEvents(2.0)

			ut := lts.finishTurn(2.5)

			Expect(sess.closed).To(Equal(1))
			Expect(ut).NotTo(BeNil())
			Expect(ut.Text).To(Equal("hello world"), "Final event text wins over joined deltas")
			Expect(lts.open()).To(BeFalse())
			Expect(lts.eouAtSec).To(BeZero())
			Expect(lts.parts).To(BeEmpty())
			Expect(lts.fed16k).To(BeZero())
		})

		It("returns nil when the stream heard nothing", func() {
			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue())
			Expect(lts.finishTurn(1.0)).To(BeNil())
			Expect(m.liveSession.closed).To(Equal(1))
		})

		It("is a no-op without an open stream", func() {
			Expect(lts.finishTurn(1.0)).To(BeNil())
		})
	})

	Describe("discardTurn", func() {
		It("closes the stream, drops the transcript and retracts streamed captions", func() {
			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue())
			sess := m.liveSession
			sess.onEvent(backend.LiveTranscriptionEvent{Delta: "noise"})
			lts.drainEvents(1.0)

			lts.discardTurn()

			Expect(sess.closed).To(Equal(1))
			Expect(lts.open()).To(BeFalse())
			Expect(lts.parts).To(BeEmpty())
			Expect(ftr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionFailed)).To(Equal(1),
				"the client saw caption deltas for this turn — it must be told to drop them")
		})

		It("sends no failed event when no captions ever reached the client", func() {
			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue())
			lts.discardTurn()
			Expect(ftr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionFailed)).To(Equal(0))
		})
	})

	Describe("live captions", func() {
		It("streams each delta to the client under the turn's item id as it drains", func() {
			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue())
			turnID := lts.itemID
			Expect(turnID).NotTo(BeEmpty(), "the item id exists from turn open so captions can reference it")

			m.liveSession.onEvent(backend.LiveTranscriptionEvent{Delta: "hel"})
			m.liveSession.onEvent(backend.LiveTranscriptionEvent{Delta: "lo"})
			lts.drainEvents(1.0)

			var got []types.ConversationItemInputAudioTranscriptionDeltaEvent
			for _, e := range ftr.events {
				if d, ok := e.(types.ConversationItemInputAudioTranscriptionDeltaEvent); ok {
					got = append(got, d)
				}
			}
			Expect(got).To(HaveLen(2))
			Expect(got[0].Delta).To(Equal("hel"))
			Expect(got[1].Delta).To(Equal("lo"))
			Expect(got[0].ItemID).To(Equal(turnID))
			Expect(got[1].ItemID).To(Equal(turnID))
			Expect(lts.deltasSent).To(BeTrue())
		})

		It("finishTurn does not retract captions — the commit's completed event supersedes them", func() {
			Expect(lts.openTurn(context.Background(), "item1")).To(BeTrue())
			m.liveSession.onEvent(backend.LiveTranscriptionEvent{Delta: "hello"})
			lts.drainEvents(1.0)

			Expect(lts.finishTurn(1.5)).NotTo(BeNil())
			Expect(ftr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionFailed)).To(Equal(0))
		})
	})
})

// commitUtteranceWithTranscript routes the three transcript sources: the
// retranscribe gate's batch decode, the live stream's accumulated text, and
// the historical file path.
var _ = Describe("commitUtteranceWithTranscript", func() {
	newTranscriptionOnlySession := func(m *fakeModel, streamTranscription bool) *Session {
		cfg := &config.ModelConfig{}
		if streamTranscription {
			on := true
			cfg.Pipeline.Streaming.Transcription = &on
		}
		return &Session{
			TranscriptionOnly:       true, // stop after the transcript: no LLM/TTS in these specs
			InputAudioTranscription: &types.AudioTranscription{},
			ModelConfig:             cfg,
			ModelInterface:          m,
		}
	}

	It("uses the gate's batch transcript and never re-runs the backend", func() {
		m := &fakeModel{transcribeErr: errors.New("must not be called")}
		session := newTranscriptionOnlySession(m, true)
		tr := &fakeTransport{}

		commitUtteranceWithTranscript(context.Background(), []byte{1, 2}, nil,
			&schema.TranscriptionResult{Text: "batch text", Eou: true}, "item_turn", session, &Conversation{}, tr)

		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionDelta)).To(Equal(0))
		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
	})

	It("emits only the completed event for a live transcript — captions already streamed during the turn", func() {
		m := &fakeModel{transcribeErr: errors.New("must not be called")}
		session := newTranscriptionOnlySession(m, true)
		tr := &fakeTransport{}

		commitUtteranceWithTranscript(context.Background(), []byte{1, 2},
			&liveUtterance{Text: "hello"}, nil, "item_turn", session, &Conversation{}, tr)

		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionDelta)).To(Equal(0))
		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))

		var completed types.ConversationItemInputAudioTranscriptionCompletedEvent
		for _, e := range tr.events {
			if c, ok := e.(types.ConversationItemInputAudioTranscriptionCompletedEvent); ok {
				completed = c
			}
		}
		Expect(completed.ItemID).To(Equal("item_turn"),
			"completed must reuse the caption deltas' item id so the client replaces, not duplicates")
		Expect(completed.Transcript).To(Equal("hello"))
	})

	It("falls back to the file path when the live stream heard nothing", func() {
		m := &fakeModel{transcribeFinal: &schema.TranscriptionResult{Text: "from file"}}
		session := newTranscriptionOnlySession(m, false)
		tr := &fakeTransport{}

		commitUtteranceWithTranscript(context.Background(), []byte{1, 2},
			&liveUtterance{}, nil, "", session, &Conversation{}, tr)

		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
	})
})

// transcribeUtterance is the retranscribe gate's offline decode of the
// buffered turn.
var _ = Describe("transcribeUtterance", func() {
	It("returns the batch decode with its Eou flag", func() {
		m := &fakeModel{transcribeFinal: &schema.TranscriptionResult{Text: "confirmed", Eou: true}}
		session := &Session{
			InputAudioTranscription: &types.AudioTranscription{},
			ModelInterface:          m,
		}

		tr, err := transcribeUtterance(context.Background(), []byte{0, 0, 1, 1}, session)
		Expect(err).ToNot(HaveOccurred())
		Expect(tr.Text).To(Equal("confirmed"))
		Expect(tr.Eou).To(BeTrue())
	})

	It("propagates backend errors", func() {
		m := &fakeModel{transcribeErr: errors.New("engine fell over")}
		session := &Session{
			InputAudioTranscription: &types.AudioTranscription{},
			ModelInterface:          m,
		}

		_, err := transcribeUtterance(context.Background(), []byte{0, 0}, session)
		Expect(err).To(MatchError(ContainSubstring("engine fell over")))
	})
})

// emitPrecomputedTranscription replays an already-produced transcript as the
// standard delta/completed event sequence.
var _ = Describe("emitPrecomputedTranscription", func() {
	It("emits deltas then completed, sharing the item id", func() {
		tr := &fakeTransport{}
		Expect(emitPrecomputedTranscription(tr, "item42", []string{"a", "", "b"}, "ab")).To(Succeed())

		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionDelta)).To(Equal(2), "empty deltas skipped")
		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
		for _, e := range tr.events {
			switch ev := e.(type) {
			case types.ConversationItemInputAudioTranscriptionDeltaEvent:
				Expect(ev.ItemID).To(Equal("item42"))
			case types.ConversationItemInputAudioTranscriptionCompletedEvent:
				Expect(ev.ItemID).To(Equal("item42"))
				Expect(ev.Transcript).To(Equal("ab"))
			}
		}
	})

	It("emits only the completed event with no deltas", func() {
		tr := &fakeTransport{}
		Expect(emitPrecomputedTranscription(tr, "item1", nil, "hi")).To(Succeed())
		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionDelta)).To(Equal(0))
		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
	})
})
