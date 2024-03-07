package services

import (
	"fmt"
	"sync"
	"time"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/grammar"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/google/uuid"
	"github.com/imdario/mergo"
	"github.com/rs/zerolog/log"
)

type endpointGenerationConfigurationFn func(bc *config.BackendConfig, request *schema.OpenAIRequest) (schemaObject string, templatePath string, templateData model.PromptTemplateData, mappingFn func(resp *backend.LLMResponse, index int) schema.Choice)

// TODO: Consider alternative names for this.
// The purpose of this struct is to hold a reference to the OpenAI request context information
// This keeps things simple within core/services/openai.go and allows consumers to "see" this information if they need it
type OpenAIRequestTraceID struct {
	ID      string
	Created int
}

// This type split out from core/backend/llm.go - I'm still not _totally_ sure about this, but it seems to make sense to keep the generic LLM code from the OpenAI specific higher level functionality
type OpenAIService struct {
	bcl       *config.BackendConfigLoader
	ml        *model.ModelLoader
	appConfig *config.ApplicationConfig
	llmbs     *backend.LLMBackendService
}

func NewOpenAIService(ml *model.ModelLoader, bcl *config.BackendConfigLoader, appConfig *config.ApplicationConfig, llmbs *backend.LLMBackendService) *OpenAIService {
	return &OpenAIService{
		bcl:       bcl,
		ml:        ml,
		appConfig: appConfig,
		llmbs:     llmbs,
	}
}

// Keeping in place as a reminder to POTENTIALLY ADD MORE VALIDATION HERE???
func (oais *OpenAIService) getConfig(request *schema.OpenAIRequest) (*config.BackendConfig, *schema.OpenAIRequest, error) {
	return config.LoadBackendConfigForModelAndOpenAIRequest(request.Model, request, oais.bcl, oais.appConfig)
}

// TODO: It would be a lot less messy to make a return struct that had references to each of these channels
// INTENTIONALLY not doing that quite yet - I believe we need to let the references to unused channels die for the GC to automatically collect -- can we manually free()?
// finalResultsChannel is the primary async return path: one result for the entire request.
// promptResultsChannels is DUBIOUS. It's expected to be raw fan-out used within the function itself, but I am exposing for testing? One bundle of LLMResponseBundle per PromptString? Gets all N completions for a single prompt.
// completionsChannel is a channel that emits one *LLMResponse per generated completion, be that different prompts or N. Seems the most useful other than "entire request" Request is available to attempt tracing???
// tokensChannel is a channel that emits one *LLMResponse per generated token. Let's see what happens!
func (oais *OpenAIService) Completion(request *schema.OpenAIRequest, notifyOnPromptResult bool, notifyOnToken bool) (
	traceID OpenAIRequestTraceID, finalResultChannel <-chan utils.ErrorOr[*schema.OpenAIResponse], promptResultsChannels []<-chan utils.ErrorOr[*backend.LLMResponseBundle],
	completionsChannel <-chan utils.ErrorOr[*backend.LLMResponse], tokenChannel <-chan utils.ErrorOr[*backend.LLMResponse], err error) {

	return oais.GenerateTextFromRequest(request, func(bc *config.BackendConfig, request *schema.OpenAIRequest) (
		schemaObject string, templatePath string, templateData model.PromptTemplateData, mappingFn func(resp *backend.LLMResponse, promptIndex int) schema.Choice) {

		return "edit", bc.TemplateConfig.Completion, model.PromptTemplateData{
				SystemPrompt: bc.SystemPrompt,
			}, func(resp *backend.LLMResponse, promptIndex int) schema.Choice {
				return schema.Choice{
					Index:        promptIndex,
					FinishReason: "stop",
					Text:         resp.Response,
				}
			}
	}, notifyOnPromptResult, notifyOnToken)
}

func (oais *OpenAIService) Edit(request *schema.OpenAIRequest, notifyOnPromptResult bool, notifyOnToken bool) (
	traceID OpenAIRequestTraceID, finalResultChannel <-chan utils.ErrorOr[*schema.OpenAIResponse], promptResultsChannels []<-chan utils.ErrorOr[*backend.LLMResponseBundle],
	completionsChannel <-chan utils.ErrorOr[*backend.LLMResponse], tokenChannel <-chan utils.ErrorOr[*backend.LLMResponse], err error) {

	return oais.GenerateTextFromRequest(request, func(bc *config.BackendConfig, request *schema.OpenAIRequest) (
		schemaObject string, templatePath string, templateData model.PromptTemplateData, mappingFn func(resp *backend.LLMResponse, promptIndex int) schema.Choice) {

		return "text_completion", bc.TemplateConfig.Edit, model.PromptTemplateData{
				SystemPrompt: bc.SystemPrompt,
				Instruction:  request.Instruction,
			}, func(resp *backend.LLMResponse, promptIndex int) schema.Choice {
				return schema.Choice{
					Index:        promptIndex,
					FinishReason: "stop",
					Text:         resp.Response,
				}
			}
	}, notifyOnPromptResult, notifyOnToken)
}

func (oais *OpenAIService) GenerateTextFromRequest(request *schema.OpenAIRequest, endpointConfigFn endpointGenerationConfigurationFn, notifyOnPromptResult bool, notifyOnToken bool) (
	traceID OpenAIRequestTraceID, finalResultChannel <-chan utils.ErrorOr[*schema.OpenAIResponse], promptResultsChannels []<-chan utils.ErrorOr[*backend.LLMResponseBundle],
	completionsChannel <-chan utils.ErrorOr[*backend.LLMResponse], tokenChannel <-chan utils.ErrorOr[*backend.LLMResponse], err error) {

	traceID = OpenAIRequestTraceID{
		ID:      uuid.New().String(),
		Created: int(time.Now().Unix()),
	}

	bc, request, err := oais.getConfig(request)
	if err != nil {
		return
	}

	if request.ResponseFormat.Type == "json_object" {
		request.Grammar = grammar.JSONBNF
	}

	if request.Stream {
		if len(bc.PromptStrings) > 1 {
			log.Warn().Msg("potentially cannot handle more than 1 `PromptStrings` when Streaming?")
			// return nil, fmt.Errorf("cannot handle more than 1 `PromptStrings` when Streaming")
		}
		// bc.PromptStrings = bc.PromptStrings[:1] // ?
	}

	rawFinalResultChannel := make(chan utils.ErrorOr[*schema.OpenAIResponse])
	promptResultsChannels = []<-chan utils.ErrorOr[*backend.LLMResponseBundle]{}
	var rawCompletionsChannel chan utils.ErrorOr[*backend.LLMResponse]
	var rawTokenChannel chan utils.ErrorOr[*backend.LLMResponse]
	if notifyOnPromptResult {
		rawCompletionsChannel = make(chan utils.ErrorOr[*backend.LLMResponse])
	}
	if notifyOnToken {
		rawTokenChannel = make(chan utils.ErrorOr[*backend.LLMResponse])
	}

	promptResultsChannelLock := sync.Mutex{}

	schemaObject, templateFile, commonPromptData, mappingFn := endpointConfigFn(bc, request)

	if len(templateFile) == 0 {
		// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
		if oais.ml.ExistsInModelPath(fmt.Sprintf("%s.tmpl", bc.Model)) {
			templateFile = bc.Model
		} else {
			log.Warn().Msgf("failed to find any template for %+v", request)
		}
	}

	setupWG := sync.WaitGroup{}
	setupWG.Add(len(bc.PromptStrings))

	for pI, p := range bc.PromptStrings {

		go func(promptIndex int, prompt string) {
			if templateFile != "" {
				promptTemplateData := model.PromptTemplateData{
					Input: prompt,
				}
				err := mergo.Merge(promptTemplateData, commonPromptData, mergo.WithOverride)
				if err == nil {
					templatedInput, err := oais.ml.EvaluateTemplateForPrompt(model.CompletionPromptTemplate, templateFile, promptTemplateData)
					if err == nil {
						prompt = templatedInput
						log.Debug().Msgf("Template found, input modified to: %s", prompt)
					}
				}
			}

			promptResultsChannel, completionChannels, tokenChannels, err := oais.llmbs.GenerateText(prompt, request, bc, func(r *backend.LLMResponse) schema.Choice { return mappingFn(r, promptIndex) }, false, notifyOnToken)
			if err != nil {
				log.Error().Msgf("TODO DEBUG IF HIT:\nprompt: %q\nerr: %q", prompt, err)
				return
			}
			if notifyOnPromptResult {
				utils.SliceOfChannelsRawMergerWithoutMapping(completionChannels, rawCompletionsChannel)
			}
			if notifyOnToken {
				utils.SliceOfChannelsRawMergerWithoutMapping(tokenChannels, rawTokenChannel)
			}
			promptResultsChannelLock.Lock()
			promptResultsChannels = append(promptResultsChannels, promptResultsChannel)
			promptResultsChannelLock.Unlock()
			setupWG.Done()
		}(pI, p)

	}
	setupWG.Wait()
	log.Debug().Msgf("OpenAIService::GenerateTextFromRequest: number of promptResultChannels: %d", len(promptResultsChannels))

	initialResponse := &schema.OpenAIResponse{
		ID:      traceID.ID,
		Created: traceID.Created,
		Model:   request.Model,
		Object:  schemaObject,
		Usage:   schema.OpenAIUsage{},
	}

	// utils.SliceOfChannelsRawMerger[[]schema.Choice](promptResultsChannels, rawFinalResultChannel, func(results []schema.Choice) (*schema.OpenAIResponse, error) {
	utils.SliceOfChannelsReducer[utils.ErrorOr[*backend.LLMResponseBundle], utils.ErrorOr[*schema.OpenAIResponse]](
		promptResultsChannels, rawFinalResultChannel,
		func(iv utils.ErrorOr[*backend.LLMResponseBundle], result utils.ErrorOr[*schema.OpenAIResponse]) utils.ErrorOr[*schema.OpenAIResponse] {

			if iv.Error != nil {
				result.Error = iv.Error
				return result
			}
			result.Value.Usage.PromptTokens += iv.Value.Usage.Prompt
			result.Value.Usage.CompletionTokens += iv.Value.Usage.Completion
			result.Value.Usage.TotalTokens = result.Value.Usage.PromptTokens + result.Value.Usage.CompletionTokens

			result.Value.Choices = append(result.Value.Choices, iv.Value.Response...)

			return result
		}, utils.ErrorOr[*schema.OpenAIResponse]{Value: initialResponse})

	finalResultChannel = rawFinalResultChannel
	completionsChannel = rawCompletionsChannel
	tokenChannel = rawTokenChannel

	return
}
