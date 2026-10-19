package openai

import (
	"context"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	"reflect"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
)

type liveRouteModel struct {
	fakeModel
	early          chan struct{}
	received       []string
	calls          int
	failure        error
	failAfterAudio bool
}

func (*liveRouteModel) SupportsTTSLive() bool { return true }
func (m *liveRouteModel) TTSLive(ctx context.Context, text <-chan string, _, _ string, audio func([]byte, int) error) error {
	m.calls++
	if m.failure != nil && !m.failAfterAudio {
		return m.failure
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case part, ok := <-text:
			if !ok {
				return nil
			}
			m.received = append(m.received, part)
			if err := audio([]byte{byte(len(m.received)), 0}, 24000); err != nil {
				return err
			}
			if len(m.received) == 1 {
				close(m.early)
			}
			if m.failure != nil {
				return m.failure
			}
		}
	}
}
func (m *liveRouteModel) Predict(ctx context.Context, _ schema.Messages, _, _, _ []string, cb func(string, backend.TokenUsage) bool, _ []types.ToolUnion, _ *types.ToolChoiceUnion, _, _ *int, _ map[string]float64) (func() (backend.LLMResponse, error), error) {
	return func() (backend.LLMResponse, error) {
		if !cb("First ", backend.TokenUsage{}) {
			return backend.LLMResponse{}, errors.New("callback stopped")
		}
		// No second text, input EOS, or completed LLM response exists at this point.
		select {
		case <-m.early:
		case <-ctx.Done():
			return backend.LLMResponse{}, ctx.Err()
		}
		if !cb("late text.", backend.TokenUsage{}) {
			return backend.LLMResponse{}, errors.New("callback stopped")
		}
		return backend.LLMResponse{Response: "First late text."}, nil
	}, nil
}

var _ = Describe("TTSLiveRealtimeRoute", func() {
	It("preserves the live synthesis contract", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		on := true
		m := &liveRouteModel{early: make(chan struct{})}
		session := &Session{OutputSampleRate: 24000, ModelInterface: m, ModelConfig: &config.ModelConfig{Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{LLM: &on, TTS: &on, ClauseChunking: &on}}}}
		transport := &fakeTransport{}
		if !streamLLMResponse(ctx, session, &Conversation{}, transport, &liveResponse{id: "live"}, nil, nil, &config.ModelConfig{}, nil, nil, 0) {
			Fail(fmt.Sprint("route unhandled"))
		}
		if ctx.Err() != nil {
			Fail(fmt.Sprint("no audio before LLM finished", ctx.Err()))
		}
		if !reflect.DeepEqual(m.received, []string{"First ", "late text."}) || m.calls != 1 {
			Fail(fmt.Sprintf("received %v in %d sessions", m.received, m.calls))
		}
		if len(transport.audio) != 2 {
			Fail(fmt.Sprintf("audio chunks %d", len(transport.audio)))
		}
	})
})
var _ = Describe("TTSLivePipelineCancellation", func() {
	It("preserves the live synthesis contract", func() {
		started := make(chan struct{})
		stopped := make(chan struct{})
		p := newLiveSpeechPipeline(context.Background(), func(ctx context.Context, _ <-chan string) ([]byte, error) {
			close(started)
			defer close(stopped)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		<-started
		p.abort()
		select {
		case <-stopped:
		case <-time.After(time.Second):
			Fail(fmt.Sprint("worker leaked"))
		}
		if p.push("late") {
			Fail(fmt.Sprint("accepted text after cancellation"))
		}
	})
})

var _ = Describe("TTSLiveRealtimeFailureCancelsPrediction", func() {
	It("preserves the live synthesis contract", func() {
		for _, afterAudio := range []bool{false, true} {
			By(map[bool]string{false: "before PCM", true: "after PCM"}[afterAudio])
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				on := true
				m := &liveRouteModel{early: make(chan struct{}), failure: errors.New("native failed"), failAfterAudio: afterAudio}
				session := &Session{OutputSampleRate: 24000, ModelInterface: m, ModelConfig: &config.ModelConfig{Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{LLM: &on, TTS: &on}}}}
				transport := &fakeTransport{}
				r := &liveResponse{id: "failure"}
				streamLLMResponse(ctx, session, &Conversation{}, transport, r, nil, nil, &config.ModelConfig{}, nil, nil, 0)
				if ctx.Err() != nil {
					Fail(fmt.Sprint("TTS failure did not cancel in-flight prediction promptly"))
				}
				if r.outcome != outcomeFailed || m.calls != 1 {
					Fail(fmt.Sprintf("outcome=%v sessions=%d", r.outcome, m.calls))
				}
			}()
		}
	})
})
