package openai

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
)

// wavCaptureModel records the sample rate and data size of every WAV file a
// commit hands to the transcription backend.
type wavCaptureModel struct {
	*fakeModel
	mu       sync.Mutex
	rates    []int
	dataLens []int
}

func (m *wavCaptureModel) capture(path string) {
	b, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())
	Expect(len(b)).To(BeNumerically(">=", 44))
	Expect(bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WAVE"))).To(BeTrue())
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rates = append(m.rates, int(binary.LittleEndian.Uint32(b[24:28])))
	m.dataLens = append(m.dataLens, len(b)-44)
}

func (m *wavCaptureModel) Transcribe(ctx context.Context, audio, language string, translate, diarize bool, prompt string) (*schema.TranscriptionResult, error) {
	m.capture(audio)
	return m.fakeModel.Transcribe(ctx, audio, language, translate, diarize, prompt)
}

func (m *wavCaptureModel) TranscribeStream(ctx context.Context, audio, language string, translate, diarize bool, prompt string, onDelta func(text string)) (*schema.TranscriptionResult, error) {
	m.capture(audio)
	return m.fakeModel.TranscribeStream(ctx, audio, language, translate, diarize, prompt, onDelta)
}

// Regression: the committed-utterance WAV is always written with a 16 kHz
// header, so the audio inside must be 16 kHz on every commit path — also for
// a 24 kHz session. The client-commit path used to pass the raw 24 kHz buffer
// (decoded at 1.5x speed: garbage or empty transcript); the VAD path stores
// audio that is already resampled and must stay untouched.
var _ = Describe("committed-utterance WAV sample rate", func() {
	const sessionRate = 24000

	newModel := func() *wavCaptureModel {
		return &wavCaptureModel{fakeModel: &fakeModel{
			vadSegments:     []schema.VADSegment{{Start: 0.1, End: 0.6}},
			transcribeFinal: &schema.TranscriptionResult{Text: "hello"},
		}}
	}
	newSession := func(m *wavCaptureModel, td *types.TurnDetectionUnion) *Session {
		return &Session{
			TranscriptionOnly:       true, // stop after the transcript
			TurnDetection:           td,
			InputAudioTranscription: &types.AudioTranscription{},
			ModelConfig:             &config.ModelConfig{},
			ModelInterface:          m,
			InputSampleRate:         sessionRate,
			respSink:                newResponseSink(),
		}
	}

	It("resamples a 24 kHz client commit to 16 kHz", func() {
		m := newModel()
		session := newSession(m, nil)
		utt := make([]byte, sessionRate*2) // 1 s of 16-bit mono PCM at 24 kHz

		commitUtterance(context.Background(), utt, session, &Conversation{}, &fakeTransport{}, session.nextCommitSlot())

		Expect(m.rates).To(Equal([]int{localSampleRate}))
		Expect(m.dataLens[0]).To(BeNumerically("~", localSampleRate*2, 4), "1 s at 16 kHz")
	})

	It("writes the VAD commit of a 24 kHz session as 16 kHz audio", func() {
		m := newModel()
		session := newSession(m, &types.TurnDetectionUnion{ServerVad: &types.ServerVad{SilenceDurationMs: 500}})
		tr := &fakeTransport{}
		sink := newTurnSink(session, &Conversation{}, tr, newLiveTurnState(session, tr), context.Background(), time.Now())
		session.InputAudioBuffer = make([]byte, int(1.4*sessionRate)*2) // 1.4 s at 24 kHz

		vadTick(sink, 0.5)
		session.respSink.wait()

		Expect(tr.countEvents(types.ServerEventTypeInputAudioBufferCommitted)).To(Equal(1))
		Expect(m.rates).To(Equal([]int{localSampleRate}))
		Expect(m.dataLens[0]).To(BeNumerically("~", int(1.4*localSampleRate)*2, 4), "1.4 s at 16 kHz")
	})
})
