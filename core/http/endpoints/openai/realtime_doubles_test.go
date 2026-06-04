package openai

import (
	"context"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
)

// fakeTransport records the server events and audio sent to a realtime client
// so streaming behaviour can be asserted without a real WebSocket/WebRTC peer.
// It is not a *WebRTCTransport, so handler code takes the WebSocket path.
type fakeTransport struct {
	events []types.ServerEvent
	audio  []fakeAudioChunk
}

type fakeAudioChunk struct {
	pcm        []byte
	sampleRate int
}

func (f *fakeTransport) SendEvent(e types.ServerEvent) error {
	f.events = append(f.events, e)
	return nil
}

func (f *fakeTransport) ReadEvent() ([]byte, error) { return nil, nil }

func (f *fakeTransport) SendAudio(_ context.Context, pcm []byte, sampleRate int) error {
	f.audio = append(f.audio, fakeAudioChunk{pcm: pcm, sampleRate: sampleRate})
	return nil
}

func (f *fakeTransport) Close() error { return nil }

// countEvents returns how many recorded events have the given type.
func (f *fakeTransport) countEvents(et types.ServerEventType) int {
	n := 0
	for _, e := range f.events {
		if e.ServerEventType() == et {
			n++
		}
	}
	return n
}

// fakeModel is a configurable Model double. TTSStream replays ttsStreamChunks
// and TranscribeStream replays transcribeDeltas, so the handler's streaming
// paths can be driven deterministically.
type fakeModel struct {
	cfg *config.ModelConfig

	ttsFile         string
	ttsStreamChunks [][]byte
	ttsStreamRate   int
	ttsStreamErr    error

	transcribeDeltas []string
	transcribeFinal  *schema.TranscriptionResult
}

func (m *fakeModel) VAD(context.Context, *schema.VADRequest) (*schema.VADResponse, error) {
	return nil, nil
}

func (m *fakeModel) Transcribe(context.Context, string, string, bool, bool, string) (*schema.TranscriptionResult, error) {
	return m.transcribeFinal, nil
}

func (m *fakeModel) Predict(context.Context, schema.Messages, []string, []string, []string, func(string, backend.TokenUsage) bool, []types.ToolUnion, *types.ToolChoiceUnion, *int, *int, map[string]float64) (func() (backend.LLMResponse, error), error) {
	return nil, nil
}

func (m *fakeModel) TTS(context.Context, string, string, string) (string, *proto.Result, error) {
	return m.ttsFile, &proto.Result{Success: true}, nil
}

func (m *fakeModel) TTSStream(_ context.Context, _, _, _ string, onAudio func(pcm []byte, sampleRate int) error) error {
	if m.ttsStreamErr != nil {
		return m.ttsStreamErr
	}
	for _, c := range m.ttsStreamChunks {
		if err := onAudio(c, m.ttsStreamRate); err != nil {
			return err
		}
	}
	return nil
}

func (m *fakeModel) TranscribeStream(_ context.Context, _, _ string, _, _ bool, _ string, onDelta func(text string)) (*schema.TranscriptionResult, error) {
	for _, d := range m.transcribeDeltas {
		onDelta(d)
	}
	return m.transcribeFinal, nil
}

func (m *fakeModel) PredictConfig() *config.ModelConfig { return m.cfg }
