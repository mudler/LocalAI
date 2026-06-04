package openai

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
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
	// Spoken output must never contain reasoning, even when disable_thinking set
	// DisableReasoning (which would otherwise turn the extractor's stripping off).
	reasoningCfg = spokenReasoningConfig(reasoningCfg)
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

// streamLLMResponse drives a streamed, plain-content (no tools) realtime reply.
// It announces the assistant item before tokens arrive, feeds the LLM token
// callback through a speechStreamer (transcript deltas + sentence-piped TTS),
// then emits the terminal events. It returns true when it has fully handled the
// response so the caller can return; callers must only invoke it for turns with
// no tools and an audio modality (see triggerResponseAtTurn).
func streamLLMResponse(ctx context.Context, session *Session, conv *Conversation, t Transport, responseID string, history schema.Messages, images []string, llmCfg *config.ModelConfig) bool {
	// Announce the assistant item up front so streamed deltas target a known item.
	item := types.MessageItemUnion{
		Assistant: &types.MessageItemAssistant{
			ID:      generateItemID(),
			Status:  types.ItemStatusInProgress,
			Content: []types.MessageContentOutput{{Type: types.MessageContentTypeOutputAudio}},
		},
	}
	conv.Lock.Lock()
	conv.Items = append(conv.Items, &item)
	conv.Lock.Unlock()

	sendEvent(t, types.ResponseOutputItemAddedEvent{
		ServerEventBase: types.ServerEventBase{},
		ResponseID:      responseID,
		OutputIndex:     0,
		Item:            item,
	})
	sendEvent(t, types.ResponseContentPartAddedEvent{
		ServerEventBase: types.ServerEventBase{},
		ResponseID:      responseID,
		ItemID:          item.Assistant.ID,
		OutputIndex:     0,
		ContentIndex:    0,
		Part:            item.Assistant.Content[0],
	})

	cancel := func() {
		conv.Lock.Lock()
		for i := len(conv.Items) - 1; i >= 0; i-- {
			if conv.Items[i].Assistant != nil && conv.Items[i].Assistant.ID == item.Assistant.ID {
				conv.Items = append(conv.Items[:i], conv.Items[i+1:]...)
				break
			}
		}
		conv.Lock.Unlock()
		sendEvent(t, types.ResponseDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			Response:        types.Response{ID: responseID, Object: "realtime.response", Status: types.ResponseStatusCancelled},
		})
	}

	var template string
	if llmCfg.TemplateConfig.UseTokenizerTemplate {
		template = llmCfg.GetModelTemplate()
	} else {
		template = llmCfg.TemplateConfig.Chat
	}
	thinkingStartToken := reasoning.DetectThinkingStartToken(template, &llmCfg.ReasoningConfig)

	streamer := newSpeechStreamer(ctx, t, session, responseID, item.Assistant.ID, thinkingStartToken, llmCfg.ReasoningConfig)
	cb := func(token string, _ backend.TokenUsage) bool {
		if ctx.Err() != nil {
			return false
		}
		streamer.onToken(token)
		return true
	}

	predFunc, err := session.ModelInterface.Predict(ctx, history, images, nil, nil, cb, nil, nil, nil, nil, nil)
	if err != nil {
		sendError(t, "inference_failed", fmt.Sprintf("backend error: %v", err), "", item.Assistant.ID)
		return true
	}
	if _, err := predFunc(); err != nil {
		if ctx.Err() != nil {
			cancel()
			return true
		}
		sendError(t, "prediction_failed", fmt.Sprintf("backend error: %v", err), "", item.Assistant.ID)
		return true
	}
	if ctx.Err() != nil {
		cancel()
		return true
	}

	content, audio, err := streamer.finish()
	if err != nil {
		sendError(t, "tts_error", fmt.Sprintf("TTS generation failed: %v", err), "", item.Assistant.ID)
		return true
	}

	_, isWebRTC := t.(*WebRTCTransport)

	sendEvent(t, types.ResponseOutputAudioTranscriptDoneEvent{
		ServerEventBase: types.ServerEventBase{},
		ResponseID:      responseID,
		ItemID:          item.Assistant.ID,
		OutputIndex:     0,
		ContentIndex:    0,
		Transcript:      content,
	})
	if !isWebRTC {
		sendEvent(t, types.ResponseOutputAudioDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          item.Assistant.ID,
			OutputIndex:     0,
			ContentIndex:    0,
		})
	}

	conv.Lock.Lock()
	item.Assistant.Status = types.ItemStatusCompleted
	item.Assistant.Content[0].Transcript = content
	if !isWebRTC {
		item.Assistant.Content[0].Audio = base64.StdEncoding.EncodeToString(audio)
	}
	conv.Lock.Unlock()

	sendEvent(t, types.ResponseContentPartDoneEvent{
		ServerEventBase: types.ServerEventBase{},
		ResponseID:      responseID,
		ItemID:          item.Assistant.ID,
		OutputIndex:     0,
		ContentIndex:    0,
		Part:            item.Assistant.Content[0],
	})
	sendEvent(t, types.ResponseOutputItemDoneEvent{
		ServerEventBase: types.ServerEventBase{},
		ResponseID:      responseID,
		OutputIndex:     0,
		Item:            item,
	})
	sendEvent(t, types.ResponseDoneEvent{
		ServerEventBase: types.ServerEventBase{},
		Response:        types.Response{ID: responseID, Object: "realtime.response", Status: types.ResponseStatusCompleted},
	})
	return true
}
