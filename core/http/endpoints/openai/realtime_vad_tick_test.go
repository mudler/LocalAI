package openai

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/turncoord"
	"github.com/mudler/LocalAI/core/schema"
)

// vadTick specs drive one synchronous turn-detection inspection at a time
// (no ticker), the same way classifySoundWindow's specs drive the
// sound-detection loop. The fake VAD answers in the coordinates of the audio
// it is HANDED — i.e. scan-clip coordinates once the buffer outgrows the
// window — exactly like the real backend.
var _ = Describe("vadTick", func() {
	const rate = 16000 // InputSampleRate == localSampleRate: resample is a copy

	// pcm returns sec seconds of silent 16-bit PCM; content is irrelevant to
	// the scripted VAD.
	pcm := func(sec float64) []byte {
		return make([]byte, int(sec*rate)*2)
	}
	bufferSec := func(s *Session) float64 {
		return float64(len(s.InputAudioBuffer)) / (rate * 2)
	}

	newHarness := func(td *types.TurnDetectionUnion, m *fakeModel) (*Session, *fakeTransport, *turnSink) {
		session := &Session{
			TranscriptionOnly:       true, // commit stops after the transcription events
			TurnDetection:           td,
			InputAudioTranscription: &types.AudioTranscription{},
			ModelConfig:             &config.ModelConfig{},
			ModelInterface:          m,
			InputSampleRate:         rate,
			respSink:                newResponseSink(),
		}
		tr := &fakeTransport{}
		sink := newTurnSink(session, &Conversation{}, tr, newLiveTurnState(session, tr), context.Background(), time.Now())
		return session, tr, sink
	}
	serverVad := &types.TurnDetectionUnion{ServerVad: &types.ServerVad{SilenceDurationMs: 500}}
	semanticHigh := &types.TurnDetectionUnion{SemanticVad: &types.RealtimeSessionSemanticVad{Eagerness: "high"}}

	speaking := func(sink *turnSink) bool {
		_, ok := sink.coord.State().(turncoord.Speaking)
		return ok
	}

	It("commits a normal short turn (extraction is behavior-neutral)", func() {
		m := &fakeModel{
			vadSegments:     []schema.VADSegment{{Start: 0.1, End: 0.6}},
			transcribeFinal: &schema.TranscriptionResult{Text: "go up"},
		}
		session, tr, sink := newHarness(serverVad, m)
		session.InputAudioBuffer = pcm(1.4) // under the 1.5s scan window: no clip

		vadTick(sink, 0.5)

		Expect(tr.countEvents(types.ServerEventTypeInputAudioBufferSpeechStarted)).To(Equal(1))
		Expect(tr.countEvents(types.ServerEventTypeInputAudioBufferSpeechStopped)).To(Equal(1))
		Expect(tr.countEvents(types.ServerEventTypeInputAudioBufferCommitted)).To(Equal(1))
		Expect(session.InputAudioBuffer).To(BeEmpty(), "commit drops the whole inspected window")
		Expect(speaking(sink)).To(BeFalse())

		session.respSink.wait()
		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
	})

	It("hands the VAD only the scan window and rebases its answer", func() {
		var scanned []int
		m := &fakeModel{
			vadFn: func(req *schema.VADRequest) (*schema.VADResponse, error) {
				scanned = append(scanned, len(req.Audio))
				// Clip coordinates: speech ends 0.9s into the 1.5s window,
				// leaving 0.6s of trailing silence > the 0.5s threshold.
				return &schema.VADResponse{Segments: []schema.VADSegment{{Start: 0.2, End: 0.9}}}, nil
			},
			transcribeFinal: &schema.TranscriptionResult{Text: "clipped"},
		}
		session, tr, sink := newHarness(serverVad, m)
		session.InputAudioBuffer = pcm(20)

		vadTick(sink, 0.5)

		Expect(scanned).To(Equal([]int{int(1.5 * rate)}), "server_vad window = silence 0.5s + 1s margin")
		Expect(tr.countEvents(types.ServerEventTypeInputAudioBufferCommitted)).To(Equal(1),
			"rebased segment end (18.5+0.9) leaves 0.6s trailing silence in buffer coordinates")
	})

	It("commits when trailing silence outruns the scan window instead of discarding the turn", func() {
		call := 0
		m := &fakeModel{
			vadFn: func(req *schema.VADRequest) (*schema.VADResponse, error) {
				call++
				if call == 1 {
					// Speech still running at the end of the inspected audio.
					return &schema.VADResponse{Segments: []schema.VADSegment{{Start: 0.2, End: 0}}}, nil
				}
				// Later ticks: the (clipped) window is all silence.
				return &schema.VADResponse{}, nil
			},
			transcribeFinal: &schema.TranscriptionResult{Text: "late silence"},
		}
		session, tr, sink := newHarness(serverVad, m)
		session.InputAudioBuffer = pcm(1.4)
		vadTick(sink, 0.5)
		Expect(speaking(sink)).To(BeTrue())
		Expect(tr.countEvents(types.ServerEventTypeInputAudioBufferCommitted)).To(BeZero())

		session.InputAudioBuffer = append(session.InputAudioBuffer, pcm(2.6)...) // 4s total: clip is in effect
		vadTick(sink, 0.5)

		Expect(tr.countEvents(types.ServerEventTypeInputAudioBufferSpeechStopped)).To(Equal(1))
		Expect(tr.countEvents(types.ServerEventTypeInputAudioBufferCommitted)).To(Equal(1))
		Expect(session.InputAudioBuffer).To(BeEmpty())
		Expect(speaking(sink)).To(BeFalse())
		session.respSink.wait()
		Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
	})

	It("stays bounded when segments never stop (the noise-floor pathology)", func() {
		var maxScan int
		m := &fakeModel{
			vadFn: func(req *schema.VADRequest) (*schema.VADResponse, error) {
				if len(req.Audio) > maxScan {
					maxScan = len(req.Audio)
				}
				return &schema.VADResponse{Segments: []schema.VADSegment{{Start: 0.1, End: 0}}}, nil
			},
		}
		session, tr, sink := newHarness(serverVad, m)

		for i := 0; i < 95; i++ {
			session.InputAudioBuffer = append(session.InputAudioBuffer, pcm(1)...)
			vadTick(sink, 0.5)
		}

		Expect(maxScan).To(Equal(int(1.5*rate)), "VAD never rescans more than the window")
		Expect(bufferSec(session)).To(BeNumerically("<=", maxTurnBufferSec), "retention bound holds")
		Expect(speaking(sink)).To(BeTrue(), "the turn is neither committed nor aborted")
		Expect(tr.countEvents(types.ServerEventTypeInputAudioBufferSpeechStarted)).To(Equal(1))
		Expect(tr.countEvents(types.ServerEventTypeInputAudioBufferCommitted)).To(BeZero())
	})

	It("keeps the live feed gapless across a retention trim", func() {
		m := &fakeModel{
			vadFn: func(req *schema.VADRequest) (*schema.VADResponse, error) {
				return &schema.VADResponse{Segments: []schema.VADSegment{{Start: 0.1, End: 0}}}, nil
			},
		}
		session, _, sink := newHarness(semanticHigh, m)
		session.InputAudioBuffer = pcm(2)
		vadTick(sink, 0.5) // opens the turn + live stream, feeds the onset audio
		Expect(m.liveOpened).To(Equal(1))

		session.InputAudioBuffer = append(session.InputAudioBuffer, pcm(89)...) // 91s: over the 90s bound
		vadTick(sink, 0.5)

		Expect(bufferSec(session)).To(BeNumerically("<=", maxTurnBufferSec))
		total := 0
		for _, chunk := range m.liveSession.fed {
			total += len(chunk)
		}
		// Everything ever buffered minus the one held-back resample-edge
		// sample: no gap (undercount) and no re-feed (overcount) across the
		// trim's cursor rebase.
		Expect(total).To(Equal(91*rate-1), "fed samples = all audio seen minus the held-back tail sample")
	})

	It("bounds memory when the VAD backend keeps failing", func() {
		m := &fakeModel{vadErr: errors.New("backend down")}
		session, tr, sink := newHarness(serverVad, m)
		session.InputAudioBuffer = pcm(95)

		vadTick(sink, 0.5)

		Expect(bufferSec(session)).To(BeNumerically("<=", maxTurnBufferSec), "retention trim runs before the VAD call")
		Expect(tr.countEvents(types.ServerEventTypeError)).To(Equal(1))
	})
})

var _ = Describe("vadScanWindowSec", func() {
	It("sizes from the silence the commit test must measure, plus the warm-up margin", func() {
		Expect(vadScanWindowSec(nil, 0.5, nil)).To(Equal(1.5))
		Expect(vadScanWindowSec(&types.RealtimeSessionSemanticVad{Eagerness: "high"}, 0.5, nil)).To(Equal(3.0))
		Expect(vadScanWindowSec(&types.RealtimeSessionSemanticVad{Eagerness: "low"}, 0.5, nil)).To(Equal(9.0))
	})

	It("lets vad_window_sec widen but never narrow the window", func() {
		cfg := &config.ModelConfig{}
		cfg.Pipeline.TurnDetection.VadWindowSec = 10
		Expect(vadScanWindowSec(nil, 0.5, cfg)).To(Equal(10.0))
		cfg.Pipeline.TurnDetection.VadWindowSec = 0.2
		Expect(vadScanWindowSec(nil, 0.5, cfg)).To(Equal(1.5), "values below the floor are ignored")
	})
})
