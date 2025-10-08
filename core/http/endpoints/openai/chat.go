package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"

	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

// ChatEndpoint is the OpenAI Completion API endpoint https://platform.openai.com/docs/api-reference/chat/create
// @Summary Generate a chat completions for a given prompt and model.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/chat/completions [post]
func ChatEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, startupOptions *config.ApplicationConfig) func(c *fiber.Ctx) error {
	var id, textContentToReturn string
	var created int

	process := func(s string, req *schema.OpenAIRequest, config *config.ModelConfig, loader *model.ModelLoader, responses chan schema.OpenAIResponse, extraUsage bool) error {
		initialMessage := schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant", Content: &textContentToReturn}}},
			Object:  "chat.completion.chunk",
		}
		responses <- initialMessage

		_, _, err := ComputeChoices(req, s, config, cl, startupOptions, loader, func(s string, c *[]schema.Choice) {}, func(s string, tokenUsage backend.TokenUsage) bool {
			usage := schema.OpenAIUsage{
				PromptTokens:     tokenUsage.Prompt,
				CompletionTokens: tokenUsage.Completion,
				TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
			}
			if extraUsage {
				usage.TimingTokenGeneration = tokenUsage.TimingTokenGeneration
				usage.TimingPromptProcessing = tokenUsage.TimingPromptProcessing
			}

			resp := schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{{Delta: &schema.Message{Content: &s}, Index: 0}},
				Object:  "chat.completion.chunk",
				Usage:   usage,
			}

			responses <- resp
			return true
		})
		close(responses)
		return err
	}
	processTools := func(noAction string, prompt string, req *schema.OpenAIRequest, config *config.ModelConfig, loader *model.ModelLoader, responses chan schema.OpenAIResponse, extraUsage bool) error {
		result := ""
		_, tokenUsage, err := ComputeChoices(req, prompt, config, cl, startupOptions, loader, func(s string, c *[]schema.Choice) {}, func(s string, usage backend.TokenUsage) bool {
			result += s
			// TODO: Change generated BNF grammar to be compliant with the schema so we can
			// stream the result token by token here.
			return true
		})
		if err != nil {
			return err
		}
		textContentToReturn = functions.ParseTextContent(result, config.FunctionsConfig)
		result = functions.CleanupLLMResult(result, config.FunctionsConfig)
		functionResults := functions.ParseFunctionCall(result, config.FunctionsConfig)
		log.Debug().Msgf("Text content to return: %s", textContentToReturn)
		noActionToRun := len(functionResults) > 0 && functionResults[0].Name == noAction || len(functionResults) == 0

		switch {
		case noActionToRun:
			initialMessage := schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant", Content: &textContentToReturn}}},
				Object:  "chat.completion.chunk",
			}
			responses <- initialMessage

			result, err := handleQuestion(config, cl, req, ml, startupOptions, functionResults, result, prompt)
			if err != nil {
				log.Error().Err(err).Msg("error handling question")
				return err
			}
			usage := schema.OpenAIUsage{
				PromptTokens:     tokenUsage.Prompt,
				CompletionTokens: tokenUsage.Completion,
				TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
			}
			if extraUsage {
				usage.TimingTokenGeneration = tokenUsage.TimingTokenGeneration
				usage.TimingPromptProcessing = tokenUsage.TimingPromptProcessing
			}

			resp := schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{{Delta: &schema.Message{Content: &result}, Index: 0}},
				Object:  "chat.completion.chunk",
				Usage:   usage,
			}

			responses <- resp

		default:
			for i, ss := range functionResults {
				name, args := ss.Name, ss.Arguments

				initialMessage := schema.OpenAIResponse{
					ID:      id,
					Created: created,
					Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []schema.Choice{{
						Delta: &schema.Message{
							Role: "assistant",
							ToolCalls: []schema.ToolCall{
								{
									Index: i,
									ID:    id,
									Type:  "function",
									FunctionCall: schema.FunctionCall{
										Name: name,
									},
								},
							},
						}}},
					Object: "chat.completion.chunk",
				}
				responses <- initialMessage

				responses <- schema.OpenAIResponse{
					ID:      id,
					Created: created,
					Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []schema.Choice{{
						Delta: &schema.Message{
							Role:    "assistant",
							Content: &textContentToReturn,
							ToolCalls: []schema.ToolCall{
								{
									Index: i,
									ID:    id,
									Type:  "function",
									FunctionCall: schema.FunctionCall{
										Arguments: args,
									},
								},
							},
						}}},
					Object: "chat.completion.chunk",
				}
			}
		}

		close(responses)
		return err
	}

	return func(c *fiber.Ctx) error {
		textContentToReturn = ""
		id = uuid.New().String()
		created = int(time.Now().Unix())

		input, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return fiber.ErrBadRequest
		}

		extraUsage := c.Get("Extra-Usage", "") != ""

		config, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			return fiber.ErrBadRequest
		}

		log.Debug().Msgf("Chat endpoint configuration read: %+v", config)

		funcs := input.Functions
		shouldUseFn := len(input.Functions) > 0 && config.ShouldUseFunctions()
		strictMode := false

		for _, f := range input.Functions {
			if f.Strict {
				strictMode = true
				break
			}
		}

		// Allow the user to set custom actions via config file
		// to be "embedded" in each model
		noActionName := "answer"
		noActionDescription := "use this action to answer without performing any action"

		if config.FunctionsConfig.NoActionFunctionName != "" {
			noActionName = config.FunctionsConfig.NoActionFunctionName
		}
		if config.FunctionsConfig.NoActionDescriptionName != "" {
			noActionDescription = config.FunctionsConfig.NoActionDescriptionName
		}

		if config.ResponseFormatMap != nil {
			d := schema.ChatCompletionResponseFormat{}
			dat, err := json.Marshal(config.ResponseFormatMap)
			if err != nil {
				return err
			}
			err = json.Unmarshal(dat, &d)
			if err != nil {
				return err
			}

			switch d.Type {
			case "json_object":
				input.Grammar = functions.JSONBNF
			case "json_schema":
				d := schema.JsonSchemaRequest{}
				dat, err := json.Marshal(config.ResponseFormatMap)
				if err != nil {
					return err
				}
				err = json.Unmarshal(dat, &d)
				if err != nil {
					return err
				}
				fs := &functions.JSONFunctionStructure{
					AnyOf: []functions.Item{d.JsonSchema.Schema},
				}
				g, err := fs.Grammar(config.FunctionsConfig.GrammarOptions()...)
				if err == nil {
					input.Grammar = g
				} else {
					log.Error().Err(err).Msg("Failed generating grammar")
				}
			}
		}

		config.Grammar = input.Grammar

		if shouldUseFn {
			log.Debug().Msgf("Response needs to process functions")
		}

		switch {
		case (!config.FunctionsConfig.GrammarConfig.NoGrammar || strictMode) && shouldUseFn:
			noActionGrammar := functions.Function{
				Name:        noActionName,
				Description: noActionDescription,
				Parameters: map[string]interface{}{
					"properties": map[string]interface{}{
						"message": map[string]interface{}{
							"type":        "string",
							"description": "The message to reply the user with",
						}},
				},
			}

			// Append the no action function
			if !config.FunctionsConfig.DisableNoAction && !strictMode {
				funcs = append(funcs, noActionGrammar)
			}

			// Force picking one of the functions by the request
			if config.FunctionToCall() != "" {
				funcs = funcs.Select(config.FunctionToCall())
			}

			// Update input grammar
			jsStruct := funcs.ToJSONStructure(config.FunctionsConfig.FunctionNameKey, config.FunctionsConfig.FunctionNameKey)
			g, err := jsStruct.Grammar(config.FunctionsConfig.GrammarOptions()...)
			if err == nil {
				config.Grammar = g
			} else {
				log.Error().Err(err).Msg("Failed generating grammar")
			}
		case input.JSONFunctionGrammarObject != nil:
			g, err := input.JSONFunctionGrammarObject.Grammar(config.FunctionsConfig.GrammarOptions()...)
			if err == nil {
				config.Grammar = g
			} else {
				log.Error().Err(err).Msg("Failed generating grammar")
			}
		default:
			// Force picking one of the functions by the request
			if config.FunctionToCall() != "" {
				funcs = funcs.Select(config.FunctionToCall())
			}
		}

		// process functions if we have any defined or if we have a function call string

		// functions are not supported in stream mode (yet?)
		toStream := input.Stream

		log.Debug().Msgf("Parameters: %+v", config)

		var predInput string

		// If we are using the tokenizer template, we don't need to process the messages
		// unless we are processing functions
		if !config.TemplateConfig.UseTokenizerTemplate || shouldUseFn {
			predInput = evaluator.TemplateMessages(*input, input.Messages, config, funcs, shouldUseFn)

			log.Debug().Msgf("Prompt (after templating): %s", predInput)
			if config.Grammar != "" {
				log.Debug().Msgf("Grammar: %+v", config.Grammar)
			}
		}

		switch {
		case toStream:

			log.Debug().Msgf("Stream request received")
			c.Context().SetContentType("text/event-stream")
			//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
			//	c.Set("Content-Type", "text/event-stream")
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")
			c.Set("Transfer-Encoding", "chunked")
			c.Set("X-Correlation-ID", id)

			responses := make(chan schema.OpenAIResponse)
			ended := make(chan error, 1)

			go func() {
				if !shouldUseFn {
					ended <- process(predInput, input, config, ml, responses, extraUsage)
				} else {
					ended <- processTools(noActionName, predInput, input, config, ml, responses, extraUsage)
				}
			}()

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
				usage := &schema.OpenAIUsage{}
				toolsCalled := false

			LOOP:
				for {
					select {
					case ev := <-responses:
						if len(ev.Choices) == 0 {
							log.Debug().Msgf("No choices in the response, skipping")
							continue
						}
						usage = &ev.Usage // Copy a pointer to the latest usage chunk so that the stop message can reference it
						if len(ev.Choices[0].Delta.ToolCalls) > 0 {
							toolsCalled = true
						}
						var buf bytes.Buffer
						enc := json.NewEncoder(&buf)
						enc.Encode(ev)
						log.Debug().Msgf("Sending chunk: %s", buf.String())
						_, err := fmt.Fprintf(w, "data: %v\n", buf.String())
						if err != nil {
							log.Debug().Msgf("Sending chunk failed: %v", err)
							input.Cancel()
						}
						w.Flush()
					case err := <-ended:
						if err == nil {
							break LOOP
						}
						log.Error().Msgf("Stream ended with error: %v", err)

						resp := &schema.OpenAIResponse{
							ID:      id,
							Created: created,
							Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
							Choices: []schema.Choice{
								{
									FinishReason: "stop",
									Index:        0,
									Delta:        &schema.Message{Content: "Internal error: " + err.Error()},
								}},
							Object: "chat.completion.chunk",
							Usage:  *usage,
						}
						respData, _ := json.Marshal(resp)

						w.WriteString(fmt.Sprintf("data: %s\n\n", respData))
						w.WriteString("data: [DONE]\n\n")
						w.Flush()

						return
					}
				}

				finishReason := "stop"
				if toolsCalled && len(input.Tools) > 0 {
					finishReason = "tool_calls"
				} else if toolsCalled {
					finishReason = "function_call"
				}

				resp := &schema.OpenAIResponse{
					ID:      id,
					Created: created,
					Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []schema.Choice{
						{
							FinishReason: finishReason,
							Index:        0,
							Delta:        &schema.Message{Content: &textContentToReturn},
						}},
					Object: "chat.completion.chunk",
					Usage:  *usage,
				}
				respData, _ := json.Marshal(resp)

				w.WriteString(fmt.Sprintf("data: %s\n\n", respData))
				w.WriteString("data: [DONE]\n\n")
				w.Flush()
				log.Debug().Msgf("Stream ended")
			}))

			return nil

		// no streaming mode
		default:

			tokenCallback := func(s string, c *[]schema.Choice) {
				if !shouldUseFn {
					// no function is called, just reply and use stop as finish reason
					*c = append(*c, schema.Choice{FinishReason: "stop", Index: 0, Message: &schema.Message{Role: "assistant", Content: &s}})
					return
				}

				textContentToReturn = functions.ParseTextContent(s, config.FunctionsConfig)
				s = functions.CleanupLLMResult(s, config.FunctionsConfig)
				results := functions.ParseFunctionCall(s, config.FunctionsConfig)
				log.Debug().Msgf("Text content to return: %s", textContentToReturn)
				noActionsToRun := len(results) > 0 && results[0].Name == noActionName || len(results) == 0

				switch {
				case noActionsToRun:
					result, err := handleQuestion(config, cl, input, ml, startupOptions, results, s, predInput)
					if err != nil {
						log.Error().Err(err).Msg("error handling question")
						return
					}

					*c = append(*c, schema.Choice{
						FinishReason: "stop",
						Message:      &schema.Message{Role: "assistant", Content: &result}})
				default:
					toolChoice := schema.Choice{
						FinishReason: "tool_calls",
						Message: &schema.Message{
							Role: "assistant",
						},
					}

					for _, ss := range results {
						name, args := ss.Name, ss.Arguments
						if len(input.Tools) > 0 {
							// If we are using tools, we condense the function calls into
							// a single response choice with all the tools
							toolChoice.Message.Content = textContentToReturn
							toolChoice.Message.ToolCalls = append(toolChoice.Message.ToolCalls,
								schema.ToolCall{
									ID:   id,
									Type: "function",
									FunctionCall: schema.FunctionCall{
										Name:      name,
										Arguments: args,
									},
								},
							)
						} else {
							// otherwise we return more choices directly (deprecated)
							*c = append(*c, schema.Choice{
								FinishReason: "function_call",
								Message: &schema.Message{
									Role:    "assistant",
									Content: &textContentToReturn,
									FunctionCall: map[string]interface{}{
										"name":      name,
										"arguments": args,
									},
								},
							})
						}
					}

					if len(input.Tools) > 0 {
						// we need to append our result if we are using tools
						*c = append(*c, toolChoice)
					}
				}

			}

			result, tokenUsage, err := ComputeChoices(
				input,
				predInput,
				config,
				cl,
				startupOptions,
				ml,
				tokenCallback,
				nil,
			)
			if err != nil {
				return err
			}
			usage := schema.OpenAIUsage{
				PromptTokens:     tokenUsage.Prompt,
				CompletionTokens: tokenUsage.Completion,
				TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
			}
			if extraUsage {
				usage.TimingTokenGeneration = tokenUsage.TimingTokenGeneration
				usage.TimingPromptProcessing = tokenUsage.TimingPromptProcessing
			}

			resp := &schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: result,
				Object:  "chat.completion",
				Usage:   usage,
			}
			respData, _ := json.Marshal(resp)
			log.Debug().Msgf("Response: %s", respData)

			// Return the prediction in the response body
			return c.JSON(resp)
		}
	}
}

func handleQuestion(config *config.ModelConfig, cl *config.ModelConfigLoader, input *schema.OpenAIRequest, ml *model.ModelLoader, o *config.ApplicationConfig, funcResults []functions.FuncCallResults, result, prompt string) (string, error) {

	if len(funcResults) == 0 && result != "" {
		log.Debug().Msgf("nothing function results but we had a message from the LLM")

		return result, nil
	}

	log.Debug().Msgf("nothing to do, computing a reply")
	arg := ""
	if len(funcResults) > 0 {
		arg = funcResults[0].Arguments
	}
	// If there is a message that the LLM already sends as part of the JSON reply, use it
	arguments := map[string]interface{}{}
	if err := json.Unmarshal([]byte(arg), &arguments); err != nil {
		log.Debug().Msg("handleQuestion: function result did not contain a valid JSON object")
	}
	m, exists := arguments["message"]
	if exists {
		switch message := m.(type) {
		case string:
			if message != "" {
				log.Debug().Msgf("Reply received from LLM: %s", message)
				message = backend.Finetune(*config, prompt, message)
				log.Debug().Msgf("Reply received from LLM(finetuned): %s", message)

				return message, nil
			}
		}
	}

	log.Debug().Msgf("No action received from LLM, without a message, computing a reply")
	// Otherwise ask the LLM to understand the JSON output and the context, and return a message
	// Note: This costs (in term of CPU/GPU) another computation
	config.Grammar = ""
	images := []string{}
	for _, m := range input.Messages {
		images = append(images, m.StringImages...)
	}
	videos := []string{}
	for _, m := range input.Messages {
		videos = append(videos, m.StringVideos...)
	}
	audios := []string{}
	for _, m := range input.Messages {
		audios = append(audios, m.StringAudios...)
	}

	predFunc, err := backend.ModelInference(input.Context, prompt, input.Messages, images, videos, audios, ml, config, cl, o, nil)
	if err != nil {
		log.Error().Err(err).Msg("model inference failed")
		return "", err
	}

	prediction, err := predFunc()
	if err != nil {
		log.Error().Err(err).Msg("prediction failed")
		return "", err
	}
	return backend.Finetune(*config, prompt, prediction.Response), nil
}
