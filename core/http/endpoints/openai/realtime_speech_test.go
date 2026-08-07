package openai

import (
	"context"
	"os"

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

	It("emits the first PCM delta before TTS generation completes", func() {
		firstChunk := make(chan struct{})
		release := make(chan struct{})
		m := &fakeModel{
			ttsStreamChunks: [][]byte{{1, 2}, {3, 4}},
			ttsStreamRate:   24000,
			ttsStreamAfter: func(index int) {
				if index == 0 {
					close(firstChunk)
					<-release
				}
			},
		}
		t := &fakeTransport{}
		done := make(chan error, 1)
		go func() {
			_, err := emitSpeech(context.Background(), t, streamingSession(m), "resp1", "item1", "Hello there.")
			done <- err
		}()

		Eventually(firstChunk).Should(BeClosed())
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioDelta)).To(Equal(1))
		Consistently(done, "50ms").ShouldNot(Receive())
		close(release)
		Expect(<-done).ToNot(HaveOccurred())
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioDelta)).To(Equal(2))
	})

	It("does not emit a streaming delta after the response context is cancelled", func() {
		m := &fakeModel{
			ttsStreamChunks: [][]byte{{1, 2}},
			ttsStreamRate:   24000,
		}
		t := &fakeTransport{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := emitSpeech(ctx, t, streamingSession(m), "resp1", "item1", "Hello there.")

		Expect(err).To(MatchError(context.Canceled))
		Expect(t.audio).To(BeEmpty())
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioDelta)).To(Equal(0))
	})

	It("stops before the next delta when cancellation lands between chunks", func() {
		ctx, cancel := context.WithCancel(context.Background())
		m := &fakeModel{
			ttsStreamChunks: [][]byte{{1, 2}, {3, 4}},
			ttsStreamRate:   24000,
			ttsStreamAfter: func(index int) {
				if index == 0 {
					cancel()
				}
			},
		}
		t := &fakeTransport{}

		_, err := emitSpeech(ctx, t, streamingSession(m), "resp1", "item1", "Hello there.")

		Expect(err).To(MatchError(context.Canceled))
		Expect(t.countEvents(types.ServerEventTypeResponseOutputAudioDelta)).To(Equal(1))
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
