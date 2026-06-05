package openai

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
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

	// announce, if set, is invoked once just before the first transcript delta.
	// It lets the caller create the assistant item lazily, so a content-less
	// tool-call turn never emits a spurious empty assistant item.
	announce  func()
	announced bool
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
	if !s.announced {
		s.announced = true
		if s.announce != nil {
			s.announce()
		}
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

// streamLLMResponse drives a streamed realtime reply. It streams the assistant
// transcript as the LLM generates, then synthesizes the whole buffered message
// once (streaming the audio chunks when the TTS backend supports it, otherwise a
// single unary delta). Tool calls parsed from the autoparser ChatDeltas are
// emitted after the spoken content. The assistant content item is created lazily
// on the first content delta, so a content-less tool-call turn emits only the
// tool calls. It returns true when it has fully handled the response so the
// caller can return; callers must only invoke it for an audio modality, and with
// tools only when the model uses its tokenizer template (see triggerResponseAtTurn).
func streamLLMResponse(ctx context.Context, session *Session, conv *Conversation, t Transport, responseID string, history schema.Messages, images []string, llmCfg *config.ModelConfig, tools []types.ToolUnion, toolChoice *types.ToolChoiceUnion, toolTurn int) bool {
	itemID := generateItemID()
	item := types.MessageItemUnion{
		Assistant: &types.MessageItemAssistant{
			ID:      itemID,
			Status:  types.ItemStatusInProgress,
			Content: []types.MessageContentOutput{{Type: types.MessageContentTypeOutputAudio}},
		},
	}

	// announce creates the assistant content item lazily, just before the first
	// transcript delta — a tool-only turn never produces content, so it stays out
	// of the conversation and the client sees only the tool calls.
	announced := false
	announce := func() {
		announced = true
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
			ItemID:          itemID,
			OutputIndex:     0,
			ContentIndex:    0,
			Part:            item.Assistant.Content[0],
		})
	}

	cancel := func() {
		if announced {
			conv.Lock.Lock()
			for i := len(conv.Items) - 1; i >= 0; i-- {
				if conv.Items[i].Assistant != nil && conv.Items[i].Assistant.ID == itemID {
					conv.Items = append(conv.Items[:i], conv.Items[i+1:]...)
					break
				}
			}
			conv.Lock.Unlock()
		}
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

	streamer := newTranscriptStreamer(ctx, t, responseID, itemID, thinkingStartToken, llmCfg.ReasoningConfig)
	streamer.announce = announce
	cb := func(token string, usage backend.TokenUsage) bool {
		if ctx.Err() != nil {
			return false
		}
		// Plain-content models stream text via the token; autoparser tool turns
		// clear the text and deliver content via ChatDeltas, so prefer the latter
		// when present. Either way only content reaches the transcript — tool-call
		// deltas are parsed from the final response below.
		text := token
		if len(usage.ChatDeltas) > 0 {
			text = functions.ContentFromChatDeltas(usage.ChatDeltas)
		}
		streamer.onToken(text)
		return true
	}

	predFunc, err := session.ModelInterface.Predict(ctx, history, images, nil, nil, cb, tools, toolChoice, nil, nil, nil)
	if err != nil {
		sendError(t, "inference_failed", fmt.Sprintf("backend error: %v", err), "", itemID)
		return true
	}
	pred, err := predFunc()
	if err != nil {
		if ctx.Err() != nil {
			cancel()
			return true
		}
		sendError(t, "prediction_failed", fmt.Sprintf("backend error: %v", err), "", itemID)
		return true
	}
	if ctx.Err() != nil {
		cancel()
		return true
	}

	content := streamer.content()
	toolCalls := functions.ToolCallsFromChatDeltas(pred.ChatDeltas)

	// Finalize the spoken content item only when the turn produced content. A
	// tool-only turn skips this entirely (no empty assistant item).
	if content != "" {
		if !announced {
			announce()
		}
		// Buffer the whole message, then synthesize it once. emitSpeech streams
		// the audio chunks when the TTS backend supports TTSStream, otherwise it
		// sends a single unary delta — no per-sentence segmentation either way.
		audio, err := emitSpeech(ctx, t, session, responseID, itemID, content)
		if err != nil {
			if ctx.Err() != nil {
				cancel()
				return true
			}
			sendError(t, "tts_error", fmt.Sprintf("TTS generation failed: %v", err), "", itemID)
			return true
		}

		_, isWebRTC := t.(*WebRTCTransport)

		sendEvent(t, types.ResponseOutputAudioTranscriptDoneEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          itemID,
			OutputIndex:     0,
			ContentIndex:    0,
			Transcript:      content,
		})
		if !isWebRTC {
			sendEvent(t, types.ResponseOutputAudioDoneEvent{
				ServerEventBase: types.ServerEventBase{},
				ResponseID:      responseID,
				ItemID:          itemID,
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
			ItemID:          itemID,
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
	}

	// Emit any tool calls, the terminal response.done, and (for server-side
	// assistant tools) the follow-up turn — shared with the buffered path.
	emitToolCallItems(ctx, session, conv, t, responseID, toolCalls, content != "", toolTurn)
	return true
}
