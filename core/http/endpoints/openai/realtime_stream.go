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
// for the new content (reasoning stripped) and returning that content delta so
// the caller can also feed it to the clause chunker. For plain-content models
// the unit is the raw text token; for autoparser tool turns the backend clears
// the text and delivers content via ChatDeltas, so the caller passes that
// content here. Returns "" when the token produced no new spoken content.
func (s *transcriptStreamer) onToken(token string) string {
	_, content := s.extractor.ProcessToken(token)
	if content == "" {
		return ""
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
	return content
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
func streamLLMResponse(ctx context.Context, session *Session, conv *Conversation, t Transport, r *liveResponse, history schema.Messages, images []string, llmCfg *config.ModelConfig, tools []types.ToolUnion, toolChoice *types.ToolChoiceUnion, toolTurn int) bool {
	responseID := r.id
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

	// cancel rolls back the partial item and records the cancelled outcome; the
	// single terminal is emitted by triggerResponse.
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
		r.outcome = outcomeCancelled
	}

	var template string
	if llmCfg.TemplateConfig.UseTokenizerTemplate {
		template = llmCfg.GetModelTemplate()
	} else {
		template = llmCfg.TemplateConfig.Chat
	}
	thinkingStartToken := reasoning.DetectThinkingStartToken(template, &llmCfg.ReasoningConfig)

	// The autoparser (tokenizer-template path) already delivers reasoning-free
	// content. Prefilling the thinking start token here would re-tag that clean
	// content as an unclosed reasoning block, leaving CleanedContent() empty —
	// no spoken reply, no TTS. Disable the prefill; closed tag pairs are still
	// stripped (PEG-fallback case, #9985).
	reasoningCfg := llmCfg.ReasoningConfig
	if llmCfg.TemplateConfig.UseTokenizerTemplate {
		disablePrefill := true
		reasoningCfg.DisableReasoningTagPrefill = &disablePrefill
	}

	streamer := newTranscriptStreamer(ctx, t, responseID, itemID, thinkingStartToken, reasoningCfg)
	streamer.announce = announce

	// Clause chunking (opt-in): synthesize each clause as soon as it completes
	// instead of buffering the whole reply. Synthesis runs on a worker goroutine
	// (ttsPipeline) rather than inline in the token callback: emitSpeech blocks
	// until the whole clause is synthesized (and, for WebRTC, played back at
	// real time), and the callback runs on the goroutine that drains the LLM
	// gRPC stream — so speaking inline stalls generation and freezes the
	// assistant transcript at every clause boundary. The worker lets generation
	// and the transcript stream keep flowing while audio is produced behind them.
	var chunker *clauseChunker
	var ttsPipe *ttsPipeline
	if session.ModelConfig != nil && session.ModelConfig.Pipeline.ChunkClauses() {
		chunker = newClauseChunker(defaultClauseMinRunes, defaultClauseMaxRunes)
		ttsPipe = newTTSPipeline(func(clause string) ([]byte, error) {
			return emitSpeech(ctx, t, session, responseID, itemID, clause)
		})
	}
	var streamedAudio []byte
	var ttsErr error

	// Backstop: always join the TTS worker, even on an unexpected early return.
	// wait() is idempotent, so the explicit drain below (which captures the
	// streamed audio and first error) stays authoritative; this only guarantees
	// the goroutine can never leak if a new return path is added.
	if ttsPipe != nil {
		defer func() { _, _ = ttsPipe.wait() }()
	}

	// fail reports a mid-stream failure. A cancelled context means the client
	// interrupted (barge-in), so roll the turn back instead of erroring.
	fail := func(code, msg string, err error) bool {
		if ctx.Err() != nil {
			cancel()
		} else {
			sendError(t, code, fmt.Sprintf("%s: %v", msg, err), "", itemID)
			r.outcome = outcomeFailed
		}
		return true
	}

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
		delta := streamer.onToken(text)
		if chunker != nil && delta != "" {
			for _, clause := range chunker.push(delta) {
				// Hand the clause to the worker and keep going — never block the
				// recv loop on synthesis. A false return means a prior clause
				// already failed; stop the prediction (the error is collected
				// from the pipeline after predFunc returns).
				if !ttsPipe.enqueue(clause) {
					return false
				}
			}
		}
		return true
	}

	predFunc, err := session.ModelInterface.Predict(ctx, history, images, nil, nil, cb, tools, toolChoice, nil, nil, nil)
	if err != nil {
		// The deferred wait() joins the (idle) worker.
		sendError(t, "inference_failed", fmt.Sprintf("backend error: %v", err), "", itemID)
		return true
	}
	pred, err := predFunc()

	// Drain the TTS worker. On a clean finish, enqueue the trailing clause(s) the
	// chunker was still holding; on an error or barge-in, stop synthesizing.
	// wait() runs on every path so the worker goroutine never leaks, and it
	// returns the audio streamed so far plus the first synthesis failure.
	if ttsPipe != nil {
		if err == nil && ctx.Err() == nil {
			for _, clause := range chunker.flush() {
				if !ttsPipe.enqueue(clause) {
					break
				}
			}
		}
		streamedAudio, ttsErr = ttsPipe.wait()
	}

	// A clause synthesis failed mid-stream (the callback stopped the prediction);
	// report it as a TTS error rather than a prediction error.
	if ttsErr != nil {
		return fail("tts_error", "TTS generation failed", ttsErr)
	}
	if err != nil {
		return fail("prediction_failed", "backend error", err)
	}
	if ctx.Err() != nil {
		cancel()
		return true
	}
	r.addUsage(pred.Usage)

	content := streamer.content()
	toolCalls := functions.ToolCallsFromChatDeltas(pred.ChatDeltas)

	// Finalize the spoken content item only when the turn produced content. A
	// tool-only turn skips this entirely (no empty assistant item).
	if content != "" {
		if !announced {
			announce()
		}

		// With clause chunking the clauses were synthesized on the worker as the
		// reply streamed (including the trailing flush drained above), so the
		// audio is already accumulated. Otherwise buffer the whole message and
		// synthesize it once now — emitSpeech streams the audio chunks when the
		// TTS backend supports TTSStream, otherwise it sends a single unary delta.
		var audio []byte
		if chunker != nil {
			audio = streamedAudio
		} else {
			audio, ttsErr = emitSpeech(ctx, t, session, responseID, itemID, content)
			if ttsErr != nil {
				return fail("tts_error", "TTS generation failed", ttsErr)
			}
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
		r.addItem(item)
	}

	// Emit any tool calls and (for server-side assistant tools) the follow-up
	// turn — shared with the buffered path. The single terminal is emitted by
	// triggerResponse.
	emitToolCallItems(ctx, session, conv, t, r, toolCalls, content != "", toolTurn)
	return true
}
