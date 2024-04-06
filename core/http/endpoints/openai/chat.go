package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"

	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

// ChatEndpoint is the OpenAI Completion API endpoint https://platform.openai.com/docs/api-reference/chat/create
// @Summary Generate a chat completions for a given prompt and model.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/chat/completions [post]
<<<<<<< HEAD
func ChatEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, startupOptions *config.ApplicationConfig) func(c *fiber.Ctx) error {
	emptyMessage := ""
	id := uuid.New().String()
	created := int(time.Now().Unix())

	process := func(s string, req *schema.OpenAIRequest, config *config.BackendConfig, loader *model.ModelLoader, responses chan schema.OpenAIResponse) {
		initialMessage := schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant", Content: &emptyMessage}}},
			Object:  "chat.completion.chunk",
		}
		responses <- initialMessage

		ComputeChoices(req, s, config, startupOptions, loader, func(s string, c *[]schema.Choice) {}, func(s string, usage backend.TokenUsage) bool {
			resp := schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{{Delta: &schema.Message{Content: &s}, Index: 0}},
				Object:  "chat.completion.chunk",
				Usage: schema.OpenAIUsage{
					PromptTokens:     usage.Prompt,
					CompletionTokens: usage.Completion,
					TotalTokens:      usage.Prompt + usage.Completion,
				},
			}

			responses <- resp
			return true
		})
		close(responses)
	}
	processTools := func(noAction string, prompt string, req *schema.OpenAIRequest, config *config.BackendConfig, loader *model.ModelLoader, responses chan schema.OpenAIResponse) {
		result := ""
		_, tokenUsage, _ := ComputeChoices(req, prompt, config, startupOptions, loader, func(s string, c *[]schema.Choice) {}, func(s string, usage backend.TokenUsage) bool {
			result += s
			// TODO: Change generated BNF grammar to be compliant with the schema so we can
			// stream the result token by token here.
			return true
		})

		results := parseFunctionCall(result, config.FunctionsConfig.ParallelCalls)
		noActionToRun := len(results) > 0 && results[0].name == noAction

		switch {
		case noActionToRun:
			initialMessage := schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant", Content: &emptyMessage}}},
				Object:  "chat.completion.chunk",
			}
			responses <- initialMessage

			result, err := handleQuestion(config, req, ml, startupOptions, results[0].arguments, prompt)
			if err != nil {
				log.Error().Err(err).Msg("error handling question")
				return
			}

			resp := schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{{Delta: &schema.Message{Content: &result}, Index: 0}},
				Object:  "chat.completion.chunk",
				Usage: schema.OpenAIUsage{
					PromptTokens:     tokenUsage.Prompt,
					CompletionTokens: tokenUsage.Completion,
					TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
				},
			}

			responses <- resp

		default:
			for i, ss := range results {
				name, args := ss.name, ss.arguments

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
							Role: "assistant",
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
	}

=======
func ChatEndpoint(fce *fiberContext.FiberContextExtractor, oais *services.OpenAIService) func(c *fiber.Ctx) error {
>>>>>>> rf-core-3
	return func(c *fiber.Ctx) error {
		_, request, err := fce.OpenAIRequestFromContext(c, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		// log.Debug().Msgf("`[CHAT] OpenAIRequest`: %+v", request)

		traceID, finalResultChannel, _, tokenChannel, err := oais.Chat(request, false, request.Stream)
		if err != nil {
			return err
		}

		if request.Stream {

			log.Debug().Msgf("Chat Stream request received")

<<<<<<< HEAD
		// process functions if we have any defined or if we have a function call string
		if len(input.Functions) > 0 && config.ShouldUseFunctions() {
			log.Debug().Msgf("Response needs to process functions")

			processFunctions = true

			noActionGrammar := grammar.Function{
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
			funcs = append(funcs, input.Functions...)
			if !config.FunctionsConfig.DisableNoAction {
				funcs = append(funcs, noActionGrammar)
			}

			// Force picking one of the functions by the request
			if config.FunctionToCall() != "" {
				funcs = funcs.Select(config.FunctionToCall())
			}

			// Update input grammar
			jsStruct := funcs.ToJSONStructure()
			config.Grammar = jsStruct.Grammar("", config.FunctionsConfig.ParallelCalls)
		} else if input.JSONFunctionGrammarObject != nil {
			config.Grammar = input.JSONFunctionGrammarObject.Grammar("", config.FunctionsConfig.ParallelCalls)
		}

		// functions are not supported in stream mode (yet?)
		toStream := input.Stream

		log.Debug().Msgf("Parameters: %+v", config)

		var predInput string

		suppressConfigSystemPrompt := false
		mess := []string{}
		for messageIndex, i := range input.Messages {
			var content string
			role := i.Role

			// if function call, we might want to customize the role so we can display better that the "assistant called a json action"
			// if an "assistant_function_call" role is defined, we use it, otherwise we use the role that is passed by in the request
			if (i.FunctionCall != nil || i.ToolCalls != nil) && i.Role == "assistant" {
				roleFn := "assistant_function_call"
				r := config.Roles[roleFn]
				if r != "" {
					role = roleFn
				}
			}
			r := config.Roles[role]
			contentExists := i.Content != nil && i.StringContent != ""

			fcall := i.FunctionCall
			if len(i.ToolCalls) > 0 {
				fcall = i.ToolCalls
			}

			// First attempt to populate content via a chat message specific template
			if config.TemplateConfig.ChatMessage != "" {
				chatMessageData := model.ChatMessageTemplateData{
					SystemPrompt: config.SystemPrompt,
					Role:         r,
					RoleName:     role,
					Content:      i.StringContent,
					FunctionCall: fcall,
					FunctionName: i.Name,
					LastMessage:  messageIndex == (len(input.Messages) - 1),
					Function:     config.Grammar != "" && (messageIndex == (len(input.Messages) - 1)),
					MessageIndex: messageIndex,
				}
				templatedChatMessage, err := ml.EvaluateTemplateForChatMessage(config.TemplateConfig.ChatMessage, chatMessageData)
				if err != nil {
					log.Error().Err(err).Interface("message", chatMessageData).Str("template", config.TemplateConfig.ChatMessage).Msg("error processing message with template, skipping")
				} else {
					if templatedChatMessage == "" {
						log.Warn().Msgf("template \"%s\" produced blank output for %+v. Skipping!", config.TemplateConfig.ChatMessage, chatMessageData)
						continue // TODO: This continue is here intentionally to skip over the line `mess = append(mess, content)` below, and to prevent the sprintf
					}
					log.Debug().Msgf("templated message for chat: %s", templatedChatMessage)
					content = templatedChatMessage
				}
			}

			marshalAnyRole := func(f any) {
				j, err := json.Marshal(f)
				if err == nil {
					if contentExists {
						content += "\n" + fmt.Sprint(r, " ", string(j))
					} else {
						content = fmt.Sprint(r, " ", string(j))
					}
				}
			}
			marshalAny := func(f any) {
				j, err := json.Marshal(f)
				if err == nil {
					if contentExists {
						content += "\n" + string(j)
					} else {
						content = string(j)
					}
				}
			}
			// If this model doesn't have such a template, or if that template fails to return a value, template at the message level.
			if content == "" {
				if r != "" {
					if contentExists {
						content = fmt.Sprint(r, i.StringContent)
					}

					if i.FunctionCall != nil {
						marshalAnyRole(i.FunctionCall)
					}
					if i.ToolCalls != nil {
						marshalAnyRole(i.ToolCalls)
					}
				} else {
					if contentExists {
						content = fmt.Sprint(i.StringContent)
					}
					if i.FunctionCall != nil {
						marshalAny(i.FunctionCall)
					}
					if i.ToolCalls != nil {
						marshalAny(i.ToolCalls)
					}
				}
				// Special Handling: System. We care if it was printed at all, not the r branch, so check seperately
				if contentExists && role == "system" {
					suppressConfigSystemPrompt = true
				}
			}

			mess = append(mess, content)
		}

		predInput = strings.Join(mess, "\n")
		log.Debug().Msgf("Prompt (before templating): %s", predInput)

		if toStream {
			log.Debug().Msgf("Stream request received")
=======
>>>>>>> rf-core-3
			c.Context().SetContentType("text/event-stream")
			//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
			//
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")
			c.Set("Transfer-Encoding", "chunked")

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
				usage := &schema.OpenAIUsage{}
				toolsCalled := false
				for ev := range tokenChannel {
					if ev.Error != nil {
						log.Debug().Msgf("chat streaming responseChannel error: %q", ev.Error)
						request.Cancel()
						break
					}
					usage = &ev.Value.Usage // Copy a pointer to the latest usage chunk so that the stop message can reference it

					if len(ev.Value.Choices[0].Delta.ToolCalls) > 0 {
						toolsCalled = true
					}
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					if ev.Error != nil {
						log.Debug().Msgf("[ChatEndpoint] error to debug during tokenChannel handler: %q", ev.Error)
						enc.Encode(ev.Error)
					} else {
						enc.Encode(ev.Value)
					}
					log.Debug().Msgf("chat streaming sending chunk: %s", buf.String())
					_, err := fmt.Fprintf(w, "data: %v\n", buf.String())
					if err != nil {
						log.Debug().Msgf("Sending chunk failed: %v", err)
						request.Cancel()
						break
					}
					err = w.Flush()
					if err != nil {
						log.Debug().Msg("error while flushing, closing connection")
						request.Cancel()
						break
					}
				}

				finishReason := "stop"
				if toolsCalled {
					finishReason = "tool_calls"
				} else if toolsCalled && len(request.Tools) == 0 {
					finishReason = "function_call"
				}

				resp := &schema.OpenAIResponse{
					ID:      traceID.ID,
					Created: traceID.Created,
					Model:   request.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []schema.Choice{
						{
							FinishReason: finishReason,
							Index:        0,
							Delta:        &schema.Message{Content: ""},
						}},
					Object: "chat.completion.chunk",
					Usage:  *usage,
				}
				respData, _ := json.Marshal(resp)

				w.WriteString(fmt.Sprintf("data: %s\n\n", respData))
				w.WriteString("data: [DONE]\n\n")
				w.Flush()
			}))

			return nil
<<<<<<< HEAD

		// no streaming mode
		default:
			result, tokenUsage, err := ComputeChoices(input, predInput, config, startupOptions, ml, func(s string, c *[]schema.Choice) {
				if !processFunctions {
					// no function is called, just reply and use stop as finish reason
					*c = append(*c, schema.Choice{FinishReason: "stop", Index: 0, Message: &schema.Message{Role: "assistant", Content: &s}})
					return
				}

				results := parseFunctionCall(s, config.FunctionsConfig.ParallelCalls)
				noActionsToRun := len(results) > 0 && results[0].name == noActionName

				switch {
				case noActionsToRun:
					result, err := handleQuestion(config, input, ml, startupOptions, results[0].arguments, predInput)
					if err != nil {
						log.Error().Err(err).Msg("error handling question")
						return
					}
					*c = append(*c, schema.Choice{
						Message: &schema.Message{Role: "assistant", Content: &result}})
				default:
					toolChoice := schema.Choice{
						Message: &schema.Message{
							Role: "assistant",
						},
					}

					if len(input.Tools) > 0 {
						toolChoice.FinishReason = "tool_calls"
					}

					for _, ss := range results {
						name, args := ss.name, ss.arguments
						if len(input.Tools) > 0 {
							// If we are using tools, we condense the function calls into
							// a single response choice with all the tools
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
							// otherwise we return more choices directly
							*c = append(*c, schema.Choice{
								FinishReason: "function_call",
								Message: &schema.Message{
									Role: "assistant",
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

			}, nil)
			if err != nil {
				return err
			}

			resp := &schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: result,
				Object:  "chat.completion",
				Usage: schema.OpenAIUsage{
					PromptTokens:     tokenUsage.Prompt,
					CompletionTokens: tokenUsage.Completion,
					TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
				},
			}
			respData, _ := json.Marshal(resp)
			log.Debug().Msgf("Response: %s", respData)

			// Return the prediction in the response body
			return c.JSON(resp)
=======
>>>>>>> rf-core-3
		}

		// TODO is this proper to have exclusive from Stream, or do we need to issue both responses?
		rawResponse := <-finalResultChannel

		if rawResponse.Error != nil {
			return rawResponse.Error
		}

		jsonResult, _ := json.Marshal(rawResponse.Value)
		log.Debug().Msgf("Chat Final Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(rawResponse.Value)
	}
}
<<<<<<< HEAD

func handleQuestion(config *config.BackendConfig, input *schema.OpenAIRequest, ml *model.ModelLoader, o *config.ApplicationConfig, args, prompt string) (string, error) {
	log.Debug().Msgf("nothing to do, computing a reply")

	// If there is a message that the LLM already sends as part of the JSON reply, use it
	arguments := map[string]interface{}{}
	json.Unmarshal([]byte(args), &arguments)
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

	predFunc, err := backend.ModelInference(input.Context, prompt, images, ml, *config, o, nil)
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

type funcCallResults struct {
	name      string
	arguments string
}

func parseFunctionCall(llmresult string, multipleResults bool) []funcCallResults {
	results := []funcCallResults{}

	// TODO: use generics to avoid this code duplication
	if multipleResults {
		ss := []map[string]interface{}{}
		s := utils.EscapeNewLines(llmresult)
		json.Unmarshal([]byte(s), &ss)
		log.Debug().Msgf("Function return: %s %+v", s, ss)

		for _, s := range ss {
			func_name, ok := s["function"]
			if !ok {
				continue
			}
			args, ok := s["arguments"]
			if !ok {
				continue
			}
			d, _ := json.Marshal(args)
			funcName, ok := func_name.(string)
			if !ok {
				continue
			}
			results = append(results, funcCallResults{name: funcName, arguments: string(d)})
		}
	} else {
		// As we have to change the result before processing, we can't stream the answer token-by-token (yet?)
		ss := map[string]interface{}{}
		// This prevent newlines to break JSON parsing for clients
		s := utils.EscapeNewLines(llmresult)
		json.Unmarshal([]byte(s), &ss)
		log.Debug().Msgf("Function return: %s %+v", s, ss)

		// The grammar defines the function name as "function", while OpenAI returns "name"
		func_name, ok := ss["function"]
		if !ok {
			return results
		}
		// Similarly, while here arguments is a map[string]interface{}, OpenAI actually want a stringified object
		args, ok := ss["arguments"] // arguments needs to be a string, but we return an object from the grammar result (TODO: fix)
		if !ok {
			return results
		}
		d, _ := json.Marshal(args)
		funcName, ok := func_name.(string)
		if !ok {
			return results
		}
		results = append(results, funcCallResults{name: funcName, arguments: string(d)})
	}

	return results
}
=======
>>>>>>> rf-core-3
