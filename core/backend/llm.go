package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/grammar"
	"github.com/go-skynet/LocalAI/pkg/grpc"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/schema"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

////////// TYPES //////////////

type LLMResponse struct {
	Response string // should this be []byte?
	Usage    TokenUsage
}

// TODO: Test removing this and using the variant in pkg/schema someday?
type TokenUsage struct {
	Prompt     int
	Completion int
}

type TemplateConfigBindingFn func(*schema.Config) *string

// type LLMStreamProcessor func(s string, req *schema.OpenAIRequest, config *schema.Config, loader *model.ModelLoader, responses chan schema.OpenAIResponse)

/////// CONSTS ///////////

const DEFAULT_NO_ACTION_NAME = "answer"
const DEFAULT_NO_ACTION_DESCRIPTION = "use this action to answer without performing any action"

////// INFERENCE /////////

func ModelInference(ctx context.Context, s string, images []string, loader *model.ModelLoader, c schema.Config, o *schema.StartupOptions, tokenCallback func(string, TokenUsage) bool) (func() (LLMResponse, error), error) {
	modelFile := c.Model

	grpcOpts := gRPCModelOpts(c)

	var inferenceModel *grpc.Client
	var err error

	opts := modelOpts(c, o, []model.Option{
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
		model.WithThreads(uint32(c.Threads)), // some models uses this to allocate threads during startup
		model.WithAssetDir(o.AssetsDestination),
		model.WithModel(modelFile),
		model.WithContext(o.Context),
		model.WithExternalBackends(o.ExternalGRPCBackends, false),
	})

	if c.Backend != "" {
		opts = append(opts, model.WithBackendString(c.Backend))
	}

	// Check if the modelFile exists, if it doesn't try to load it from the gallery
	if o.AutoloadGalleries { // experimental
		if _, err := os.Stat(modelFile); os.IsNotExist(err) {
			utils.ResetDownloadTimers()
			// if we failed to load the model, we try to download it
			err := gallery.InstallModelFromGalleryByName(o.Galleries, modelFile, loader.ModelPath, gallery.GalleryModel{}, utils.DisplayDownloadFunction)
			if err != nil {
				return nil, err
			}
		}
	}

	if c.Backend == "" {
		inferenceModel, err = loader.GreedyLoader(opts...)
	} else {
		inferenceModel, err = loader.BackendLoader(opts...)
	}

	if err != nil {
		return nil, err
	}

	// in GRPC, the backend is supposed to answer to 1 single token if stream is not supported
	fn := func() (LLMResponse, error) {
		opts := gRPCPredictOpts(c, loader.ModelPath)
		opts.Prompt = s
		opts.Images = images

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
			ss := ""

			var partialRune []byte
			err := inferenceModel.PredictStream(ctx, opts, func(chars []byte) {
				partialRune = append(partialRune, chars...)

				for len(partialRune) > 0 {
					r, size := utf8.DecodeRune(partialRune)
					if r == utf8.RuneError {
						// incomplete rune, wait for more bytes
						break
					}

					tokenCallback(string(r), tokenUsage)
					ss += string(r)

					partialRune = partialRune[size:]
				}
			})
			return LLMResponse{
				Response: ss,
				Usage:    tokenUsage,
			}, err
		} else {
			// TODO: Is the chicken bit the only way to get here? is that acceptable?
			reply, err := inferenceModel.Predict(ctx, opts)
			if err != nil {
				return LLMResponse{}, err
			}
			return LLMResponse{
				Response: string(reply.Message),
				Usage:    tokenUsage,
			}, err
		}
	}

	return fn, nil
}

var cutstrings map[string]*regexp.Regexp = make(map[string]*regexp.Regexp)
var mu sync.Mutex = sync.Mutex{}

func Finetune(config schema.Config, input, prediction string) string {
	if config.Echo {
		prediction = input + prediction
	}

	for _, c := range config.Cutstrings {
		mu.Lock()
		reg, ok := cutstrings[c]
		if !ok {
			cutstrings[c] = regexp.MustCompile(c)
			reg = cutstrings[c]
		}
		mu.Unlock()
		prediction = reg.ReplaceAllString(prediction, "")
	}

	for _, c := range config.TrimSpace {
		prediction = strings.TrimSpace(strings.TrimPrefix(prediction, c))
	}
	return prediction

}

////// CONFIG AND REQUEST HANDLING ///////////////

func ReadConfigFromFileAndCombineWithOpenAIRequest(modelFile string, input *schema.OpenAIRequest, cm *services.ConfigLoader, startupOptions *schema.StartupOptions) (*schema.Config, *schema.OpenAIRequest, error) {
	// Load a config file if present after the model name
	modelConfig := filepath.Join(startupOptions.ModelPath, modelFile+".yaml")

	var cfg *schema.Config

	defaults := func() {
		cfg = schema.DefaultConfig(modelFile)
		cfg.ContextSize = startupOptions.ContextSize
		cfg.Threads = startupOptions.Threads
		cfg.F16 = startupOptions.F16
		cfg.Debug = startupOptions.Debug
	}

	cfgExisting, exists := cm.GetConfig(modelFile)
	if !exists {
		if _, err := os.Stat(modelConfig); err == nil {
			if err := cm.LoadConfig(modelConfig); err != nil {
				return nil, nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
			}
			cfgExisting, exists = cm.GetConfig(modelFile)
			if exists {
				cfg = &cfgExisting
			} else {
				defaults()
			}
		} else {
			defaults()
		}
	} else {
		cfg = &cfgExisting
	}

	// Set the parameters for the language model prediction
	schema.UpdateConfigFromOpenAIRequest(cfg, input)

	// Don't allow 0 as setting
	if cfg.Threads == 0 {
		if startupOptions.Threads != 0 {
			cfg.Threads = startupOptions.Threads
		} else {
			cfg.Threads = 4
		}
	}

	// Enforce debug flag if passed from CLI
	if startupOptions.Debug {
		cfg.Debug = true
	}

	return cfg, input, nil
}

func ComputeChoices(
	req *schema.OpenAIRequest,
	predInput string,
	config *schema.Config,
	o *schema.StartupOptions,
	loader *model.ModelLoader,
	cb func(string, *[]schema.Choice),
	tokenCallback func(string, TokenUsage) bool) ([]schema.Choice, TokenUsage, error) {
	n := req.N // number of completions to return
	result := []schema.Choice{}

	if n == 0 {
		n = 1
	}

	images := []string{}
	for _, m := range req.Messages {
		images = append(images, m.StringImages...)
	}

	// get the model function to call for the result
	predFunc, err := ModelInference(req.Context, predInput, images, loader, *config, o, tokenCallback)
	if err != nil {
		return result, TokenUsage{}, err
	}

	tokenUsage := TokenUsage{}

	for i := 0; i < n; i++ {
		prediction, err := predFunc()
		if err != nil {
			return result, TokenUsage{}, err
		}

		tokenUsage.Prompt += prediction.Usage.Prompt
		tokenUsage.Completion += prediction.Usage.Completion

		finetunedResponse := Finetune(*config, predInput, prediction.Response)
		cb(finetunedResponse, &result)

		//result = append(result, Choice{Text: prediction})

	}
	return result, tokenUsage, err
}

// TODO: No functions???? Commonize with prepareChatGenerationOpenAIRequest below?
func prepareGenerationOpenAIRequest(bindingFn TemplateConfigBindingFn, modelName string, input *schema.OpenAIRequest, cl *services.ConfigLoader, ml *model.ModelLoader, startupOptions *schema.StartupOptions) (*schema.Config, error) {
	config, input, err := ReadConfigFromFileAndCombineWithOpenAIRequest(modelName, input, cl, startupOptions)
	if err != nil {
		return nil, fmt.Errorf("failed reading parameters from request:%w", err)
	}

	if input.ResponseFormat.Type == "json_object" {
		input.Grammar = grammar.JSONBNF
	}

	log.Debug().Msgf("Parameter Config: %+v", config)

	configTemplate := bindingFn(config)

	// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
	if (*configTemplate == "") && (ml.ExistsInModelPath(fmt.Sprintf("%s.tmpl", config.Model))) {
		*configTemplate = config.Model
	}
	if *configTemplate == "" {
		return nil, fmt.Errorf(("failed to find templateConfig"))
	}

	return config, nil
}

////////// SPECIFIC REQUESTS //////////////
// TODO: For round one of the refactor, give each of the three primary text endpoints their own function?
// SEMITODO: During a merge, edit/completion were semi-combined - but remain nominally split
// Can cleanup into a common form later if possible easier if they are all here for now
// If they remain different, extract each of these named segments to a seperate file

func prepareChatGenerationOpenAIRequest(modelName string, input *schema.OpenAIRequest, cl *services.ConfigLoader, ml *model.ModelLoader, startupOptions *schema.StartupOptions) (*schema.Config, string, bool, error) {

	// IMPORTANT DEFS
	funcs := grammar.Functions{}

	// The Basic Begining

	config, input, err := ReadConfigFromFileAndCombineWithOpenAIRequest(modelName, input, cl, startupOptions)
	if err != nil {
		return nil, "", false, fmt.Errorf("failed reading parameters from request:%w", err)
	}
	log.Debug().Msgf("Configuration read: %+v", config)

	// Special Input/Config Handling

	// Allow the user to set custom actions via config file
	// to be "embedded" in each model - but if they are missing, use defaults.
	if config.FunctionsConfig.NoActionFunctionName == "" {
		config.FunctionsConfig.NoActionFunctionName = DEFAULT_NO_ACTION_NAME
	}
	if config.FunctionsConfig.NoActionDescriptionName == "" {
		config.FunctionsConfig.NoActionDescriptionName = DEFAULT_NO_ACTION_DESCRIPTION
	}

	if input.ResponseFormat.Type == "json_object" {
		input.Grammar = grammar.JSONBNF
	}

	processFunctions := len(input.Functions) > 0 && config.ShouldUseFunctions()

	if processFunctions {
		log.Debug().Msgf("Response needs to process functions")

		noActionGrammar := grammar.Function{
			Name:        config.FunctionsConfig.NoActionFunctionName,
			Description: config.FunctionsConfig.NoActionDescriptionName,
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

	templateFile := ""

	// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
	if ml.ExistsInModelPath(fmt.Sprintf("%s.tmpl", config.Model)) {
		templateFile = config.Model
	}

	if config.TemplateConfig.Chat != "" && !processFunctions {
		templateFile = config.TemplateConfig.Chat
	}

	if config.TemplateConfig.Functions != "" && processFunctions {
		templateFile = config.TemplateConfig.Functions
	}

	if templateFile != "" {
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
	}

	log.Debug().Msgf("Prompt (after templating): %s", predInput)
	if processFunctions {
		log.Debug().Msgf("Grammar: %+v", config.Grammar)
	}

	return config, predInput, processFunctions, nil

}

func EditGenerationOpenAIRequest(modelName string, input *schema.OpenAIRequest, cl *services.ConfigLoader, ml *model.ModelLoader, startupOptions *schema.StartupOptions) (*schema.OpenAIResponse, error) {
	id := uuid.New().String()
	created := int(time.Now().Unix())

	binding := func(config *schema.Config) *string {
		return &config.TemplateConfig.Edit
	}

	config, err := prepareGenerationOpenAIRequest(binding, modelName, input, cl, ml, startupOptions)
	if err != nil {
		return nil, err
	}

	var result []schema.Choice
	totalTokenUsage := TokenUsage{}

	for _, i := range config.InputStrings {
		// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
		templatedInput, err := ml.EvaluateTemplateForPrompt(model.EditPromptTemplate, config.TemplateConfig.Edit, model.PromptTemplateData{
			Input:        i,
			Instruction:  input.Instruction,
			SystemPrompt: config.SystemPrompt,
		})
		if err == nil {
			i = templatedInput
			log.Debug().Msgf("Template found, input modified to: %s", i)
		}

		r, tokenUsage, err := ComputeChoices(input, i, config, startupOptions, ml, func(s string, c *[]schema.Choice) {
			*c = append(*c, schema.Choice{Text: s})
		}, nil)
		if err != nil {
			return nil, err
		}

		totalTokenUsage.Prompt += tokenUsage.Prompt
		totalTokenUsage.Completion += tokenUsage.Completion

		result = append(result, r...)
	}

	return &schema.OpenAIResponse{
		ID:      id,
		Created: created,
		Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
		Choices: result,
		Object:  "edit",
		Usage: schema.OpenAIUsage{
			PromptTokens:     totalTokenUsage.Prompt,
			CompletionTokens: totalTokenUsage.Completion,
			TotalTokens:      totalTokenUsage.Prompt + totalTokenUsage.Completion,
		},
	}, nil
}

func ChatGenerationOpenAIRequest(modelName string, input *schema.OpenAIRequest, cl *services.ConfigLoader, ml *model.ModelLoader, startupOptions *schema.StartupOptions) (*schema.OpenAIResponse, error) {

	// DEFS
	id := uuid.New().String()
	created := int(time.Now().Unix())

	// Prepare
	config, predInput, processFunctions, err := prepareChatGenerationOpenAIRequest(modelName, input, cl, ml, startupOptions)
	if err != nil {
		return nil, err
	}

	result, tokenUsage, err := ComputeChoices(input, predInput, config, startupOptions, ml, func(s string, c *[]schema.Choice) {
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
			if func_name == config.FunctionsConfig.NoActionFunctionName {
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
							message = Finetune(*config, predInput, message)
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
				images := []string{}
				for _, m := range input.Messages {
					images = append(images, m.StringImages...)
				}
				predFunc, err := ModelInference(input.Context, predInput, images, ml, *config, startupOptions, nil)
				if err != nil {
					log.Error().Msgf("inference error: %s", err.Error())
					return
				}

				prediction, err := predFunc()
				if err != nil {
					log.Error().Msgf("inference error: %s", err.Error())
					return
				}

				fineTunedResponse := Finetune(*config, predInput, prediction.Response)
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
		return nil, err
	}

	return &schema.OpenAIResponse{
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
	}, nil

}

func CompletionGenerationOpenAIRequest(modelName string, input *schema.OpenAIRequest, cl *services.ConfigLoader, ml *model.ModelLoader, startupOptions *schema.StartupOptions) (*schema.OpenAIResponse, error) {
	// Prepare
	id := uuid.New().String()
	created := int(time.Now().Unix())

	binding := func(config *schema.Config) *string {
		return &config.TemplateConfig.Completion
	}

	config, err := prepareGenerationOpenAIRequest(binding, modelName, input, cl, ml, startupOptions)
	if err != nil {
		return nil, err
	}

	var result []schema.Choice

	totalTokenUsage := TokenUsage{}

	for k, i := range config.PromptStrings {
		// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
		templatedInput, err := ml.EvaluateTemplateForPrompt(model.CompletionPromptTemplate, config.TemplateConfig.Completion, model.PromptTemplateData{
			SystemPrompt: config.SystemPrompt,
			Input:        i,
		})
		if err == nil {
			i = templatedInput
			log.Debug().Msgf("Template found, input modified to: %s", i)
		}

		r, tokenUsage, err := ComputeChoices(
			input, i, config, startupOptions, ml, func(s string, c *[]schema.Choice) {
				*c = append(*c, schema.Choice{Text: s, FinishReason: "stop", Index: k})
			}, nil)
		if err != nil {
			return nil, err
		}

		totalTokenUsage.Prompt += tokenUsage.Prompt
		totalTokenUsage.Completion += tokenUsage.Completion

		result = append(result, r...)
	}

	return &schema.OpenAIResponse{
		ID:      id,
		Created: created,
		Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
		Choices: result,
		Object:  "text_completion",
		Usage: schema.OpenAIUsage{
			PromptTokens:     totalTokenUsage.Prompt,
			CompletionTokens: totalTokenUsage.Completion,
			TotalTokens:      totalTokenUsage.Prompt + totalTokenUsage.Completion,
		},
	}, nil
}

func StreamingChatGenerationOpenAIRequest(modelName string, input *schema.OpenAIRequest, cl *services.ConfigLoader, ml *model.ModelLoader, startupOptions *schema.StartupOptions) (chan schema.OpenAIResponse, error) {

	// DEFS
	emptyMessage := ""
	id := uuid.New().String()
	created := int(time.Now().Unix())

	// Prepare
	config, predInput, processFunctions, err := prepareChatGenerationOpenAIRequest(modelName, input, cl, ml, startupOptions)
	if err != nil {
		return nil, err
	}

	if processFunctions {
		// TODO: unused variable means I did something wrong. investigate once stable
		log.Debug().Msgf("StreamingChatGenerationOpenAIRequest with processFunctions=true for %s?", config.Name)
	}

	processor := func(s string, req *schema.OpenAIRequest, config *schema.Config, loader *model.ModelLoader, responses chan schema.OpenAIResponse) {
		initialMessage := schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant", Content: &emptyMessage}}},
			Object:  "chat.completion.chunk",
		}
		responses <- initialMessage

		ComputeChoices(req, s, config, startupOptions, loader, func(s string, c *[]schema.Choice) {}, func(s string, usage TokenUsage) bool {
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
	log.Trace().Msg("StreamingChatGenerationOpenAIRequest :: About to create response channel")

	responses := make(chan schema.OpenAIResponse)

	log.Trace().Msg("StreamingChatGenerationOpenAIRequest :: About to start processor goroutine")

	go processor(predInput, input, config, ml, responses)

	log.Trace().Msg("StreamingChatGenerationOpenAIRequest :: DONE! successfully returning to caller!")

	return responses, nil

}

func StreamingCompletionGenerationOpenAIRequest(modelName string, input *schema.OpenAIRequest, cl *services.ConfigLoader, ml *model.ModelLoader, startupOptions *schema.StartupOptions) (chan schema.OpenAIResponse, error) {
	// DEFS
	id := uuid.New().String()
	created := int(time.Now().Unix())

	binding := func(config *schema.Config) *string {
		return &config.TemplateConfig.Completion
	}

	// Prepare

	config, err := prepareGenerationOpenAIRequest(binding, modelName, input, cl, ml, startupOptions)
	if err != nil {
		return nil, err
	}

	processor := func(s string, req *schema.OpenAIRequest, config *schema.Config, loader *model.ModelLoader, responses chan schema.OpenAIResponse) {
		ComputeChoices(req, s, config, startupOptions, loader, func(s string, c *[]schema.Choice) {}, func(s string, usage TokenUsage) bool {
			resp := schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{
					{
						Index: 0,
						Text:  s,
					},
				},
				Object: "text_completion",
				Usage: schema.OpenAIUsage{
					PromptTokens:     usage.Prompt,
					CompletionTokens: usage.Completion,
					TotalTokens:      usage.Prompt + usage.Completion,
				},
			}
			log.Debug().Msgf("Sending goroutine: %s", s)

			responses <- resp
			return true
		})
		close(responses)
	}

	if len(config.PromptStrings) > 1 {
		return nil, errors.New("cannot handle more than 1 `PromptStrings` when Streaming")

	}

	predInput := config.PromptStrings[0]

	//A model can have a "file.bin.tmpl" file associated with a prompt template prefix
	templatedInput, err := ml.EvaluateTemplateForPrompt(model.CompletionPromptTemplate, config.TemplateConfig.Completion, model.PromptTemplateData{
		Input: predInput,
	})
	if err == nil {
		predInput = templatedInput
		log.Debug().Msgf("Template found, input modified to: %s", predInput)
	}

	log.Trace().Msg("StreamingCompletionGenerationOpenAIRequest :: About to create response channel")

	responses := make(chan schema.OpenAIResponse)

	log.Trace().Msg("StreamingCompletionGenerationOpenAIRequest :: About to start processor goroutine")

	go processor(predInput, input, config, ml, responses)

	log.Trace().Msg("StreamingCompletionGenerationOpenAIRequest :: DONE! successfully returning to caller!")

	return responses, nil
}
