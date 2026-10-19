package openai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	laudio "github.com/mudler/LocalAI/pkg/audio"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type bufferedSpeechModel struct {
	fakeModel
	engine, dir             string
	clauses                 []string
	early                   chan struct{}
	fail                    error
	cancel                  context.CancelFunc
	streamCalls, unaryCalls int
}

func (m *bufferedSpeechModel) SupportsTTSStream() bool {
	return (&wrappedModel{TTSConfig: &config.ModelConfig{Backend: m.engine}}).SupportsTTSStream()
}
func (m *bufferedSpeechModel) Predict(ctx context.Context, _ schema.Messages, _, _, _ []string, cb func(string, backend.TokenUsage) bool, _ []types.ToolUnion, _ *types.ToolChoiceUnion, _, _ *int, _ map[string]float64) (func() (backend.LLMResponse, error), error) {
	return func() (backend.LLMResponse, error) {
		if !cb("First complete sentence. Next", backend.TokenUsage{}) {
			return backend.LLMResponse{}, ctx.Err()
		}
		select {
		case <-m.early:
		case <-ctx.Done():
			return backend.LLMResponse{}, ctx.Err()
		}
		if !cb(" complete sentence. Final tail.", backend.TokenUsage{}) {
			return backend.LLMResponse{}, ctx.Err()
		}
		return backend.LLMResponse{Response: "First complete sentence. Next complete sentence. Final tail."}, nil
	}, nil
}
func (m *bufferedSpeechModel) TTSStream(ctx context.Context, text, _, _ string, onAudio func([]byte, int) error) error {
	m.streamCalls++
	m.clauses = append(m.clauses, text)
	if m.fail != nil {
		return m.fail
	}
	if err := onAudio([]byte{byte(len(m.clauses)), 0}, 24000); err != nil {
		return err
	}
	if len(m.clauses) == 1 {
		if m.cancel != nil {
			m.cancel()
		}
	}
	return onAudio([]byte{byte(len(m.clauses)), 1}, 24000)
}
func (m *bufferedSpeechModel) TTS(ctx context.Context, text, _, _ string) (string, *pb.Result, error) {
	m.unaryCalls++
	m.clauses = append(m.clauses, text)
	if m.fail != nil {
		return "", nil, m.fail
	}
	file, err := os.CreateTemp(m.dir, "clause-*.wav")
	if err != nil {
		return "", nil, err
	}
	header := laudio.NewWAVHeader(2)
	if err := header.Write(file); err != nil {
		_ = file.Close()
		return "", nil, err
	}
	if _, err := file.Write([]byte{byte(len(m.clauses)), 0}); err != nil {
		_ = file.Close()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		return "", nil, err
	}
	return filepath.Clean(file.Name()), &pb.Result{Success: true}, nil
}

type bufferedAudioTransport struct {
	fakeTransport
	ready chan struct{}
	once  sync.Once
}

func (t *bufferedAudioTransport) SendAudio(ctx context.Context, pcm []byte, rate int) error {
	if err := t.fakeTransport.SendAudio(ctx, pcm, rate); err != nil {
		return err
	}
	t.once.Do(func() { close(t.ready) })
	return nil
}

var _ = Describe("Buffered progressive TTS", func() {
	for _, tc := range []struct {
		engine string
		stream bool
	}{{"sherpa-onnx", true}, {"piper", true}, {"sherpa-onnx", false}} {
		engine := tc.engine
		It(fmt.Sprintf("emits ordered clauses before LLM EOF with %s (stream=%t)", engine, tc.stream), func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			on := true
			m := &bufferedSpeechModel{engine: engine, dir: GinkgoT().TempDir(), early: make(chan struct{})}
			session := &Session{OutputSampleRate: 24000, ModelInterface: m, ModelConfig: &config.ModelConfig{Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{LLM: &on, TTS: &tc.stream, ClauseChunking: &on}}}}
			transport := &bufferedAudioTransport{ready: m.early}
			r := &liveResponse{id: "buffered"}
			streamLLMResponse(ctx, session, &Conversation{}, transport, r, nil, nil, &config.ModelConfig{}, nil, nil, 0)
			Expect(ctx.Err()).NotTo(HaveOccurred(), "audio must reach the transport before the predictor can finish")
			Expect(m.clauses).To(Equal([]string{"First complete sentence.", "Next complete sentence.", "Final tail."}))
			if engine == "piper" || !tc.stream {
				Expect(m.streamCalls).To(BeZero())
				Expect(m.unaryCalls).To(Equal(3))
				Expect(transport.audio).To(HaveLen(3))
			} else {
				Expect(m.streamCalls).To(Equal(3))
				Expect(transport.audio).To(HaveLen(6))
			}
			var sequence []byte
			for _, chunk := range transport.audio {
				sequence = append(sequence, chunk.pcm[0])
			}
			if engine == "piper" || !tc.stream {
				Expect(sequence).To(Equal([]byte{1, 2, 3}))
			} else {
				Expect(sequence).To(Equal([]byte{1, 1, 2, 2, 3, 3}))
			}
		})
	}
	It("cancels a waiting predictor immediately on synthesis error", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		on := true
		m := &bufferedSpeechModel{engine: "sherpa-onnx", early: make(chan struct{}), fail: errors.New("synthesis failed")}
		session := &Session{OutputSampleRate: 24000, ModelInterface: m, ModelConfig: &config.ModelConfig{Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{LLM: &on, TTS: &on, ClauseChunking: &on}}}}
		r := &liveResponse{id: "error"}
		streamLLMResponse(ctx, session, &Conversation{}, &fakeTransport{}, r, nil, nil, &config.ModelConfig{}, nil, nil, 0)
		Expect(ctx.Err()).NotTo(HaveOccurred())
		Expect(r.outcome).To(Equal(outcomeFailed))
		Expect(m.clauses).To(HaveLen(1))
	})
	It("stops queued speech on caller cancellation", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		on := true
		m := &bufferedSpeechModel{engine: "sherpa-onnx", early: make(chan struct{}), cancel: cancel}
		session := &Session{OutputSampleRate: 24000, ModelInterface: m, ModelConfig: &config.ModelConfig{Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{LLM: &on, TTS: &on, ClauseChunking: &on}}}}
		r := &liveResponse{id: "cancel"}
		streamLLMResponse(ctx, session, &Conversation{}, &fakeTransport{}, r, nil, nil, &config.ModelConfig{}, nil, nil, 0)
		Expect(r.outcome).To(Equal(outcomeCancelled))
		Expect(m.clauses).To(HaveLen(1))
	})
})

var _ = Describe("Buffered speech backpressure", func() {
	It("bounds pending clauses and wakes a blocked producer on cancellation", func() {
		p := newLiveSpeechPipeline(context.Background(), func(ctx context.Context, _ <-chan string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() })
		defer p.abort()
		for i := 0; i < cap(p.text); i++ {
			Expect(p.push("queued clause")).To(BeTrue())
		}
		pushed := make(chan bool, 1)
		go func() { pushed <- p.push("blocked clause") }()
		Consistently(pushed, 20*time.Millisecond).ShouldNot(Receive())
		p.cancel()
		Eventually(pushed).Should(Receive(BeFalse()))
		_, err := p.finish()
		Expect(err).To(MatchError(context.Canceled))
	})
})
