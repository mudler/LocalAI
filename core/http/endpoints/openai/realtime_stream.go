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

// transcriptStreamer turns streamed LLM tokens into the assistant's spoken
// transcript: it strips reasoning incrementally and sends one
// response.output_audio_transcript.delta per content fragment. It does NOT
// synthesize audio — the caller buffers the full message and synthesizes it
// once (streaming the audio chunks when the TTS backend supports TTSStream),
// which works uniformly for streaming and non-streaming TTS and for languages
// without sentence or word boundaries.
type transcriptStreamer struct {
	ctx        context.Context
	t          Transport
	responseID string
	itemID     string
	extractor  *reasoning.ReasoningExtractor
}

func newTranscriptStreamer(ctx context.Context, t Transport, responseID, itemID, thinkingStartToken string, reasoningCfg reasoning.Config) *transcriptStreamer {
	return &transcriptStreamer{
		ctx:        ctx,
		t:          t,
		responseID: responseID,
		itemID:     itemID,
		extractor:  reasoning.NewReasoningExtractor(thinkingStartToken, spokenReasoningConfig(reasoningCfg)),
	}
}

// onToken handles one streamed unit of model output, sending a transcript delta
// for the new content (reasoning stripped). For plain-content models the unit is
// the raw text token; for autoparser tool turns the backend clears the text and
// delivers content via ChatDeltas, so the caller passes that content here.
func (s *transcriptStreamer) onToken(token string) {
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
}

// content returns the full transcript so far with reasoning stripped.
func (s *transcriptStreamer) content() string {
	return s.extractor.CleanedContent()
}

// streamLLMResponse drives a streamed, plain-content (no tools) realtime reply.
// It announces the assistant item before tokens arrive, streams transcript
// deltas as the LLM generates, then synthesizes the whole buffered message once
// (streaming the audio chunks when the TTS backend supports it, otherwise a
// single unary delta) and emits the terminal events. It returns true when it has
// fully handled the response so the caller can return; callers must only invoke
// it for turns with no tools and an audio modality (see triggerResponseAtTurn).
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

	streamer := newTranscriptStreamer(ctx, t, responseID, item.Assistant.ID, thinkingStartToken, llmCfg.ReasoningConfig)
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

	// Buffer the whole message, then synthesize it once. emitSpeech streams the
	// audio chunks when the TTS backend supports TTSStream, otherwise it sends a
	// single unary delta — no per-sentence segmentation either way.
	content := streamer.content()
	audio, err := emitSpeech(ctx, t, session, responseID, item.Assistant.ID, content)
	if err != nil {
		if ctx.Err() != nil {
			cancel()
			return true
		}
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
