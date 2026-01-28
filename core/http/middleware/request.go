package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

type correlationIDKeyType string

// CorrelationIDKey to track request across process boundary
const CorrelationIDKey correlationIDKeyType = "correlationID"

type RequestExtractor struct {
	modelConfigLoader *config.ModelConfigLoader
	modelLoader       *model.ModelLoader
	applicationConfig *config.ApplicationConfig
}

func NewRequestExtractor(modelConfigLoader *config.ModelConfigLoader, modelLoader *model.ModelLoader, applicationConfig *config.ApplicationConfig) *RequestExtractor {
	return &RequestExtractor{
		modelConfigLoader: modelConfigLoader,
		modelLoader:       modelLoader,
		applicationConfig: applicationConfig,
	}
}

const CONTEXT_LOCALS_KEY_MODEL_NAME = "MODEL_NAME"
const CONTEXT_LOCALS_KEY_LOCALAI_REQUEST = "LOCALAI_REQUEST"
const CONTEXT_LOCALS_KEY_MODEL_CONFIG = "MODEL_CONFIG"

// TODO: Refactor to not return error if unchanged
func (re *RequestExtractor) setModelNameFromRequest(c echo.Context) {
	model, ok := c.Get(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
	if ok && model != "" {
		return
	}
	model = c.Param("model")

	if model == "" {
		model = c.QueryParam("model")
	}

	// Check FormValue for multipart/form-data requests (e.g., /v1/images/inpainting)
	if model == "" {
		model = c.FormValue("model")
	}

	if model == "" {
		// Set model from bearer token, if available
		auth := c.Request().Header.Get("Authorization")
		bearer := strings.TrimPrefix(auth, "Bearer ")
		if bearer != "" && bearer != auth {
			exists, err := services.CheckIfModelExists(re.modelConfigLoader, re.modelLoader, bearer, services.ALWAYS_INCLUDE)
			if err == nil && exists {
				model = bearer
			}
		}
	}

	c.Set(CONTEXT_LOCALS_KEY_MODEL_NAME, model)
}

func (re *RequestExtractor) BuildConstantDefaultModelNameMiddleware(defaultModelName string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			re.setModelNameFromRequest(c)
			localModelName, ok := c.Get(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
			if !ok || localModelName == "" {
				c.Set(CONTEXT_LOCALS_KEY_MODEL_NAME, defaultModelName)
				xlog.Debug("context local model name not found, setting to default", "defaultModelName", defaultModelName)
			}
			return next(c)
		}
	}
}

func (re *RequestExtractor) BuildFilteredFirstAvailableDefaultModel(filterFn config.ModelConfigFilterFn) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			re.setModelNameFromRequest(c)
			localModelName := c.Get(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
			if localModelName != "" { // Don't overwrite existing values
				return next(c)
			}

			modelNames, err := services.ListModels(re.modelConfigLoader, re.modelLoader, filterFn, services.SKIP_IF_CONFIGURED)
			if err != nil {
				xlog.Error("non-fatal error calling ListModels during SetDefaultModelNameToFirstAvailable()", "error", err)
				return next(c)
			}

			if len(modelNames) == 0 {
				xlog.Warn("SetDefaultModelNameToFirstAvailable used with no matching models installed")
				// This is non-fatal - making it so was breaking the case of direct installation of raw models
				// return errors.New("this endpoint requires at least one model to be installed")
				return next(c)
			}

			c.Set(CONTEXT_LOCALS_KEY_MODEL_NAME, modelNames[0])
			xlog.Debug("context local model name not found, setting to the first model", "first model name", modelNames[0])
			return next(c)
		}
	}
}

// TODO: If context and cancel above belong on all methods, move that part of above into here!
// Otherwise, it's in its own method below for now
func (re *RequestExtractor) SetModelAndConfig(initializer func() schema.LocalAIRequest) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			input := initializer()
			if input == nil {
				return echo.NewHTTPError(http.StatusBadRequest, "unable to initialize body")
			}
			if err := c.Bind(input); err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("failed parsing request body: %v", err))
			}

			// If this request doesn't have an associated model name, fetch it from earlier in the middleware chain
			if input.ModelName(nil) == "" {
				localModelName, ok := c.Get(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
				if ok && localModelName != "" {
					xlog.Debug("overriding empty model name in request body with value found earlier in middleware chain", "context localModelName", localModelName)
					input.ModelName(&localModelName)
				}
			}

			cfg, err := re.modelConfigLoader.LoadModelConfigFileByNameDefaultOptions(input.ModelName(nil), re.applicationConfig)

			if err != nil {
				xlog.Warn("Model Configuration File not found", "model", input.ModelName(nil), "error", err)
			} else if cfg.Model == "" && input.ModelName(nil) != "" {
				xlog.Debug("config does not include model, using input", "input.ModelName", input.ModelName(nil))
				cfg.Model = input.ModelName(nil)
			}

			c.Set(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
			c.Set(CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

			return next(c)
		}
	}
}

func (re *RequestExtractor) SetOpenAIRequest(c echo.Context) error {
	input, ok := c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
	if !ok || input.Model == "" {
		return echo.ErrBadRequest
	}

	cfg, ok := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
	if !ok || cfg == nil {
		return echo.ErrBadRequest
	}

	// Extract or generate the correlation ID
	correlationID := c.Request().Header.Get("X-Correlation-ID")
	if correlationID == "" {
		correlationID = uuid.New().String()
	}
	c.Response().Header().Set("X-Correlation-ID", correlationID)

	// Use the request context directly - Echo properly supports context cancellation!
	// No need for workarounds like handleConnectionCancellation
	reqCtx := c.Request().Context()
	c1, cancel := context.WithCancel(re.applicationConfig.Context)

	// Cancel when request context is cancelled (client disconnects)
	go func() {
		select {
		case <-reqCtx.Done():
			cancel()
		case <-c1.Done():
			// Already cancelled
		}
	}()

	// Add the correlation ID to the new context
	ctxWithCorrelationID := context.WithValue(c1, CorrelationIDKey, correlationID)

	input.Context = ctxWithCorrelationID
	input.Cancel = cancel

	err := mergeOpenAIRequestAndModelConfig(cfg, input)
	if err != nil {
		return err
	}

	if cfg.Model == "" {
		xlog.Debug("replacing empty cfg.Model with input value", "input.Model", input.Model)
		cfg.Model = input.Model
	}

	c.Set(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
	c.Set(CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

	return nil
}

func mergeOpenAIRequestAndModelConfig(config *config.ModelConfig, input *schema.OpenAIRequest) error {
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
						xlog.Error("Failed encoding video", "error", err)
						continue CONTENT
					}
					input.Messages[i].StringVideos = append(input.Messages[i].StringVideos, base64) // TODO: make sure that we only return base64 stuff
					vidIndex++
					nrOfVideosInMessage++
				case "audio_url", "audio":
					// Decode content as base64 either if it's an URL or base64 text
					base64, err := utils.GetContentURIAsBase64(pp.AudioURL.URL)
					if err != nil {
						xlog.Error("Failed encoding audio", "error", err)
						continue CONTENT
					}
					input.Messages[i].StringAudios = append(input.Messages[i].StringAudios, base64) // TODO: make sure that we only return base64 stuff
					audioIndex++
					nrOfAudiosInMessage++
				case "input_audio":
					// TODO: make sure that we only return base64 stuff
					input.Messages[i].StringAudios = append(input.Messages[i].StringAudios, pp.InputAudio.Data)
					audioIndex++
					nrOfAudiosInMessage++
				case "image_url", "image":
					// Decode content as base64 either if it's an URL or base64 text
					base64, err := utils.GetContentURIAsBase64(pp.ImageURL.URL)
					if err != nil {
						xlog.Error("Failed encoding image", "error", err)
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

	xlog.Debug("input.Input", "input", fmt.Sprintf("%+v", input.Input))

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
						xlog.Error("Unknown input type", "type", fmt.Sprintf("%T", ii))
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

	if valid, _ := config.Validate(); valid {
		return nil
	}
	return fmt.Errorf("unable to validate configuration after merging")
}

func (re *RequestExtractor) SetOpenResponsesRequest(c echo.Context) error {
	input, ok := c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenResponsesRequest)
	if !ok || input.Model == "" {
		return echo.ErrBadRequest
	}

	// Convert input items to Messages (this will be done in the endpoint handler)
	// We store the input in the request for the endpoint to process
	cfg, ok := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
	if !ok || cfg == nil {
		return echo.ErrBadRequest
	}

	// Extract or generate the correlation ID (Open Responses uses x-request-id)
	correlationID := c.Request().Header.Get("x-request-id")
	if correlationID == "" {
		correlationID = uuid.New().String()
	}
	c.Response().Header().Set("x-request-id", correlationID)

	// Use the request context directly - Echo properly supports context cancellation!
	reqCtx := c.Request().Context()
	c1, cancel := context.WithCancel(re.applicationConfig.Context)

	// Cancel when request context is cancelled (client disconnects)
	go func() {
		select {
		case <-reqCtx.Done():
			cancel()
		case <-c1.Done():
			// Already cancelled
		}
	}()

	// Add the correlation ID to the new context
	ctxWithCorrelationID := context.WithValue(c1, CorrelationIDKey, correlationID)

	input.Context = ctxWithCorrelationID
	input.Cancel = cancel

	err := mergeOpenResponsesRequestAndModelConfig(cfg, input)
	if err != nil {
		return err
	}

	if cfg.Model == "" {
		xlog.Debug("replacing empty cfg.Model with input value", "input.Model", input.Model)
		cfg.Model = input.Model
	}

	c.Set(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
	c.Set(CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

	return nil
}

func mergeOpenResponsesRequestAndModelConfig(config *config.ModelConfig, input *schema.OpenResponsesRequest) error {
	// Temperature
	if input.Temperature != nil {
		config.Temperature = input.Temperature
	}

	// TopP
	if input.TopP != nil {
		config.TopP = input.TopP
	}

	// MaxOutputTokens -> Maxtokens
	if input.MaxOutputTokens != nil {
		config.Maxtokens = input.MaxOutputTokens
	}

	// Convert tools to functions - this will be handled in the endpoint handler
	// We just validate that tools are present if needed

	// Handle tool_choice
	if input.ToolChoice != nil {
		switch tc := input.ToolChoice.(type) {
		case string:
			// "auto", "required", or "none"
			if tc == "required" {
				config.SetFunctionCallString("required")
			} else if tc == "none" {
				// Don't use tools - handled in endpoint
			}
			// "auto" is default - let model decide
		case map[string]interface{}:
			// Specific tool: {type:"function", name:"..."}
			if tcType, ok := tc["type"].(string); ok && tcType == "function" {
				if name, ok := tc["name"].(string); ok {
					config.SetFunctionCallString(name)
				}
			}
		}
	}

	if valid, _ := config.Validate(); valid {
		return nil
	}
	return fmt.Errorf("unable to validate configuration after merging")
}
