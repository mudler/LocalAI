package openai

import (
	"context"
	"strings"

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

// transcriptDeltaText concatenates the Delta of every recorded transcript
// delta event — i.e. the text streamed to the client as it is generated.
func (f *fakeTransport) transcriptDeltaText() string {
	var b strings.Builder
	for _, e := range f.events {
		if d, ok := e.(types.ResponseOutputAudioTranscriptDeltaEvent); ok {
			b.WriteString(d.Delta)
		}
	}
	return b.String()
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

	// Predict streaming: predictTokens are replayed through the token callback
	// (simulating streamed LLM output); predictResp/predictErr are returned by
	// the deferred predict function. predictChunkDeltas, when set, are delivered
	// per-token via TokenUsage.ChatDeltas to exercise the autoparser path.
	predictTokens      []string
	predictChunkDeltas [][]*proto.ChatDelta
	predictResp        backend.LLMResponse
	predictErr         error

	lastMessages schema.Messages
}

func (m *fakeModel) VAD(context.Context, *schema.VADRequest) (*schema.VADResponse, error) {
	return nil, nil
}

func (m *fakeModel) Transcribe(context.Context, string, string, bool, bool, string) (*schema.TranscriptionResult, error) {
	return m.transcribeFinal, nil
}

func (m *fakeModel) Predict(_ context.Context, msgs schema.Messages, _, _, _ []string, cb func(string, backend.TokenUsage) bool, _ []types.ToolUnion, _ *types.ToolChoiceUnion, _, _ *int, _ map[string]float64) (func() (backend.LLMResponse, error), error) {
	m.lastMessages = msgs
	if m.predictErr != nil {
		return nil, m.predictErr
	}
	return func() (backend.LLMResponse, error) {
		for i, tok := range m.predictTokens {
			if cb == nil {
				continue
			}
			usage := backend.TokenUsage{}
			if i < len(m.predictChunkDeltas) {
				usage.ChatDeltas = m.predictChunkDeltas[i]
			}
			cb(tok, usage)
		}
		return m.predictResp, nil
	}, nil
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
