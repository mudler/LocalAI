package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// CompletionEndpoint is the OpenAI Completion API endpoint https://platform.openai.com/docs/api-reference/completions
// @Summary Generate completions for a given prompt and model.
// @Tags inference
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/completions [post]
func CompletionEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig, piiRedactor *pii.Redactor, piiEvents pii.EventStore) echo.HandlerFunc {
	process := func(id string, s string, req *schema.OpenAIRequest, config *config.ModelConfig, loader *model.ModelLoader, responses chan schema.OpenAIResponse, extraUsage bool) error {
		tokenCallback := func(s string, tokenUsage backend.TokenUsage) bool {
			created := int(time.Now().Unix())

			usage := schema.OpenAIUsage{
				PromptTokens:     tokenUsage.Prompt,
				CompletionTokens: tokenUsage.Completion,
				TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
			}
			if extraUsage {
				usage.TimingTokenGeneration = tokenUsage.TimingTokenGeneration
				usage.TimingPromptProcessing = tokenUsage.TimingPromptProcessing
			}
			// Usage rides on the struct for the consumer to track the
			// running cumulative; the consumer strips it before marshalling
			// so intermediate chunks stay OpenAI-spec compliant.
			usageForChunk := usage
			resp := schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{
					{
						Index:        0,
						Text:         s,
						FinishReason: nil,
					},
				},
				Object: "text_completion",
				Usage:  &usageForChunk,
			}
			xlog.Debug("Sending goroutine", "text", s)

			responses <- resp
			return true
		}
		_, _, _, err := ComputeChoices(req, s, config, cl, appConfig, loader, func(s string, c *[]schema.Choice) {}, tokenCallback)
		close(responses)
		return err
	}

	return func(c echo.Context) error {

		created := int(time.Now().Unix())

		// Handle Correlation
		id := c.Request().Header.Get("X-Correlation-ID")
		if id == "" {
			id = uuid.New().String()
		}
		extraUsage := c.Request().Header.Get("Extra-Usage") != ""

		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		config, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			return echo.ErrBadRequest
		}

		if config.ResponseFormatMap != nil {
			d := schema.ChatCompletionResponseFormat{}
			dat, _ := json.Marshal(config.ResponseFormatMap)
			_ = json.Unmarshal(dat, &d)
			if d.Type == "json_object" {
				input.Grammar = functions.JSONBNF
			}
		}

		config.Grammar = input.Grammar

		xlog.Debug("Parameter Config", "config", config)

		if input.Stream {
			xlog.Debug("Stream request received")
			c.Response().Header().Set("Content-Type", "text/event-stream")
			c.Response().Header().Set("Cache-Control", "no-cache")
			c.Response().Header().Set("Connection", "keep-alive")

			if len(config.PromptStrings) > 1 {
				return errors.New("cannot handle more than 1 `PromptStrings` when Streaming")
			}

			// Per-stream PII filter — same gating as chat. /v1/completions
			// has no chat-message structure, so request-side PII isn't
			// wired here, but the response-side filter still catches PII
			// trained into the model. Filter is nil when this model has
			// PII disabled.
			var streamPIIFilter *pii.StreamFilter
			if piiRedactor != nil && config.PIIIsEnabled() {
				correlationID := id
				userID := ""
				if u := auth.GetUser(c); u != nil {
					userID = u.ID
				}
				var overrides map[string]pii.Action
				if raw := config.PIIPatternOverrides(); len(raw) > 0 {
					overrides = make(map[string]pii.Action, len(raw))
					for ovid, action := range raw {
						switch pii.Action(action) {
						case pii.ActionMask, pii.ActionBlock, pii.ActionAllow:
							overrides[ovid] = pii.Action(action)
						}
					}
				}
				streamPIIFilter = pii.NewStreamFilter(piiRedactor, overrides, piiEvents, correlationID, userID)
			}

			predInput := config.PromptStrings[0]

			templatedInput, err := evaluator.EvaluateTemplateForPrompt(templates.CompletionPromptTemplate, *config, templates.PromptTemplateData{
				Input:           predInput,
				SystemPrompt:    config.SystemPrompt,
				ReasoningEffort: input.ReasoningEffort,
				Metadata:        input.Metadata,
			})
			if err == nil {
				predInput = templatedInput
				xlog.Debug("Template found, input modified", "input", predInput)
			}

			responses := make(chan schema.OpenAIResponse)

			ended := make(chan error)
			go func() {
				ended <- process(id, predInput, input, config, ml, responses, extraUsage)
			}()

			var latestUsage *schema.OpenAIUsage

		LOOP:
			for {
				select {
				case ev := <-responses:
					if len(ev.Choices) == 0 {
						xlog.Debug("No choices in the response, skipping")
						continue
					}
					// Capture running cumulative usage for the optional trailer
					// emitted after the final stop chunk when include_usage=true.
					// Done before the PII filter so a fully-buffered chunk
					// (which we drop from the wire) still contributes to the
					// running total.
					if ev.Usage != nil {
						latestUsage = ev.Usage
					}
					// OpenAI streaming spec: intermediate chunks must NOT
					// carry a `usage` field. Strip the tracking copy now.
					ev.Usage = nil
					// Run the per-chunk text through the streaming PII
					// filter. The filter holds back a tail to handle
					// pattern boundaries, so a Push may legitimately
					// return "" — drop the chunk's text rather than
					// emitting a 0-token delta. Choice.Text is the only
					// content surface in /v1/completions chunks.
					if streamPIIFilter != nil && ev.Choices[0].Text != "" {
						filtered := streamPIIFilter.Push(ev.Choices[0].Text)
						if filtered == "" {
							continue
						}
						ev.Choices[0].Text = filtered
					}
					respData, err := json.Marshal(ev)
					if err != nil {
						xlog.Debug("Failed to marshal response", "error", err)
						continue
					}

					xlog.Debug("Sending chunk", "chunk", string(respData))
					_, err = fmt.Fprintf(c.Response().Writer, "data: %s\n\n", string(respData))
					if err != nil {
						return err
					}
					c.Response().Flush()
				case err := <-ended:
					if err == nil {
						break LOOP
					}
					xlog.Error("Stream ended with error", "error", err)

					stopReason := FinishReasonStop
					errorResp := schema.OpenAIResponse{
						ID:      id,
						Created: created,
						Model:   input.Model,
						Choices: []schema.Choice{
							{
								Index:        0,
								FinishReason: &stopReason,
								Text:         "Internal error: " + err.Error(),
							},
						},
						Object: "text_completion",
					}
					errorData, marshalErr := json.Marshal(errorResp)
					if marshalErr != nil {
						xlog.Error("Failed to marshal error response", "error", marshalErr)
						// Send a simple error message as fallback
						fmt.Fprintf(c.Response().Writer, "data: {\"error\":\"Internal error\"}\n\n")
					} else {
						fmt.Fprintf(c.Response().Writer, "data: %s\n\n", string(errorData))
					}
					c.Response().Flush()
					return nil
				}
			}

			// Flush any residual the streaming PII filter held back as
			// part of its trailing pattern-window. Emit it as one final
			// text-bearing chunk before the synthetic stop chunk so the
			// completion body remains a contiguous text stream.
			if streamPIIFilter != nil {
				if residual := streamPIIFilter.Drain(); residual != "" {
					residualResp := schema.OpenAIResponse{
						ID:      id,
						Created: created,
						Model:   input.Model,
						Choices: []schema.Choice{{Index: 0, Text: residual}},
						Object:  "text_completion",
					}
					if data, err := json.Marshal(residualResp); err == nil {
						_, _ = fmt.Fprintf(c.Response().Writer, "data: %s\n\n", string(data))
					}
				}
			}

			stopReason := FinishReasonStop
			resp := &schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{
					{
						Index:        0,
						FinishReason: &stopReason,
					},
				},
				Object: "text_completion",
			}
			respData, _ := json.Marshal(resp)

			pt, ct := 0, 0
			if latestUsage != nil {
				pt = latestUsage.PromptTokens
				ct = latestUsage.CompletionTokens
			}
			middleware.StampUsage(c, input.Model, pt, ct)

			fmt.Fprintf(c.Response().Writer, "data: %s\n\n", respData)

			// Trailing usage chunk per OpenAI spec: emit only when the caller
			// opted in via stream_options.include_usage.
			if input.StreamOptions != nil && input.StreamOptions.IncludeUsage && latestUsage != nil {
				trailer := streamUsageTrailerJSON(id, input.Model, created, *latestUsage)
				_, _ = fmt.Fprintf(c.Response().Writer, "data: %s\n\n", trailer)
			}

			fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
			c.Response().Flush()
			return nil
		}

		var result []schema.Choice

		totalTokenUsage := backend.TokenUsage{}

		for k, i := range config.PromptStrings {
			templatedInput, err := evaluator.EvaluateTemplateForPrompt(templates.CompletionPromptTemplate, *config, templates.PromptTemplateData{
				SystemPrompt:    config.SystemPrompt,
				Input:           i,
				ReasoningEffort: input.ReasoningEffort,
				Metadata:        input.Metadata,
			})
			if err == nil {
				i = templatedInput
				xlog.Debug("Template found, input modified", "input", i)
			}

			r, tokenUsage, _, err := ComputeChoices(
				input, i, config, cl, appConfig, ml, func(s string, c *[]schema.Choice) {
					stopReason := FinishReasonStop
					*c = append(*c, schema.Choice{Text: s, FinishReason: &stopReason, Index: k})
				}, nil)
			if err != nil {
				return err
			}

			totalTokenUsage.TimingTokenGeneration += tokenUsage.TimingTokenGeneration
			totalTokenUsage.TimingPromptProcessing += tokenUsage.TimingPromptProcessing

			result = append(result, r...)
		}
		usage := schema.OpenAIUsage{
			PromptTokens:     totalTokenUsage.Prompt,
			CompletionTokens: totalTokenUsage.Completion,
			TotalTokens:      totalTokenUsage.Prompt + totalTokenUsage.Completion,
		}
		if extraUsage {
			usage.TimingTokenGeneration = totalTokenUsage.TimingTokenGeneration
			usage.TimingPromptProcessing = totalTokenUsage.TimingPromptProcessing
		}

		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "text_completion",
			Usage:   &usage,
		}

		jsonResult, _ := json.Marshal(resp)
		xlog.Debug("Response", "response", string(jsonResult))

		middleware.StampUsage(c, input.Model, totalTokenUsage.Prompt, totalTokenUsage.Completion)

		// Return the prediction in the response body
		return c.JSON(200, resp)
	}
}
