package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/go-skynet/LocalAI/core/backend"
	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

func ChatEndpoint(fce *fiberContext.FiberContextExtractor, oais *services.OpenAIService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		_, request, err := fce.OpenAIRequestFromContext(c, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("`[CHAT] OpenAIRequest`: %+v", request)

		traceID, finalResultChannel, _, tokenChannel, err := oais.Chat(request, false, request.Stream)
		if err != nil {
			return err
		}

		if request.Stream {

			log.Debug().Msgf("Stream request received")
			c.Context().SetContentType("text/event-stream")
			//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
			//c.Set("Content-Type", "text/event-stream")
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")
			c.Set("Transfer-Encoding", "chunked")

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
				w.WriteString("DUMB TEST????\n")
				w.Flush()

				usage := &backend.TokenUsage{}
				toolsCalled := false
				for ev := range tokenChannel {
					if ev.Error != nil {
						log.Debug().Msgf("chat streaming responseChannel error: %q", ev.Error)
						request.Cancel()
						break
					}
					usage = &ev.Value.Usage // Copy a pointer to the latest usage chunk so that the stop message can reference it

					/// TODO DAVE: THIS IS IMPORTANT BUT IT'S INTENTIONALLY BROKEN RIGHT NOW UNTIL WE FIGURE OUT HOW TO GET A CHOICE PARAM PER TOKEN
					// if len(ev.Value.Choices[0].Delta.ToolCalls) > 0 {
					// 	toolsCalled = true
					// }
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					enc.Encode(ev.Value.Response)
					log.Debug().Msgf("Sending chunk: %s", buf.String())
					_, err := fmt.Fprintf(w, "data: %v\n", buf.String())
					if err != nil {
						log.Debug().Msgf("Sending chunk failed: %v", err)
						request.Cancel()
						break
					}
					w.Flush()
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
					Usage: schema.OpenAIUsage{
						PromptTokens:     usage.Prompt,
						CompletionTokens: usage.Completion,
						TotalTokens:      usage.Completion + usage.Prompt,
					},
				}
				respData, _ := json.Marshal(resp)

				w.WriteString(fmt.Sprintf("data: %s\n\n", respData))
				w.WriteString("data: [DONE]\n\n")
				w.Flush()
				log.Warn().Msg("DELETEME:: SetBodyStreamWriter:: Done!!!")
			}))
			return nil
		}

		// TODO is this proper to have exclusive from Stream, or do we need to issue both responses?
		rawResponse := <-finalResultChannel

		if rawResponse.Error != nil {
			return rawResponse.Error
		}

		jsonResult, _ := json.Marshal(rawResponse.Value)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(rawResponse.Value)
	}
}

// 		return func(c *fiber.Ctx) error {
// 			processFunctions := false
// 			funcs := grammar.Functions{}
// 			modelFile, input, err := readRequest(c, ml, startupOptions, true)
// 			if err != nil {
// 				return fmt.Errorf("failed reading parameters from request:%w", err)
// 			}

// 			config, input, err := mergeRequestWithConfig(modelFile, input, cl, ml, startupOptions.Debug, startupOptions.Threads, startupOptions.ContextSize, startupOptions.F16)
// 			if err != nil {
// 				return fmt.Errorf("failed reading parameters from request:%w", err)
// 			}
// 			log.Debug().Msgf("Configuration read: %+v", config)

// 			if input.ResponseFormat.Type == "json_object" {
// 				input.Grammar = grammar.JSONBNF
// 			}

// 			// process functions if we have any defined or if we have a function call string
// 			if len(input.Functions) > 0 && config.ShouldUseFunctions() {
// 				log.Debug().Msgf("Response needs to process functions")

// 				processFunctions = true

// 				noActionGrammar := grammar.Function{
// 					Name:        noActionName,
// 					Description: noActionDescription,
// 					Parameters: map[string]interface{}{
// 						"properties": map[string]interface{}{
// 							"message": map[string]interface{}{
// 								"type":        "string",
// 								"description": "The message to reply the user with",
// 							}},
// 					},
// 				}

// 				// Append the no action function
// 				funcs = append(funcs, input.Functions...)
// 				if !config.FunctionsConfig.DisableNoAction {
// 					funcs = append(funcs, noActionGrammar)
// 				}

// 				// Force picking one of the functions by the request
// 				if config.FunctionToCall() != "" {
// 					funcs = funcs.Select(config.FunctionToCall())
// 				}

// 				// Update input grammar
// 				jsStruct := funcs.ToJSONStructure()
// 				config.Grammar = jsStruct.Grammar("", config.FunctionsConfig.ParallelCalls)
// 			} else if input.JSONFunctionGrammarObject != nil {
// 				config.Grammar = input.JSONFunctionGrammarObject.Grammar("", config.FunctionsConfig.ParallelCalls)
// 			}

// 			// functions are not supported in stream mode (yet?)
// 			toStream := input.Stream

// 			log.Debug().Msgf("Parameters: %+v", config)

// 			// var predInput string

// 			// suppressConfigSystemPrompt := false
// 			// mess := []string{}
// 			// for messageIndex, i := range input.Messages {
// 			// 	var content string
// 			// 	role := i.Role

// 			// 	// if function call, we might want to customize the role so we can display better that the "assistant called a json action"
// 			// 	// if an "assistant_function_call" role is defined, we use it, otherwise we use the role that is passed by in the request
// 			// 	if i.FunctionCall != nil && i.Role == "assistant" {
// 			// 		roleFn := "assistant_function_call"
// 			// 		r := config.Roles[roleFn]
// 			// 		if r != "" {
// 			// 			role = roleFn
// 			// 		}
// 			// 	}
// 			// 	r := config.Roles[role]
// 			// 	contentExists := i.Content != nil && i.StringContent != ""

// 			// 	// First attempt to populate content via a chat message specific template
// 			// 	if config.TemplateConfig.ChatMessage != "" {
// 			// 		chatMessageData := model.ChatMessageTemplateData{
// 			// 			SystemPrompt: config.SystemPrompt,
// 			// 			Role:         r,
// 			// 			RoleName:     role,
// 			// 			Content:      i.StringContent,
// 			// 			FunctionName: i.Name,
// 			// 			MessageIndex: messageIndex,
// 			// 		}
// 			// 		templatedChatMessage, err := ml.EvaluateTemplateForChatMessage(config.TemplateConfig.ChatMessage, chatMessageData)
// 			// 		if err != nil {
// 			// 			log.Error().Msgf("error processing message %+v using template \"%s\": %v. Skipping!", chatMessageData, config.TemplateConfig.ChatMessage, err)
// 			// 		} else {
// 			// 			if templatedChatMessage == "" {
// 			// 				log.Warn().Msgf("template \"%s\" produced blank output for %+v. Skipping!", config.TemplateConfig.ChatMessage, chatMessageData)
// 			// 				continue // TODO: This continue is here intentionally to skip over the line `mess = append(mess, content)` below, and to prevent the sprintf
// 			// 			}
// 			// 			log.Debug().Msgf("templated message for chat: %s", templatedChatMessage)
// 			// 			content = templatedChatMessage
// 			// 		}
// 			// 	}
// 			// 	// If this model doesn't have such a template, or if that template fails to return a value, template at the message level.
// 			// 	if content == "" {
// 			// 		if r != "" {
// 			// 			if contentExists {
// 			// 				content = fmt.Sprint(r, i.StringContent)
// 			// 			}
// 			// 			if i.FunctionCall != nil {
// 			// 				j, err := json.Marshal(i.FunctionCall)
// 			// 				if err == nil {
// 			// 					if contentExists {
// 			// 						content += "\n" + fmt.Sprint(r, " ", string(j))
// 			// 					} else {
// 			// 						content = fmt.Sprint(r, " ", string(j))
// 			// 					}
// 			// 				}
// 			// 			}
// 			// 		} else {
// 			// 			if contentExists {
// 			// 				content = fmt.Sprint(i.StringContent)
// 			// 			}
// 			// 			if i.FunctionCall != nil {
// 			// 				j, err := json.Marshal(i.FunctionCall)
// 			// 				if err == nil {
// 			// 					if contentExists {
// 			// 						content += "\n" + string(j)
// 			// 					} else {
// 			// 						content = string(j)
// 			// 					}
// 			// 				}
// 			// 			}
// 			// 		}
// 			// 		// Special Handling: System. We care if it was printed at all, not the r branch, so check seperately
// 			// 		if contentExists && role == "system" {
// 			// 			suppressConfigSystemPrompt = true
// 			// 		}
// 			// 	}

// 			// 	mess = append(mess, content)
// 			// }

// 			// predInput = strings.Join(mess, "\n")
// 			// log.Debug().Msgf("Prompt (before templating): %s", predInput)

// 			if toStream {
// 				log.Debug().Msgf("Stream request received")
// 				c.Context().SetContentType("text/event-stream")
// 				//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
// 				//	c.Set("Content-Type", "text/event-stream")
// 				c.Set("Cache-Control", "no-cache")
// 				c.Set("Connection", "keep-alive")
// 				c.Set("Transfer-Encoding", "chunked")
// 			}

// 			// templateFile := ""

// 			// // A model can have a "file.bin.tmpl" file associated with a prompt template prefix
// 			// if ml.ExistsInModelPath(fmt.Sprintf("%s.tmpl", config.Model)) {
// 			// 	templateFile = config.Model
// 			// }

// 			// if config.TemplateConfig.Chat != "" && !processFunctions {
// 			// 	templateFile = config.TemplateConfig.Chat
// 			// }

// 			// if config.TemplateConfig.Functions != "" && processFunctions {
// 			// 	templateFile = config.TemplateConfig.Functions
// 			// }

// 			// if templateFile != "" {
// 			// 	templatedInput, err := ml.EvaluateTemplateForPrompt(model.ChatPromptTemplate, templateFile, model.PromptTemplateData{
// 			// 		SystemPrompt:         config.SystemPrompt,
// 			// 		SuppressSystemPrompt: suppressConfigSystemPrompt,
// 			// 		Input:                predInput,
// 			// 		Functions:            funcs,
// 			// 	})
// 			// 	if err == nil {
// 			// 		predInput = templatedInput
// 			// 		log.Debug().Msgf("Template found, input modified to: %s", predInput)
// 			// 	} else {
// 			// 		log.Debug().Msgf("Template failed loading: %s", err.Error())
// 			// 	}
// 			// }

// 			// log.Debug().Msgf("Prompt (after templating): %s", predInput)
// 			// if processFunctions {
// 			// 	log.Debug().Msgf("Grammar: %+v", config.Grammar)
// 			// }

// 			switch {
// 			case toStream:
// 				responses := make(chan schema.OpenAIResponse)

// 				if !processFunctions {
// 					go process(predInput, input, config, ml, responses)
// 				} else {
// 					go processTools(noActionName, predInput, input, config, ml, responses)
// 				}

//

// 			// no streaming mode
// 			default:
// 				result, tokenUsage, err := ComputeChoices(input, predInput, config, startupOptions, ml, func(s string, c *[]schema.Choice) {
// 					if !processFunctions {
// 						// no function is called, just reply and use stop as finish reason
// 						*c = append(*c, schema.Choice{FinishReason: "stop", Index: 0, Message: &schema.Message{Role: "assistant", Content: &s}})
// 						return
// 					}

// 					results := parseFunctionCall(s, config.FunctionsConfig.ParallelCalls)
// 					noActionsToRun := len(results) > 0 && results[0].name == noActionName

// 					switch {
// 					case noActionsToRun:
// 						result, err := handleQuestion(config, input, ml, startupOptions, results[0].arguments, predInput)
// 						if err != nil {
// 							log.Error().Msgf("error handling question: %s", err.Error())
// 							return
// 						}
// 						*c = append(*c, schema.Choice{
// 							Message: &schema.Message{Role: "assistant", Content: &result}})
// 					default:
// 						toolChoice := schema.Choice{
// 							Message: &schema.Message{
// 								Role: "assistant",
// 							},
// 						}

// 						if len(input.Tools) > 0 {
// 							toolChoice.FinishReason = "tool_calls"
// 						}

// 						for _, ss := range results {
// 							name, args := ss.name, ss.arguments
// 							if len(input.Tools) > 0 {
// 								// If we are using tools, we condense the function calls into
// 								// a single response choice with all the tools
// 								toolChoice.Message.ToolCalls = append(toolChoice.Message.ToolCalls,
// 									schema.ToolCall{
// 										ID:   id,
// 										Type: "function",
// 										FunctionCall: schema.FunctionCall{
// 											Name:      name,
// 											Arguments: args,
// 										},
// 									},
// 								)
// 							} else {
// 								// otherwise we return more choices directly
// 								*c = append(*c, schema.Choice{
// 									FinishReason: "function_call",
// 									Message: &schema.Message{
// 										Role: "assistant",
// 										FunctionCall: map[string]interface{}{
// 											"name":      name,
// 											"arguments": args,
// 										},
// 									},
// 								})
// 							}
// 						}

// 						if len(input.Tools) > 0 {
// 							// we need to append our result if we are using tools
// 							*c = append(*c, toolChoice)
// 						}
// 					}

// 				}, nil)
// 				if err != nil {
// 					return err
// 				}

// 				resp := &schema.OpenAIResponse{
// 					ID:      id,
// 					Created: created,
// 					Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
// 					Choices: result,
// 					Object:  "chat.completion",
// 					Usage: schema.OpenAIUsage{
// 						PromptTokens:     tokenUsage.Prompt,
// 						CompletionTokens: tokenUsage.Completion,
// 						TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
// 					},
// 				}
// 				respData, _ := json.Marshal(resp)
// 				log.Debug().Msgf("Response: %s", respData)

// 				// Return the prediction in the response body
// 				return c.JSON(resp)
// 			}

// 		}
// 	}

// 	return results
// }
