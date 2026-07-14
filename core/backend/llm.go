package backend

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/core/trace"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/distributedhdr"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
)

type LLMResponse struct {
	Response    string // should this be []byte?
	Usage       TokenUsage
	AudioOutput string
	Logprobs    *schema.Logprobs   // Logprobs from the backend response
	ChatDeltas  []*proto.ChatDelta // Pre-parsed tool calls/content from C++ autoparser
}

type TokenUsage struct {
	Prompt                 int
	Completion             int
	TimingPromptProcessing float64
	TimingTokenGeneration  float64
	ChatDeltas             []*proto.ChatDelta // per-chunk deltas from C++ autoparser (only set during streaming)
}

func needsThinkingProbe(c *config.ModelConfig) bool {
	return c.TemplateConfig.UseTokenizerTemplate &&
		(c.ReasoningConfig.DisableReasoning == nil ||
			c.ReasoningConfig.DisableReasoningTagPrefill == nil)
}

// persistProbedReasoning writes the post-probe reasoning slots (and media
// marker) from probed back into the loader's persisted config for modelName,
// skipping any reasoning slot the probe was not actually allowed to fill.
// persistDisableReasoning/persistDisableTagPrefill must be snapshotted from
// probed's reasoning slots *before* the probe ran: a slot that already
// carried a value at that point was populated by request-time
// ApplyReasoningEffort, not by backend detection, and persisting it would
// masquerade as an operator's explicit reasoning.disable (see #10622).
func persistProbedReasoning(cl *config.ModelConfigLoader, modelName string, probed *config.ModelConfig, persistDisableReasoning, persistDisableTagPrefill bool) {
	cl.UpdateModelConfig(modelName, func(cfg *config.ModelConfig) {
		if persistDisableReasoning {
			cfg.ReasoningConfig.DisableReasoning = probed.ReasoningConfig.DisableReasoning
		}
		if persistDisableTagPrefill {
			cfg.ReasoningConfig.DisableReasoningTagPrefill = probed.ReasoningConfig.DisableReasoningTagPrefill
		}
		if probed.MediaMarker != "" {
			cfg.MediaMarker = probed.MediaMarker
		}
	})
}

// HasChatDeltaContent returns true if any chat delta carries content or reasoning text.
// Used to decide whether to prefer C++ autoparser deltas over Go-side tag extraction.
func (t TokenUsage) HasChatDeltaContent() bool {
	for _, d := range t.ChatDeltas {
		if d.Content != "" || d.ReasoningContent != "" {
			return true
		}
	}
	return false
}

// ChatDeltaReasoningAndContent extracts accumulated reasoning and content from chat deltas.
func (t TokenUsage) ChatDeltaReasoningAndContent() (reasoning, content string) {
	for _, d := range t.ChatDeltas {
		content += d.Content
		reasoning += d.ReasoningContent
	}
	return reasoning, content
}

// ModelInferenceFunc is a test-friendly indirection to call model inference logic.
// Tests can override this variable to provide a stub implementation.
var ModelInferenceFunc = ModelInference

func ModelInference(ctx context.Context, s string, messages schema.Messages, images, videos, audios []string, loader *model.ModelLoader, c *config.ModelConfig, cl *config.ModelConfigLoader, o *config.ApplicationConfig, tokenCallback func(string, TokenUsage) bool, tools string, toolChoice string, logprobs *int, topLogprobs *int, logitBias map[string]float64, metadata map[string]string) (func() (LLMResponse, error), error) {
	modelFile := c.Model

	// Check if the modelFile exists, if it doesn't try to load it from the gallery
	if o.AutoloadGalleries { // experimental
		modelNames, err := galleryop.ListModels(cl, loader, nil, galleryop.SKIP_ALWAYS)
		if err != nil {
			return nil, err
		}
		modelName := c.Name
		if modelName == "" {
			modelName = c.Model
		}
		if !slices.Contains(modelNames, modelName) {
			utils.ResetDownloadTimers()
			// if we failed to load the model, we try to download it
			err := gallery.InstallModelFromGallery(ctx, o.Galleries, o.BackendGalleries, o.SystemState, loader, modelName, gallery.GalleryModel{}, utils.DisplayDownloadFunction, o.EnforcePredownloadScans, o.AutoloadBackendGalleries, o.RequireBackendIntegrity, gallery.WithArtifactMaterializer(o.ModelArtifactMaterializer))
			if err != nil {
				xlog.Error("failed to install model from gallery", "error", err, "model", modelFile)
				//return nil, err
			}
		}
	}

	// Make the rendered prompt's prefix chain available to the distributed router
	// for prefix-cache-aware node selection. No-op in single-process mode. The
	// model id MUST match the id ModelOptions feeds to model.WithModelID, so both
	// use the shared config.ModelConfig.ModelID() helper (Name with a fallback to
	// Model) or the chain salt and the tracking key would diverge.
	//
	// s is empty for UseTokenizerTemplate models (the backend tokenizes the
	// structured messages itself), so fall back to a prefix-stable serialization
	// of the messages - otherwise prefix routing would silently degrade to
	// round-robin for the bulk of modern chat models.
	chainSource := s
	if chainSource == "" {
		chainSource = messagesPrefixSource(messages)
	}
	ctx = distributedhdr.MaybeWithPrefixChain(ctx, c.ModelID(), chainSource)

	opts := ModelOptions(*c, o, model.WithContext(ctx))
	inferenceModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(o, c.Name, c.Backend, err, map[string]any{"model_file": modelFile})
		return nil, err
	}

	// Probe the backend for model-scoped metadata after LoadModel succeeds.
	// Two signals are captured: thinking-mode detection (only meaningful when the
	// tokenizer template path is active) and the multimodal media marker (needed
	// by custom chat templates so markers line up with what mtmd expects).
	// We probe whenever any of those slots is still empty.
	shouldProbeThinking := needsThinkingProbe(c)
	needsMarkerProbe := c.MediaMarker == ""
	if shouldProbeThinking || needsMarkerProbe {
		modelOpts := grpcModelOpts(*c, o.SystemState.Model.ModelsPath)
		// DetectThinkingSupportFromBackend only fills reasoning slots that are
		// still nil, so a slot that already carries a value here was populated by
		// request-time ApplyReasoningEffort (e.g. a `reasoning_effort: none`
		// default), not by backend detection. Persisting such a request-scoped
		// value would masquerade as an operator's explicit reasoning.disable and
		// permanently defeat future per-request reasoning_effort overrides
		// (see #10622). Only persist the slots the probe is actually allowed to
		// fill.
		persistDisableReasoning := c.ReasoningConfig.DisableReasoning == nil
		persistDisableTagPrefill := c.ReasoningConfig.DisableReasoningTagPrefill == nil
		config.DetectThinkingSupportFromBackend(ctx, c, inferenceModel, modelOpts)
		// Update the config in the loader so it persists for future requests
		persistProbedReasoning(cl, c.Name, c, persistDisableReasoning, persistDisableTagPrefill)
	}

	var protoMessages []*proto.Message
	// if we are using the tokenizer template, we need to convert the messages to proto messages
	// unless the prompt has already been tokenized (non-chat endpoints + functions)
	if c.TemplateConfig.UseTokenizerTemplate && len(messages) > 0 {
		protoMessages = messages.ToProto()
	}

	// in GRPC, the backend is supposed to answer to 1 single token if stream is not supported
	var capturedPredictOpts *proto.PredictOptions
	fn := func() (LLMResponse, error) {
		opts := gRPCPredictOpts(*c, loader.ModelPath)
		// Merge request-level metadata (overrides config defaults)
		for k, v := range metadata {
			opts.Metadata[k] = v
		}
		// The prompt was rendered with the sentinel "<__media__>" marker because
		// middleware templating runs before the backend is loaded and probed.
		// Once we know the backend's actual media marker, substitute so marker
		// count matches the bitmap count passed through opts.Images/Videos/Audios.
		// No-op when MediaMarker is unset, matches the sentinel, or the prompt has
		// no media placeholders.
		prompt := s
		if c.MediaMarker != "" && c.MediaMarker != templates.DefaultMultiMediaMarker {
			prompt = strings.ReplaceAll(prompt, templates.DefaultMultiMediaMarker, c.MediaMarker)
		}
		opts.Prompt = prompt
		opts.Messages = protoMessages
		opts.UseTokenizerTemplate = c.TemplateConfig.UseTokenizerTemplate
		opts.Images = images
		opts.Videos = videos
		opts.Audios = audios
		opts.Tools = tools
		opts.ToolChoice = toolChoice
		if logprobs != nil {
			opts.Logprobs = int32(*logprobs)
		}
		if topLogprobs != nil {
			opts.TopLogprobs = int32(*topLogprobs)
		}
		if len(logitBias) > 0 {
			// Serialize logit_bias map to JSON string for proto
			logitBiasJSON, err := json.Marshal(logitBias)
			if err == nil {
				opts.LogitBias = string(logitBiasJSON)
			}
		}
		capturedPredictOpts = opts

		tokenUsage := TokenUsage{}

		// check the per-model feature flag for usage, since tokenCallback may have a cost.
		// Defaults to off as for now it is still experimental
		if c.FeatureFlag.Enabled("usage") {
			userTokenCallback := tokenCallback
			if userTokenCallback == nil {
				userTokenCallback = func(token string, usage TokenUsage) bool {
					return true
				}
			}

			promptInfo, pErr := inferenceModel.TokenizeString(ctx, opts)
			if pErr == nil && promptInfo.Length > 0 {
				tokenUsage.Prompt = int(promptInfo.Length)
			}

			tokenCallback = func(token string, usage TokenUsage) bool {
				tokenUsage.Completion++
				return userTokenCallback(token, tokenUsage)
			}
		}

		if tokenCallback != nil {

			if c.TemplateConfig.ReplyPrefix != "" {
				tokenCallback(c.TemplateConfig.ReplyPrefix, tokenUsage)
			}

			ss := ""
			var logprobs *schema.Logprobs
			var allChatDeltas []*proto.ChatDelta

			var partialRune []byte
			err := inferenceModel.PredictStream(ctx, opts, func(reply *proto.Reply) {
				msg := reply.Message
				partialRune = append(partialRune, msg...)

				tokenUsage.Prompt = int(reply.PromptTokens)
				tokenUsage.Completion = int(reply.Tokens)
				tokenUsage.TimingTokenGeneration = reply.TimingTokenGeneration
				tokenUsage.TimingPromptProcessing = reply.TimingPromptProcessing

				// Collect chat deltas from C++ autoparser
				if len(reply.ChatDeltas) > 0 {
					allChatDeltas = append(allChatDeltas, reply.ChatDeltas...)
				}

				// Attach per-chunk chat deltas to tokenUsage so the callback can use them
				tokenUsage.ChatDeltas = reply.ChatDeltas

				// Parse logprobs from reply if present (collect from last chunk that has them)
				if len(reply.Logprobs) > 0 {
					var parsedLogprobs schema.Logprobs
					if err := json.Unmarshal(reply.Logprobs, &parsedLogprobs); err == nil {
						logprobs = &parsedLogprobs
					}
				}

				// Process complete runes and accumulate them
				var completeRunes []byte
				for len(partialRune) > 0 {
					r, size := utf8.DecodeRune(partialRune)
					if r == utf8.RuneError {
						// incomplete rune, wait for more bytes
						break
					}
					completeRunes = append(completeRunes, partialRune[:size]...)
					partialRune = partialRune[size:]
				}

				// If we have complete runes, send them as a single token
				if len(completeRunes) > 0 {
					tokenCallback(string(completeRunes), tokenUsage)
					ss += string(completeRunes)
				}

				if len(msg) == 0 {
					tokenCallback("", tokenUsage)
				}

				// Clear per-chunk deltas so they don't leak to the next chunk
				tokenUsage.ChatDeltas = nil
			})
			if len(allChatDeltas) > 0 {
				xlog.Debug("[ChatDeltas] streaming completed, accumulated deltas from C++ autoparser", "total_deltas", len(allChatDeltas))
			}
			return LLMResponse{
				Response:   ss,
				Usage:      tokenUsage,
				Logprobs:   logprobs,
				ChatDeltas: allChatDeltas,
			}, err
		} else {
			// TODO: Is the chicken bit the only way to get here? is that acceptable?
			reply, err := inferenceModel.Predict(ctx, opts)
			if err != nil {
				return LLMResponse{}, err
			}
			if tokenUsage.Prompt == 0 {
				tokenUsage.Prompt = int(reply.PromptTokens)
			}
			if tokenUsage.Completion == 0 {
				tokenUsage.Completion = int(reply.Tokens)
			}

			tokenUsage.TimingTokenGeneration = reply.TimingTokenGeneration
			tokenUsage.TimingPromptProcessing = reply.TimingPromptProcessing

			response := string(reply.Message)
			if c.TemplateConfig.ReplyPrefix != "" {
				response = c.TemplateConfig.ReplyPrefix + response
			}

			// Parse logprobs from reply if present
			var logprobs *schema.Logprobs
			if len(reply.Logprobs) > 0 {
				var parsedLogprobs schema.Logprobs
				if err := json.Unmarshal(reply.Logprobs, &parsedLogprobs); err == nil {
					logprobs = &parsedLogprobs
				}
			}

			if len(reply.ChatDeltas) > 0 {
				xlog.Debug("[ChatDeltas] non-streaming Predict received deltas from C++ autoparser", "total_deltas", len(reply.ChatDeltas))
			}
			return LLMResponse{
				Response:   response,
				Usage:      tokenUsage,
				Logprobs:   logprobs,
				ChatDeltas: reply.ChatDeltas,
			}, err
		}
	}

	if o.EnableTracing {
		trace.InitBackendTracingIfEnabled(o.TracingMaxItems, o.TracingMaxBodyBytes)

		traceData := map[string]any{
			"chat_template":     c.TemplateConfig.Chat,
			"function_template": c.TemplateConfig.Functions,
			"streaming":         tokenCallback != nil,
			"images_count":      len(images),
			"videos_count":      len(videos),
			"audios_count":      len(audios),
		}

		// Cap the captured fields up front: agent-pool LLM calls embed the
		// full augmented chat history in messages and the full reply in
		// response, so without a per-field cap a single trace can dwarf the
		// rest of the buffer. The cap matches the API-trace body cap.
		if len(messages) > 0 {
			if msgJSON, err := json.Marshal(messages); err == nil {
				traceData["messages"] = trace.TruncateToBytes(string(msgJSON), o.TracingMaxBodyBytes)
			}
		}
		if reasoningJSON, err := json.Marshal(c.ReasoningConfig); err == nil {
			traceData["reasoning_config"] = string(reasoningJSON)
		}
		traceData["functions_config"] = map[string]any{
			"grammar_disabled":  c.FunctionsConfig.GrammarConfig.NoGrammar,
			"parallel_calls":    c.FunctionsConfig.GrammarConfig.ParallelCalls,
			"mixed_mode":        c.FunctionsConfig.GrammarConfig.MixedMode,
			"xml_format_preset": c.FunctionsConfig.XMLFormatPreset,
		}

		startTime := time.Now()
		originalFn := fn
		fn = func() (LLMResponse, error) {
			resp, err := originalFn()
			duration := time.Since(startTime)

			traceData["response"] = trace.TruncateToBytes(resp.Response, o.TracingMaxBodyBytes)
			traceData["token_usage"] = map[string]any{
				"prompt":     resp.Usage.Prompt,
				"completion": resp.Usage.Completion,
			}

			if len(resp.ChatDeltas) > 0 {
				chatDeltasInfo := map[string]any{
					"total_deltas": len(resp.ChatDeltas),
				}
				var contentParts, reasoningParts []string
				toolCallCount := 0
				for _, d := range resp.ChatDeltas {
					if d.Content != "" {
						contentParts = append(contentParts, d.Content)
					}
					if d.ReasoningContent != "" {
						reasoningParts = append(reasoningParts, d.ReasoningContent)
					}
					toolCallCount += len(d.ToolCalls)
				}
				if len(contentParts) > 0 {
					chatDeltasInfo["content"] = trace.TruncateToBytes(strings.Join(contentParts, ""), o.TracingMaxBodyBytes)
				}
				if len(reasoningParts) > 0 {
					chatDeltasInfo["reasoning_content"] = trace.TruncateToBytes(strings.Join(reasoningParts, ""), o.TracingMaxBodyBytes)
				}
				if toolCallCount > 0 {
					chatDeltasInfo["tool_call_count"] = toolCallCount
				}
				traceData["chat_deltas"] = chatDeltasInfo
			}

			if capturedPredictOpts != nil {
				if optsJSON, err := json.Marshal(capturedPredictOpts); err == nil {
					var optsMap map[string]any
					if err := json.Unmarshal(optsJSON, &optsMap); err == nil {
						traceData["predict_options"] = optsMap
					}
				}
			}

			errStr := ""
			if err != nil {
				errStr = err.Error()
			}

			trace.RecordBackendTrace(trace.BackendTrace{
				Timestamp: startTime,
				Duration:  duration,
				Type:      trace.BackendTraceLLM,
				ModelName: c.Name,
				Backend:   c.Backend,
				Summary:   trace.GenerateLLMSummary(messages, s),
				Error:     errStr,
				Data:      traceData,
			})

			return resp, err
		}
	}

	return fn, nil
}

var cutstrings map[string]*regexp.Regexp = make(map[string]*regexp.Regexp)
var mu sync.Mutex = sync.Mutex{}

func Finetune(config config.ModelConfig, input, prediction string) string {
	if config.Echo {
		prediction = input + prediction
	}

	for _, c := range config.Cutstrings {
		mu.Lock()
		reg, ok := cutstrings[c]
		if !ok {
			r, err := regexp.Compile(c)
			if err != nil {
				xlog.Fatal("failed to compile regex", "error", err)
			}
			cutstrings[c] = r
			reg = cutstrings[c]
		}
		mu.Unlock()
		prediction = reg.ReplaceAllString(prediction, "")
	}

	// extract results from the response which can be for instance inside XML tags
	var predResult string
	for _, r := range config.ExtractRegex {
		mu.Lock()
		reg, ok := cutstrings[r]
		if !ok {
			regex, err := regexp.Compile(r)
			if err != nil {
				xlog.Fatal("failed to compile regex", "error", err)
			}
			cutstrings[r] = regex
			reg = regex
		}
		mu.Unlock()
		predResult += reg.FindString(prediction)
	}
	if predResult != "" {
		prediction = predResult
	}

	for _, c := range config.TrimSpace {
		prediction = strings.TrimSpace(strings.TrimPrefix(prediction, c))
	}

	for _, c := range config.TrimSuffix {
		prediction = strings.TrimSpace(strings.TrimSuffix(prediction, c))
	}
	return prediction
}
