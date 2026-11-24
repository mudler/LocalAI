package openai

import (
	"context"
	"fmt"
	proto "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	laudio "github.com/mudler/LocalAI/pkg/audio"
)

// emitSpeech synthesizes a piece of text and forwards the audio to the client,
// streaming a delta per TTS chunk when the pipeline opts in, or sending the
// whole utterance as one delta otherwise.
var _ = Describe("emitSpeech", func() {
	ttsOn := true

	streamingSession := func(m Model) *Session {
		return &Session{
			OutputSampleRate: 24000,
			ModelInterface:   m,
			ModelConfig: &config.ModelConfig{
				Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{TTS: &ttsOn}},
			},
		}
	}

	It("streams one output_audio.delta per TTS chunk when streaming is enabled", func() {
		m := &fakeModel{
			ttsStreamChunks: [][]byte{{1, 2}, {3, 4}, {5, 6}},
			ttsStreamRate:   24000,
		}
		t := &fakeTransport{}

		audio, err := emitSpeech(context.Background(), t, streamingSession(m), "resp1", "item1", "Hello there.")

		Expect(err).ToNot(HaveOccurred())
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioDelta)).To(Equal(3))
		// The returned audio is all chunks concatenated (session output rate).
		Expect(audio).To(Equal([]byte{1, 2, 3, 4, 5, 6}))
	})

	It("sends a single output_audio.delta in unary mode", func() {
		// A minimal real WAV file for the unary TTS path to read + parse.
		f, err := os.CreateTemp("", "emit-*.wav")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = os.Remove(f.Name()) }()
		pcm := make([]byte, 320) // 160 samples of silence
		hdr := laudio.NewWAVHeader(uint32(len(pcm)))
		Expect(hdr.Write(f)).To(Succeed())
		_, err = f.Write(pcm)
		Expect(err).ToNot(HaveOccurred())
		Expect(f.Close()).To(Succeed())

		session := &Session{
			OutputSampleRate: 24000,
			ModelInterface:   &fakeModel{ttsFile: f.Name()},
			ModelConfig:      &config.ModelConfig{}, // streaming off
		}
		t := &fakeTransport{}

		_, err = emitSpeech(context.Background(), t, session, "resp1", "item1", "Hello there.")

		Expect(err).ToNot(HaveOccurred())
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioDelta)).To(Equal(1))
	})
})

type unsupportedStreamingSpeech struct {
	fakeModel
	emitBeforeError bool
	unaryCalls      int
}

func (m *unsupportedStreamingSpeech) TTSStream(_ context.Context, _, _, _ string, onAudio func([]byte, int) error) error {
	if m.emitBeforeError {
		if err := onAudio([]byte{1, 2}, 24000); err != nil {
			return err
		}
	}
	return status.Error(codes.Unimplemented, "streaming unavailable")
}
func (m *unsupportedStreamingSpeech) TTS(ctx context.Context, text, voice, language string) (string, *proto.Result, error) {
	m.unaryCalls++
	return m.fakeModel.TTS(ctx, text, voice, language)
}

var _ = Describe("TTS streaming capability fallback", func() {
	for _, partial := range []bool{false, true} {
		It(fmt.Sprintf("replays only unsupported streams without committed PCM (partial=%t)", partial), func() {
			path := filepath.Join(GinkgoT().TempDir(), "speech.wav")
			f, err := os.Create(path)
			Expect(err).NotTo(HaveOccurred())
			header := laudio.NewWAVHeader(2)
			Expect(header.Write(f)).To(Succeed())
			_, err = f.Write([]byte{3, 4})
			Expect(err).NotTo(HaveOccurred())
			Expect(f.Close()).To(Succeed())
			model := &unsupportedStreamingSpeech{fakeModel: fakeModel{ttsFile: path}, emitBeforeError: partial}
			on := true
			session := &Session{OutputSampleRate: 24000, ModelInterface: model, ModelConfig: &config.ModelConfig{Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{TTS: &on}}}}
			transport := &fakeTransport{}
			_, err = emitSpeech(context.Background(), transport, session, "response", "item", "Hello")
			if partial {
				Expect(status.Code(err)).To(Equal(codes.Unimplemented))
				Expect(model.unaryCalls).To(BeZero())
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(model.unaryCalls).To(Equal(1))
			}
			Expect(transport.countEvents(types.ServerEventTypeResponseOutputAudioDelta)).To(Equal(1))
		})
	}
})
