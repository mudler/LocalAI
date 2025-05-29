package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/templates"
	"github.com/mudler/LocalAI/pkg/utils"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

type correlationIDKeyType string

// CorrelationIDKey to track request across process boundary
const CorrelationIDKey correlationIDKeyType = "correlationID"

type RequestExtractor struct {
	backendConfigLoader *config.BackendConfigLoader
	modelLoader         *model.ModelLoader
	applicationConfig   *config.ApplicationConfig
}

func NewRequestExtractor(backendConfigLoader *config.BackendConfigLoader, modelLoader *model.ModelLoader, applicationConfig *config.ApplicationConfig) *RequestExtractor {
	return &RequestExtractor{
		backendConfigLoader: backendConfigLoader,
		modelLoader:         modelLoader,
		applicationConfig:   applicationConfig,
	}
}

const CONTEXT_LOCALS_KEY_MODEL_NAME = "MODEL_NAME"
const CONTEXT_LOCALS_KEY_LOCALAI_REQUEST = "LOCALAI_REQUEST"
const CONTEXT_LOCALS_KEY_MODEL_CONFIG = "MODEL_CONFIG"

// TODO: Refactor to not return error if unchanged
func (re *RequestExtractor) setModelNameFromRequest(ctx *fiber.Ctx) {
	model, ok := ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
	if ok && model != "" {
		return
	}
	model = ctx.Params("model")

	if (model == "") && ctx.Query("model") != "" {
		model = ctx.Query("model")
	}

	if model == "" {
		// Set model from bearer token, if available
		bearer := strings.TrimLeft(ctx.Get("authorization"), "Bear ") // "Bearer " => "Bear" to please go-staticcheck. It looks dumb but we might as well take free performance on something called for nearly every request.
		if bearer != "" {
			exists, err := services.CheckIfModelExists(re.backendConfigLoader, re.modelLoader, bearer, services.ALWAYS_INCLUDE)
			if err == nil && exists {
				model = bearer
			}
		}
	}

	ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME, model)
}

func (re *RequestExtractor) BuildConstantDefaultModelNameMiddleware(defaultModelName string) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		re.setModelNameFromRequest(ctx)
		localModelName, ok := ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
		if !ok || localModelName == "" {
			ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME, defaultModelName)
			log.Debug().Str("defaultModelName", defaultModelName).Msg("context local model name not found, setting to default")
		}
		return ctx.Next()
	}
}

func (re *RequestExtractor) BuildFilteredFirstAvailableDefaultModel(filterFn config.BackendConfigFilterFn) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		re.setModelNameFromRequest(ctx)
		localModelName := ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
		if localModelName != "" { // Don't overwrite existing values
			return ctx.Next()
		}

		modelNames, err := services.ListModels(re.backendConfigLoader, re.modelLoader, filterFn, services.SKIP_IF_CONFIGURED)
		if err != nil {
			log.Error().Err(err).Msg("non-fatal error calling ListModels during SetDefaultModelNameToFirstAvailable()")
			return ctx.Next()
		}

		if len(modelNames) == 0 {
			log.Warn().Msg("SetDefaultModelNameToFirstAvailable used with no matching models installed")
			// This is non-fatal - making it so was breaking the case of direct installation of raw models
			// return errors.New("this endpoint requires at least one model to be installed")
			return ctx.Next()
		}

		ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME, modelNames[0])
		log.Debug().Str("first model name", modelNames[0]).Msg("context local model name not found, setting to the first model")
		return ctx.Next()
	}
}

// TODO: If context and cancel above belong on all methods, move that part of above into here!
// Otherwise, it's in its own method below for now
func (re *RequestExtractor) SetModelAndConfig(initializer func() schema.LocalAIRequest) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		input := initializer()
		if input == nil {
			return fmt.Errorf("unable to initialize body")
		}
		if err := ctx.BodyParser(input); err != nil {
			return fmt.Errorf("failed parsing request body: %w", err)
		}

		// If this request doesn't have an associated model name, fetch it from earlier in the middleware chain
		if input.ModelName(nil) == "" {
			localModelName, ok := ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
			if ok && localModelName != "" {
				log.Debug().Str("context localModelName", localModelName).Msg("overriding empty model name in request body with value found earlier in middleware chain")
				input.ModelName(&localModelName)
			}
		}

		cfg, err := re.backendConfigLoader.LoadBackendConfigFileByNameDefaultOptions(input.ModelName(nil), re.applicationConfig)

		if err != nil {
			log.Err(err)
			log.Warn().Msgf("Model Configuration File not found for %q", input.ModelName(nil))
		} else if cfg.Model == "" && input.ModelName(nil) != "" {
			log.Debug().Str("input.ModelName", input.ModelName(nil)).Msg("config does not include model, using input")
			cfg.Model = input.ModelName(nil)
		}

		ctx.Locals(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
		ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

		return ctx.Next()
	}
}

func (re *RequestExtractor) SetOpenAIRequest(ctx *fiber.Ctx) error {
	input, ok := ctx.Locals(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
	if !ok || input.Model == "" {
		return fiber.ErrBadRequest
	}

	cfg, ok := ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.BackendConfig)
	if !ok || cfg == nil {
		return fiber.ErrBadRequest
	}

	// Extract or generate the correlation ID
	correlationID := ctx.Get("X-Correlation-ID", uuid.New().String())
	ctx.Set("X-Correlation-ID", correlationID)

	c1, cancel := context.WithCancel(re.applicationConfig.Context)
	// Add the correlation ID to the new context
	ctxWithCorrelationID := context.WithValue(c1, CorrelationIDKey, correlationID)

	input.Context = ctxWithCorrelationID
	input.Cancel = cancel

	err := mergeOpenAIRequestAndBackendConfig(cfg, input)
	if err != nil {
		return err
	}

	if cfg.Model == "" {
		log.Debug().Str("input.Model", input.Model).Msg("replacing empty cfg.Model with input value")
		cfg.Model = input.Model
	}

	ctx.Locals(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
	ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

	return ctx.Next()
}

func mergeOpenAIRequestAndBackendConfig(config *config.BackendConfig, input *schema.OpenAIRequest) error {
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

	if input.NegativePromptScale != 0 {
		config.NegativePromptScale = input.NegativePromptScale
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
				case "audio_url", "audio", "input_audio":
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

	log.Debug().Str("input.Input", fmt.Sprintf("%+v", input.Input))

	switch inputs := input.Input.(type) {
	case string:
		if inputs != "" {
			config.InputStrings = append(config.InputStrings, inputs)
		}
	case []any:
		for _, pp := range inputs {
			switch i := pp.(type) {
			case string:
				config.InputStrings = append(config.InputStrings, i)
			case []any:
				tokens := []int{}
				inputStrings := []string{}
				for _, ii := range i {
					switch ii := ii.(type) {
					case int:
						tokens = append(tokens, ii)
					case float64:
						tokens = append(tokens, int(ii))
					case string:
						inputStrings = append(inputStrings, ii)
					default:
						log.Error().Msgf("Unknown input type: %T", ii)
					}
				}
				config.InputToken = append(config.InputToken, tokens)
				config.InputStrings = append(config.InputStrings, inputStrings...)
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

	// If a quality was defined as number, convert it to step
	if input.Quality != "" {
		q, err := strconv.Atoi(input.Quality)
		if err == nil {
			config.Step = q
		}
	}

	if config.Validate() {
		return nil
	}
	return fmt.Errorf("unable to validate configuration after merging")
}
