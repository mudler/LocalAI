package openai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	fiberContext "github.com/mudler/LocalAI/core/http/ctx"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/templates"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

type correlationIDKeyType string

// CorrelationIDKey to track request across process boundary
const CorrelationIDKey correlationIDKeyType = "correlationID"

func readRequest(c *fiber.Ctx, cl *config.BackendConfigLoader, ml *model.ModelLoader, o *config.ApplicationConfig, firstModel bool) (string, *schema.OpenAIRequest, error) {
	input := new(schema.OpenAIRequest)

	// Get input data from the request body
	if err := c.BodyParser(input); err != nil {
		return "", nil, fmt.Errorf("failed parsing request body: %w", err)
	}

	received, _ := json.Marshal(input)
	// Extract or generate the correlation ID
	correlationID := c.Get("X-Correlation-ID", uuid.New().String())

	ctx, cancel := context.WithCancel(o.Context)
	// Add the correlation ID to the new context
	ctxWithCorrelationID := context.WithValue(ctx, CorrelationIDKey, correlationID)

	input.Context = ctxWithCorrelationID
	input.Cancel = cancel

	log.Debug().Msgf("Request received: %s", string(received))

	modelFile, err := fiberContext.ModelFromContext(c, cl, ml, input.Model, firstModel)

	return modelFile, input, err
}

func readWSRequest(c *websocket.Conn, cl *config.BackendConfigLoader, ml *model.ModelLoader, o *config.ApplicationConfig, firstModel bool) (string, *schema.OpenAIRequest, error) {
	input := new(schema.OpenAIRequest)

	input.Model = c.Query("name")

	received, _ := json.Marshal(input)

	ctx, cancel := context.WithCancel(o.Context)

	input.Context = ctx
	input.Cancel = cancel

	log.Debug().Msgf("Request received: %s", string(received))

	modelFile, err := fiberContext.ModelFromContext(c, cl, ml, input.Model, firstModel)

	return modelFile, input, err
}

func updateRequestConfig(config *config.BackendConfig, input *schema.OpenAIRequest) {
	if input.Echo {
		config.Echo = input.Echo
	}
	if input.TopK != nil {
		config.TopK = input.TopK
	}
	if input.TopP != nil {
		config.TopP = input.TopP
	}

	if input.Backend != "" {
		config.Backend = input.Backend
	}

	if input.ClipSkip != 0 {
		config.Diffusers.ClipSkip = input.ClipSkip
	}

	if input.ModelBaseName != "" {
		config.AutoGPTQ.ModelBaseName = input.ModelBaseName
	}

	if input.NegativePromptScale != 0 {
		config.NegativePromptScale = input.NegativePromptScale
	}

	if input.UseFastTokenizer {
		config.UseFastTokenizer = input.UseFastTokenizer
	}

	if input.NegativePrompt != "" {
		config.NegativePrompt = input.NegativePrompt
	}

	if input.RopeFreqBase != 0 {
		config.RopeFreqBase = input.RopeFreqBase
	}

	if input.RopeFreqScale != 0 {
		config.RopeFreqScale = input.RopeFreqScale
	}

	if input.Grammar != "" {
		config.Grammar = input.Grammar
	}

	if input.Temperature != nil {
		config.Temperature = input.Temperature
	}

	if input.Maxtokens != nil {
		config.Maxtokens = input.Maxtokens
	}

	if input.ResponseFormat != nil {
		switch responseFormat := input.ResponseFormat.(type) {
		case string:
			config.ResponseFormat = responseFormat
		case map[string]interface{}:
			config.ResponseFormatMap = responseFormat
		}
	}

	switch stop := input.Stop.(type) {
	case string:
		if stop != "" {
			config.StopWords = append(config.StopWords, stop)
		}
	case []interface{}:
		for _, pp := range stop {
			if s, ok := pp.(string); ok {
				config.StopWords = append(config.StopWords, s)
			}
		}
	}

	if len(input.Tools) > 0 {
		for _, tool := range input.Tools {
			input.Functions = append(input.Functions, tool.Function)
		}
	}

	if input.ToolsChoice != nil {
		var toolChoice functions.Tool

		switch content := input.ToolsChoice.(type) {
		case string:
			_ = json.Unmarshal([]byte(content), &toolChoice)
		case map[string]interface{}:
			dat, _ := json.Marshal(content)
			_ = json.Unmarshal(dat, &toolChoice)
		}
		input.FunctionCall = map[string]interface{}{
			"name": toolChoice.Function.Name,
		}
	}

	// Decode each request's message content
	imgIndex, vidIndex, audioIndex := 0, 0, 0
	for i, m := range input.Messages {
		nrOfImgsInMessage := 0
		nrOfVideosInMessage := 0
		nrOfAudiosInMessage := 0

		switch content := m.Content.(type) {
		case string:
			input.Messages[i].StringContent = content
		case []interface{}:
			dat, _ := json.Marshal(content)
			c := []schema.Content{}
			json.Unmarshal(dat, &c)

			textContent := ""
			// we will template this at the end

		CONTENT:
			for _, pp := range c {
				switch pp.Type {
				case "text":
					textContent += pp.Text
					//input.Messages[i].StringContent = pp.Text
				case "video", "video_url":
					// Decode content as base64 either if it's an URL or base64 text
					base64, err := utils.GetContentURIAsBase64(pp.VideoURL.URL)
					if err != nil {
						log.Error().Msgf("Failed encoding video: %s", err)
						continue CONTENT
					}
					input.Messages[i].StringVideos = append(input.Messages[i].StringVideos, base64) // TODO: make sure that we only return base64 stuff
					vidIndex++
					nrOfVideosInMessage++
				case "audio_url", "audio":
					// Decode content as base64 either if it's an URL or base64 text
					base64, err := utils.GetContentURIAsBase64(pp.AudioURL.URL)
					if err != nil {
						log.Error().Msgf("Failed encoding image: %s", err)
						continue CONTENT
					}
					input.Messages[i].StringAudios = append(input.Messages[i].StringAudios, base64) // TODO: make sure that we only return base64 stuff
					audioIndex++
					nrOfAudiosInMessage++
				case "image_url", "image":
					// Decode content as base64 either if it's an URL or base64 text
					base64, err := utils.GetContentURIAsBase64(pp.ImageURL.URL)
					if err != nil {
						log.Error().Msgf("Failed encoding image: %s", err)
						continue CONTENT
					}

					input.Messages[i].StringImages = append(input.Messages[i].StringImages, base64) // TODO: make sure that we only return base64 stuff

					imgIndex++
					nrOfImgsInMessage++
				}
			}

			input.Messages[i].StringContent, _ = templates.TemplateMultiModal(config.TemplateConfig.Multimodal, templates.MultiModalOptions{
				TotalImages:     imgIndex,
				TotalVideos:     vidIndex,
				TotalAudios:     audioIndex,
				ImagesInMessage: nrOfImgsInMessage,
				VideosInMessage: nrOfVideosInMessage,
				AudiosInMessage: nrOfAudiosInMessage,
			}, textContent)
		}
	}

	if input.RepeatPenalty != 0 {
		config.RepeatPenalty = input.RepeatPenalty
	}

	if input.FrequencyPenalty != 0 {
		config.FrequencyPenalty = input.FrequencyPenalty
	}

	if input.PresencePenalty != 0 {
		config.PresencePenalty = input.PresencePenalty
	}

	if input.Keep != 0 {
		config.Keep = input.Keep
	}

	if input.Batch != 0 {
		config.Batch = input.Batch
	}

	if input.IgnoreEOS {
		config.IgnoreEOS = input.IgnoreEOS
	}

	if input.Seed != nil {
		config.Seed = input.Seed
	}

	if input.TypicalP != nil {
		config.TypicalP = input.TypicalP
	}

	switch inputs := input.Input.(type) {
	case string:
		if inputs != "" {
			config.InputStrings = append(config.InputStrings, inputs)
		}
	case []interface{}:
		for _, pp := range inputs {
			switch i := pp.(type) {
			case string:
				config.InputStrings = append(config.InputStrings, i)
			case []interface{}:
				tokens := []int{}
				for _, ii := range i {
					tokens = append(tokens, int(ii.(float64)))
				}
				config.InputToken = append(config.InputToken, tokens)
			}
		}
	}

	// Can be either a string or an object
	switch fnc := input.FunctionCall.(type) {
	case string:
		if fnc != "" {
			config.SetFunctionCallString(fnc)
		}
	case map[string]interface{}:
		var name string
		n, exists := fnc["name"]
		if exists {
			nn, e := n.(string)
			if e {
				name = nn
			}
		}
		config.SetFunctionCallNameString(name)
	}

	switch p := input.Prompt.(type) {
	case string:
		config.PromptStrings = append(config.PromptStrings, p)
	case []interface{}:
		for _, pp := range p {
			if s, ok := pp.(string); ok {
				config.PromptStrings = append(config.PromptStrings, s)
			}
		}
	}
}

func mergeRequestWithConfig(modelFile string, input *schema.OpenAIRequest, cm *config.BackendConfigLoader, loader *model.ModelLoader, debug bool, threads, ctx int, f16 bool) (*config.BackendConfig, *schema.OpenAIRequest, error) {
	cfg, err := cm.LoadBackendConfigFileByName(modelFile, loader.ModelPath,
		config.LoadOptionDebug(debug),
		config.LoadOptionThreads(threads),
		config.LoadOptionContextSize(ctx),
		config.LoadOptionF16(f16),
	)

	// Set the parameters for the language model prediction
	updateRequestConfig(cfg, input)

	if !cfg.Validate() {
		return nil, nil, fmt.Errorf("failed to validate config")
	}

	return cfg, input, err
}
