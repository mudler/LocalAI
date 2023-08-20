package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-skynet/LocalAI/api/backend"
	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/api/schema"
	"github.com/go-skynet/LocalAI/pkg/grammar"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

func ChatEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	emptyMessage := ""

	process := func(s string, req *schema.OpenAIRequest, config *config.Config, loader *model.ModelLoader, responses chan schema.OpenAIResponse) {
		initialMessage := schema.OpenAIResponse{
			Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant", Content: &emptyMessage}}},
			Object:  "chat.completion.chunk",
		}
		responses <- initialMessage

		ComputeChoices(req, s, config, o, loader, func(s string, c *[]schema.Choice) {}, func(s string, usage backend.TokenUsage) bool {
			resp := schema.OpenAIResponse{
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
	return func(c *fiber.Ctx) error {
		processFunctions := false
		funcs := grammar.Functions{}
		modelFile, input, err := readInput(c, o, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		config, input, err := readConfig(modelFile, input, cm, o.Loader, o.Debug, o.Threads, o.ContextSize, o.F16)
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
			contentExists := i.Content != nil && *i.Content != ""
			// First attempt to populate content via a chat message specific template
			if config.TemplateConfig.ChatMessage != "" {
				chatMessageData := model.ChatMessageTemplateData{
					SystemPrompt: config.SystemPrompt,
					Role:         r,
					RoleName:     role,
					Content:      *i.Content,
					MessageIndex: messageIndex,
				}
				templatedChatMessage, err := o.Loader.EvaluateTemplateForChatMessage(config.TemplateConfig.ChatMessage, chatMessageData)
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
						content = fmt.Sprint(r, " ", *i.Content)
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
						content = fmt.Sprint(*i.Content)
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
		templatedInput, err := o.Loader.EvaluateTemplateForPrompt(model.ChatPromptTemplate, templateFile, model.PromptTemplateData{
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
			responses := make(chan schema.OpenAIResponse)

			go process(predInput, input, config, o.Loader, responses)

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {

				usage := &schema.OpenAIUsage{}

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

				resp := &schema.OpenAIResponse{
					Model: input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []schema.Choice{
						{
							FinishReason: "stop",
							Index:        0,
							Delta:        &schema.Message{Content: &emptyMessage},
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

		result, tokenUsage, err := ComputeChoices(input, predInput, config, o, o.Loader, func(s string, c *[]schema.Choice) {
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

								*c = append(*c, schema.Choice{Message: &schema.Message{Role: "assistant", Content: &message}})
								return
							}
						}
					}

					log.Debug().Msgf("No action received from LLM, without a message, computing a reply")
					// Otherwise ask the LLM to understand the JSON output and the context, and return a message
					// Note: This costs (in term of CPU) another computation
					config.Grammar = ""
					predFunc, err := backend.ModelInference(input.Context, predInput, o.Loader, *config, o, nil)
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
					*c = append(*c, schema.Choice{Message: &schema.Message{Role: "assistant", Content: &fineTunedResponse}})
				} else {
					// otherwise reply with the function call
					*c = append(*c, schema.Choice{
						FinishReason: "function_call",
						Message:      &schema.Message{Role: "assistant", FunctionCall: ss},
					})
				}

				return
			}
			*c = append(*c, schema.Choice{FinishReason: "stop", Index: 0, Message: &schema.Message{Role: "assistant", Content: &s}})
		}, nil)
		if err != nil {
			return err
		}

		resp := &schema.OpenAIResponse{
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
	}
}
