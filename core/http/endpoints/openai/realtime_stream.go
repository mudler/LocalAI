package openai

import (
	"context"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/pkg/reasoning"
)

// speechStreamer consumes streamed LLM tokens and drives the realtime output:
// it strips reasoning incrementally, emits a transcript text delta for each
// content fragment, and — when the pipeline streams TTS — sentence-pipes the
// content so each completed sentence is synthesized as soon as it's ready,
// overlapping generation, synthesis and playback.
//
// It is used only for plain-content turns (no tools): tool-call output can't be
// safely spoken mid-stream, so those turns keep the buffered path.
type speechStreamer struct {
	ctx        context.Context
	t          Transport
	session    *Session
	responseID string
	itemID     string

	extractor *reasoning.ReasoningExtractor
	seg       streamSegmenter
	audio     []byte
	streamTTS bool
	err       error
}

func newSpeechStreamer(ctx context.Context, t Transport, session *Session, responseID, itemID, thinkingStartToken string, reasoningCfg reasoning.Config) *speechStreamer {
	return &speechStreamer{
		ctx:        ctx,
		t:          t,
		session:    session,
		responseID: responseID,
		itemID:     itemID,
		extractor:  reasoning.NewReasoningExtractor(thinkingStartToken, reasoningCfg),
		streamTTS:  session.ModelConfig != nil && session.ModelConfig.Pipeline.StreamTTS(),
	}
}

// onToken handles one streamed LLM token. It is shaped to be used directly as
// the backend token callback's text sink.
func (s *speechStreamer) onToken(token string) {
	_, content := s.extractor.ProcessToken(token)
	if content == "" {
		return
	}
	_ = s.t.SendEvent(types.ResponseOutputAudioTranscriptDeltaEvent{
		ServerEventBase: types.ServerEventBase{},
		ResponseID:      s.responseID,
		ItemID:          s.itemID,
		OutputIndex:     0,
		ContentIndex:    0,
		Delta:           content,
	})
	if s.streamTTS {
		for _, segment := range s.seg.Push(content) {
			s.speak(segment)
		}
	}
}

func (s *speechStreamer) speak(text string) {
	pcm, err := emitSpeech(s.ctx, s.t, s.session, s.responseID, s.itemID, text)
	if err != nil {
		if s.err == nil {
			s.err = err
		}
		return
	}
	s.audio = append(s.audio, pcm...)
}

// finish flushes any buffered sentence to TTS and returns the full cleaned
// content, the accumulated PCM audio, and the first error encountered (if any).
func (s *speechStreamer) finish() (content string, audio []byte, err error) {
	if s.streamTTS {
		if rem := s.seg.Flush(); rem != "" {
			s.speak(rem)
		}
	}
	return s.extractor.CleanedContent(), s.audio, s.err
}
