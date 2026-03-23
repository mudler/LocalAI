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
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
)

type LLMResponse struct {
	Response    string // should this be []byte?
	Usage       TokenUsage
	AudioOutput string
	Logprobs    *schema.Logprobs // Logprobs from the backend response
	ChatDeltas  []*proto.ChatDelta // Pre-parsed tool calls/content from C++ autoparser
}

type TokenUsage struct {
	Prompt                 int
	Completion             int
	TimingPromptProcessing float64
	TimingTokenGeneration  float64
}

// ModelInferenceFunc is a test-friendly indirection to call model inference logic.
// Tests can override this variable to provide a stub implementation.
var ModelInferenceFunc = ModelInference

func ModelInference(ctx context.Context, s string, messages schema.Messages, images, videos, audios []string, loader *model.ModelLoader, c *config.ModelConfig, cl *config.ModelConfigLoader, o *config.ApplicationConfig, tokenCallback func(string, TokenUsage) bool, tools string, toolChoice string, logprobs *int, topLogprobs *int, logitBias map[string]float64, metadata map[string]string) (func() (LLMResponse, error), error) {
	modelFile := c.Model

	// Check if the modelFile exists, if it doesn't try to load it from the gallery
	if o.AutoloadGalleries { // experimental
		modelNames, err := services.ListModels(cl, loader, nil, services.SKIP_ALWAYS)
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
			err := gallery.InstallModelFromGallery(ctx, o.Galleries, o.BackendGalleries, o.SystemState, loader, modelName, gallery.GalleryModel{}, utils.DisplayDownloadFunction, o.EnforcePredownloadScans, o.AutoloadBackendGalleries)
			if err != nil {
				xlog.Error("failed to install model from gallery", "error", err, "model", modelFile)
				//return nil, err
			}
		}
	}

	opts := ModelOptions(*c, o)
	inferenceModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(o, c.Name, c.Backend, err, map[string]any{"model_file": modelFile})
		return nil, err
	}

	// Detect thinking support after model load (only if not already detected)
	// This needs to happen after LoadModel succeeds so the backend can render templates
	if (c.ReasoningConfig.DisableReasoning == nil && c.ReasoningConfig.DisableReasoningTagPrefill == nil) && c.TemplateConfig.UseTokenizerTemplate {
		modelOpts := grpcModelOpts(*c, o.SystemState.Model.ModelsPath)
		config.DetectThinkingSupportFromBackend(ctx, c, inferenceModel, modelOpts)
		// Update the config in the loader so it persists for future requests
		cl.UpdateModelConfig(c.Name, func(cfg *config.ModelConfig) {
			cfg.ReasoningConfig.DisableReasoning = c.ReasoningConfig.DisableReasoning
			cfg.ReasoningConfig.DisableReasoningTagPrefill = c.ReasoningConfig.DisableReasoningTagPrefill
		})
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
		opts.Prompt = s
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
		trace.InitBackendTracingIfEnabled(o.TracingMaxItems)

		traceData := map[string]any{
			"chat_template":    c.TemplateConfig.Chat,
			"function_template": c.TemplateConfig.Functions,
			"streaming":        tokenCallback != nil,
			"images_count":     len(images),
			"videos_count":     len(videos),
			"audios_count":     len(audios),
		}

		if len(messages) > 0 {
			if msgJSON, err := json.Marshal(messages); err == nil {
				traceData["messages"] = string(msgJSON)
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

			traceData["response"] = resp.Response
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
					chatDeltasInfo["content"] = strings.Join(contentParts, "")
				}
				if len(reasoningParts) > 0 {
					chatDeltasInfo["reasoning_content"] = strings.Join(reasoningParts, "")
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
