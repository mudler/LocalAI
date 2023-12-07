package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/pkg/datamodel"
	"github.com/go-skynet/LocalAI/pkg/grammar"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

func ChatEndpoint(cl *backend.ConfigLoader, ml *model.ModelLoader, so *datamodel.StartupOptions) func(c *fiber.Ctx) error {
	emptyMessage := ""
	id := uuid.New().String()
	created := int(time.Now().Unix())

	process := func(s string, req *datamodel.OpenAIRequest, config *datamodel.Config, loader *model.ModelLoader, responses chan datamodel.OpenAIResponse) {
		initialMessage := datamodel.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: []datamodel.Choice{{Delta: &datamodel.Message{Role: "assistant", Content: &emptyMessage}}},
			Object:  "chat.completion.chunk",
		}
		responses <- initialMessage

		ComputeChoices(req, s, config, so, loader, func(s string, c *[]datamodel.Choice) {}, func(s string, usage backend.TokenUsage) bool {
			resp := datamodel.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []datamodel.Choice{{Delta: &datamodel.Message{Content: &s}, Index: 0}},
				Object:  "chat.completion.chunk",
				Usage: datamodel.OpenAIUsage{
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
	return func(c *fiber.Ctx) error {
		processFunctions := false
		funcs := grammar.Functions{}
		modelFile, input, err := readInput(c, so, ml, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		config, input, err := readConfig(modelFile, input, cl, ml, so.Debug, so.Threads, so.ContextSize, so.F16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}
		log.Debug().Msgf("Configuration read: %+v", config)

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

		if input.ResponseFormat.Type == "json_object" {
			input.Grammar = grammar.JSONBNF
		}

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
			config.Grammar = jsStruct.Grammar("")
		} else if input.JSONFunctionGrammarObject != nil {
			config.Grammar = input.JSONFunctionGrammarObject.Grammar("")
		}

		// functions are not supported in stream mode (yet?)
		toStream := input.Stream && !processFunctions

		log.Debug().Msgf("Parameters: %+v", config)

		var predInput string

		suppressConfigSystemPrompt := false
		mess := []string{}
		for messageIndex, i := range input.Messages {
			var content string
			role := i.Role

			// if function call, we might want to customize the role so we can display better that the "assistant called a json action"
			// if an "assistant_function_call" role is defined, we use it, otherwise we use the role that is passed by in the request
			if i.FunctionCall != nil && i.Role == "assistant" {
				roleFn := "assistant_function_call"
				r := config.Roles[roleFn]
				if r != "" {
					role = roleFn
				}
			}
			r := config.Roles[role]
			contentExists := i.Content != nil && i.StringContent != ""
			// First attempt to populate content via a chat message specific template
			if config.TemplateConfig.ChatMessage != "" {
				chatMessageData := model.ChatMessageTemplateData{
					SystemPrompt: config.SystemPrompt,
					Role:         r,
					RoleName:     role,
					Content:      i.StringContent,
					MessageIndex: messageIndex,
				}
				templatedChatMessage, err := ml.EvaluateTemplateForChatMessage(config.TemplateConfig.ChatMessage, chatMessageData)
				if err != nil {
					log.Error().Msgf("error processing message %+v using template \"%s\": %v. Skipping!", chatMessageData, config.TemplateConfig.ChatMessage, err)
				} else {
					if templatedChatMessage == "" {
						log.Warn().Msgf("template \"%s\" produced blank output for %+v. Skipping!", config.TemplateConfig.ChatMessage, chatMessageData)
						continue // TODO: This continue is here intentionally to skip over the line `mess = append(mess, content)` below, and to prevent the sprintf
					}
					log.Debug().Msgf("templated message for chat: %s", templatedChatMessage)
					content = templatedChatMessage
				}
			}
			// If this model doesn't have such a template, or if that template fails to return a value, template at the message level.
			if content == "" {
				if r != "" {
					if contentExists {
						content = fmt.Sprint(r, i.StringContent)
					}
					if i.FunctionCall != nil {
						j, err := json.Marshal(i.FunctionCall)
						if err == nil {
							if contentExists {
								content += "\n" + fmt.Sprint(r, " ", string(j))
							} else {
								content = fmt.Sprint(r, " ", string(j))
							}
						}
					}
				} else {
					if contentExists {
						content = fmt.Sprint(i.StringContent)
					}
					if i.FunctionCall != nil {
						j, err := json.Marshal(i.FunctionCall)
						if err == nil {
							if contentExists {
								content += "\n" + string(j)
							} else {
								content = string(j)
							}
						}
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
			c.Context().SetContentType("text/event-stream")
			//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
			//	c.Set("Content-Type", "text/event-stream")
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")
			c.Set("Transfer-Encoding", "chunked")
		}

		templateFile := config.Model

		if config.TemplateConfig.Chat != "" && !processFunctions {
			templateFile = config.TemplateConfig.Chat
		}

		if config.TemplateConfig.Functions != "" && processFunctions {
			templateFile = config.TemplateConfig.Functions
		}

		// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
		templatedInput, err := ml.EvaluateTemplateForPrompt(model.ChatPromptTemplate, templateFile, model.PromptTemplateData{
			SystemPrompt:         config.SystemPrompt,
			SuppressSystemPrompt: suppressConfigSystemPrompt,
			Input:                predInput,
			Functions:            funcs,
		})
		if err == nil {
			predInput = templatedInput
			log.Debug().Msgf("Template found, input modified to: %s", predInput)
		} else {
			log.Debug().Msgf("Template failed loading: %s", err.Error())
		}

		log.Debug().Msgf("Prompt (after templating): %s", predInput)
		if processFunctions {
			log.Debug().Msgf("Grammar: %+v", config.Grammar)
		}

		if toStream {
			responses := make(chan datamodel.OpenAIResponse)

			go process(predInput, input, config, ml, responses)

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {

				usage := &datamodel.OpenAIUsage{}

				for ev := range responses {
					usage = &ev.Usage // Copy a pointer to the latest usage chunk so that the stop message can reference it
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					enc.Encode(ev)
					log.Debug().Msgf("Sending chunk: %s", buf.String())
					_, err := fmt.Fprintf(w, "data: %v\n", buf.String())
					if err != nil {
						log.Debug().Msgf("Sending chunk failed: %v", err)
						input.Cancel()
						break
					}
					w.Flush()
				}

				resp := &datamodel.OpenAIResponse{
					ID:      id,
					Created: created,
					Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []datamodel.Choice{
						{
							FinishReason: "stop",
							Index:        0,
							Delta:        &datamodel.Message{Content: &emptyMessage},
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
		}

		result, tokenUsage, err := ComputeChoices(input, predInput, config, so, ml, func(s string, c *[]datamodel.Choice) {
			if processFunctions {
				// As we have to change the result before processing, we can't stream the answer (yet?)
				ss := map[string]interface{}{}
				// This prevent newlines to break JSON parsing for clients
				s = utils.EscapeNewLines(s)
				json.Unmarshal([]byte(s), &ss)
				log.Debug().Msgf("Function return: %s %+v", s, ss)

				// The grammar defines the function name as "function", while OpenAI returns "name"
				func_name := ss["function"]
				// Similarly, while here arguments is a map[string]interface{}, OpenAI actually want a stringified object
				args := ss["arguments"] // arguments needs to be a string, but we return an object from the grammar result (TODO: fix)
				d, _ := json.Marshal(args)

				ss["arguments"] = string(d)
				ss["name"] = func_name

				// if do nothing, reply with a message
				if func_name == noActionName {
					log.Debug().Msgf("nothing to do, computing a reply")

					// If there is a message that the LLM already sends as part of the JSON reply, use it
					arguments := map[string]interface{}{}
					json.Unmarshal([]byte(d), &arguments)
					m, exists := arguments["message"]
					if exists {
						switch message := m.(type) {
						case string:
							if message != "" {
								log.Debug().Msgf("Reply received from LLM: %s", message)
								message = backend.Finetune(*config, predInput, message)
								log.Debug().Msgf("Reply received from LLM(finetuned): %s", message)

								*c = append(*c, datamodel.Choice{Message: &datamodel.Message{Role: "assistant", Content: &message}})
								return
							}
						}
					}

					log.Debug().Msgf("No action received from LLM, without a message, computing a reply")
					// Otherwise ask the LLM to understand the JSON output and the context, and return a message
					// Note: This costs (in term of CPU) another computation
					config.Grammar = ""
					images := []string{}
					for _, m := range input.Messages {
						images = append(images, m.StringImages...)
					}
					predFunc, err := backend.ModelInference(input.Context, predInput, images, ml, *config, so, nil)
					if err != nil {
						log.Error().Msgf("inference error: %s", err.Error())
						return
					}

					prediction, err := predFunc()
					if err != nil {
						log.Error().Msgf("inference error: %s", err.Error())
						return
					}

					fineTunedResponse := backend.Finetune(*config, predInput, prediction.Response)
					*c = append(*c, datamodel.Choice{Message: &datamodel.Message{Role: "assistant", Content: &fineTunedResponse}})
				} else {
					// otherwise reply with the function call
					*c = append(*c, datamodel.Choice{
						FinishReason: "function_call",
						Message:      &datamodel.Message{Role: "assistant", FunctionCall: ss},
					})
				}

				return
			}
			*c = append(*c, datamodel.Choice{FinishReason: "stop", Index: 0, Message: &datamodel.Message{Role: "assistant", Content: &s}})
		}, nil)
		if err != nil {
			return err
		}

		resp := &datamodel.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "chat.completion",
			Usage: datamodel.OpenAIUsage{
				PromptTokens:     tokenUsage.Prompt,
				CompletionTokens: tokenUsage.Completion,
				TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
			},
		}
		respData, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", respData)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
