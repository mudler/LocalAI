package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	model "github.com/go-skynet/LocalAI/pkg/model"
	gpt2 "github.com/go-skynet/go-gpt2.cpp"
	gptj "github.com/go-skynet/go-gpt4all-j.cpp"
	llama "github.com/go-skynet/go-llama.cpp"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// APIError provides error information returned by the OpenAI API.
type APIError struct {
	Code    any     `json:"code,omitempty"`
	Message string  `json:"message"`
	Param   *string `json:"param,omitempty"`
	Type    string  `json:"type"`
}

type ErrorResponse struct {
	Error *APIError `json:"error,omitempty"`
}

type OpenAIResponse struct {
	Created int      `json:"created,omitempty"`
	Object  string   `json:"chat.completion,omitempty"`
	ID      string   `json:"id,omitempty"`
	Model   string   `json:"model,omitempty"`
	Choices []Choice `json:"choices,omitempty"`
}

type Choice struct {
	Index        int      `json:"index,omitempty"`
	FinishReason string   `json:"finish_reason,omitempty"`
	Message      *Message `json:"message,omitempty"`
	Text         string   `json:"text,omitempty"`
}

type Message struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type OpenAIModel struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

type OpenAIRequest struct {
	Model string `json:"model"`

	// Prompt is read only by completion API calls
	Prompt string `json:"prompt"`

	Stop string `json:"stop"`

	// Messages is read only by chat/completion API calls
	Messages []Message `json:"messages"`

	Echo bool `json:"echo"`
	// Common options between all the API calls
	TopP        float64 `json:"top_p"`
	TopK        int     `json:"top_k"`
	Temperature float64 `json:"temperature"`
	Maxtokens   int     `json:"max_tokens"`

	N int `json:"n"`

	// Custom parameters - not present in the OpenAI API
	Batch         int     `json:"batch"`
	F16           bool    `json:"f16kv"`
	IgnoreEOS     bool    `json:"ignore_eos"`
	RepeatPenalty float64 `json:"repeat_penalty"`
	Keep          int     `json:"n_keep"`

	Seed int `json:"seed"`
}

// https://platform.openai.com/docs/api-reference/completions
func openAIEndpoint(cm ConfigMerger, chat, debug bool, loader *model.ModelLoader, threads, ctx int, f16 bool, mutexMap *sync.Mutex, mutexes map[string]*sync.Mutex) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		var err error
		var model *llama.LLama
		var gptModel *gptj.GPTJ
		var gpt2Model *gpt2.GPT2
		var stableLMModel *gpt2.StableLM

		input := new(OpenAIRequest)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}
		modelFile := input.Model
		received, _ := json.Marshal(input)

		log.Debug().Msgf("Request received: %s", string(received))

		// Set model from bearer token, if available
		bearer := strings.TrimLeft(c.Get("authorization"), "Bearer ")
		bearerExists := bearer != "" && loader.ExistsInModelPath(bearer)

		// If no model was specified, take the first available
		if modelFile == "" {
			models, _ := loader.ListModels()
			if len(models) > 0 {
				modelFile = models[0]
				log.Debug().Msgf("No model specified, using: %s", modelFile)
			}
		}

		// If no model is found or specified, we bail out
		if modelFile == "" && !bearerExists {
			return fmt.Errorf("no model specified")
		}

		// If a model is found in bearer token takes precedence
		if bearerExists {
			log.Debug().Msgf("Using model from bearer token: %s", bearer)
			modelFile = bearer
		}

		// Set the parameters for the language model prediction

		// Load a config file if present after the model name
		modelConfig := filepath.Join(loader.ModelPath, modelFile+".yaml")
		if _, err := os.Stat(modelConfig); err == nil {
			if err := cm.LoadConfig(modelConfig); err != nil {
				return fmt.Errorf("failed loading model config %s", err.Error())
			}
		}

		var config *Config
		cfg, exists := cm[modelFile]
		if !exists {
			config = &Config{
				OpenAIRequest: OpenAIRequest{
					TopP:        0.7,
					TopK:        80,
					Maxtokens:   512,
					Temperature: 0.9,
					Model:       modelFile,
				},
			}
		} else {
			config = &cfg
		}
		if input.Echo {
			config.Echo = input.Echo
		}
		if input.TopK != 0 {
			config.TopK = input.TopK
		}
		if input.TopP != 0 {
			config.TopP = input.TopP
		}

		if input.Temperature != 0 {
			config.Temperature = input.Temperature
		}

		if input.Maxtokens != 0 {
			config.Maxtokens = input.Maxtokens
		}

		if input.Stop != "" {
			config.StopWords = append(config.StopWords, input.Stop)
		}

		if input.RepeatPenalty != 0 {
			config.RepeatPenalty = input.RepeatPenalty
		}

		if input.Keep != 0 {
			config.Keep = input.Keep
		}

		if input.Batch != 0 {
			config.Batch = input.Batch
		}

		if input.F16 {
			config.F16 = input.F16
		}

		if input.IgnoreEOS {
			config.IgnoreEOS = input.IgnoreEOS
		}

		if input.Seed != 0 {
			config.Seed = input.Seed
		}

		modelFile = config.Model

		log.Debug().Msgf("Parameter Config: %+v", config)

		// Try to load the model
		var llamaerr, gpt2err, gptjerr, stableerr error
		llamaOpts := []llama.ModelOption{}
		if ctx != 0 {
			llamaOpts = append(llamaOpts, llama.SetContext(ctx))
		}
		if f16 {
			llamaOpts = append(llamaOpts, llama.EnableF16Memory)
		}

		// TODO: this is ugly, better identifying the model somehow! however, it is a good stab for a first implementation..
		model, llamaerr = loader.LoadLLaMAModel(modelFile, llamaOpts...)
		if llamaerr != nil {
			gptModel, gptjerr = loader.LoadGPTJModel(modelFile)
			if gptjerr != nil {
				gpt2Model, gpt2err = loader.LoadGPT2Model(modelFile)
				if gpt2err != nil {
					stableLMModel, stableerr = loader.LoadStableLMModel(modelFile)
					if stableerr != nil {
						return fmt.Errorf("llama: %s gpt: %s gpt2: %s stableLM: %s", llamaerr.Error(), gptjerr.Error(), gpt2err.Error(), stableerr.Error()) // llama failed first, so we want to catch both errors
					}
				}
			}
		}

		// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
		mutexMap.Lock()
		l, ok := mutexes[modelFile]
		if !ok {
			m := &sync.Mutex{}
			mutexes[modelFile] = m
			l = m
		}
		mutexMap.Unlock()
		l.Lock()
		defer l.Unlock()

		predInput := input.Prompt
		if chat {
			mess := []string{}
			for _, i := range input.Messages {
				r := config.Roles[i.Role]
				if r == "" {
					r = i.Role
				}

				content := fmt.Sprint(r, " ", i.Content)
				mess = append(mess, content)
			}

			predInput = strings.Join(mess, "\n")
		}

		templateFile := modelFile
		if config.TemplateConfig.Chat != "" && chat {
			templateFile = config.TemplateConfig.Chat
		}

		if config.TemplateConfig.Completion != "" && !chat {
			templateFile = config.TemplateConfig.Completion
		}

		// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
		templatedInput, err := loader.TemplatePrefix(templateFile, struct {
			Input string
		}{Input: predInput})
		if err == nil {
			predInput = templatedInput
			log.Debug().Msgf("Template found, input modified to: %s", predInput)
		}

		result := []Choice{}

		n := input.N

		if input.N == 0 {
			n = 1
		}

		var predFunc func() (string, error)
		switch {
		case stableLMModel != nil:
			predFunc = func() (string, error) {
				// Generate the prediction using the language model
				predictOptions := []gpt2.PredictOption{
					gpt2.SetTemperature(config.Temperature),
					gpt2.SetTopP(config.TopP),
					gpt2.SetTopK(config.TopK),
					gpt2.SetTokens(config.Maxtokens),
					gpt2.SetThreads(threads),
				}

				if config.Batch != 0 {
					predictOptions = append(predictOptions, gpt2.SetBatch(config.Batch))
				}

				if config.Seed != 0 {
					predictOptions = append(predictOptions, gpt2.SetSeed(config.Seed))
				}

				return stableLMModel.Predict(
					predInput,
					predictOptions...,
				)
			}
		case gpt2Model != nil:
			predFunc = func() (string, error) {
				// Generate the prediction using the language model
				predictOptions := []gpt2.PredictOption{
					gpt2.SetTemperature(config.Temperature),
					gpt2.SetTopP(config.TopP),
					gpt2.SetTopK(config.TopK),
					gpt2.SetTokens(config.Maxtokens),
					gpt2.SetThreads(threads),
				}

				if config.Batch != 0 {
					predictOptions = append(predictOptions, gpt2.SetBatch(config.Batch))
				}

				if config.Seed != 0 {
					predictOptions = append(predictOptions, gpt2.SetSeed(config.Seed))
				}

				return gpt2Model.Predict(
					predInput,
					predictOptions...,
				)
			}
		case gptModel != nil:
			predFunc = func() (string, error) {
				// Generate the prediction using the language model
				predictOptions := []gptj.PredictOption{
					gptj.SetTemperature(config.Temperature),
					gptj.SetTopP(config.TopP),
					gptj.SetTopK(config.TopK),
					gptj.SetTokens(config.Maxtokens),
					gptj.SetThreads(threads),
				}

				if config.Batch != 0 {
					predictOptions = append(predictOptions, gptj.SetBatch(config.Batch))
				}

				if config.Seed != 0 {
					predictOptions = append(predictOptions, gptj.SetSeed(config.Seed))
				}

				return gptModel.Predict(
					predInput,
					predictOptions...,
				)
			}
		case model != nil:
			predFunc = func() (string, error) {
				// Generate the prediction using the language model
				predictOptions := []llama.PredictOption{
					llama.SetTemperature(config.Temperature),
					llama.SetTopP(config.TopP),
					llama.SetTopK(config.TopK),
					llama.SetTokens(config.Maxtokens),
					llama.SetThreads(threads),
				}

				if debug {
					predictOptions = append(predictOptions, llama.Debug)
				}

				predictOptions = append(predictOptions, llama.SetStopWords(config.StopWords...))

				if config.RepeatPenalty != 0 {
					predictOptions = append(predictOptions, llama.SetPenalty(config.RepeatPenalty))
				}

				if config.Keep != 0 {
					predictOptions = append(predictOptions, llama.SetNKeep(config.Keep))
				}

				if config.Batch != 0 {
					predictOptions = append(predictOptions, llama.SetBatch(config.Batch))
				}

				if config.F16 {
					predictOptions = append(predictOptions, llama.EnableF16KV)
				}

				if config.IgnoreEOS {
					predictOptions = append(predictOptions, llama.IgnoreEOS)
				}

				if config.Seed != 0 {
					predictOptions = append(predictOptions, llama.SetSeed(config.Seed))
				}

				return model.Predict(
					predInput,
					predictOptions...,
				)
			}
		}

		for i := 0; i < n; i++ {
			prediction, err := predFunc()
			if err != nil {
				return err
			}

			if config.Echo {
				prediction = predInput + prediction
			}

			for _, c := range config.Cutstrings {
				// TODO: Optimize this, no need to recompile each time
				re := regexp.MustCompile(c)
				prediction = re.ReplaceAllString(prediction, "")
			}

			for _, c := range config.TrimSpace {
				prediction = strings.TrimSpace(strings.TrimPrefix(prediction, c))
			}

			if chat {
				result = append(result, Choice{Message: &Message{Role: "assistant", Content: prediction}})
			} else {
				result = append(result, Choice{Text: prediction})
			}
		}

		jsonResult, _ := json.Marshal(result)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(OpenAIResponse{
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
		})
	}
}

func listModels(loader *model.ModelLoader) func(ctx *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		models, err := loader.ListModels()
		if err != nil {
			return err
		}

		dataModels := []OpenAIModel{}
		for _, m := range models {
			dataModels = append(dataModels, OpenAIModel{ID: m, Object: "model"})
		}
		return c.JSON(struct {
			Object string        `json:"object"`
			Data   []OpenAIModel `json:"data"`
		}{
			Object: "list",
			Data:   dataModels,
		})
	}
}

func App(configFile string, loader *model.ModelLoader, threads, ctxSize int, f16 bool, debug, disableMessage bool) *fiber.App {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	// Return errors as JSON responses
	app := fiber.New(fiber.Config{
		DisableStartupMessage: disableMessage,
		// Override default error handler
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			// Status code defaults to 500
			code := fiber.StatusInternalServerError

			// Retrieve the custom status code if it's a *fiber.Error
			var e *fiber.Error
			if errors.As(err, &e) {
				code = e.Code
			}

			// Send custom error page
			return ctx.Status(code).JSON(
				ErrorResponse{
					Error: &APIError{Message: err.Error(), Code: code},
				},
			)
		},
	})

	cm := make(ConfigMerger)
	if err := cm.LoadConfigs(loader.ModelPath); err != nil {
		log.Error().Msgf("error loading config files: %s", err.Error())
	}

	if configFile != "" {
		if err := cm.LoadConfigFile(configFile); err != nil {
			log.Error().Msgf("error loading config file: %s", err.Error())
		}
	}

	// Default middleware config
	app.Use(recover.New())
	app.Use(cors.New())

	// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
	mu := map[string]*sync.Mutex{}
	var mumutex = &sync.Mutex{}

	// openAI compatible API endpoint
	app.Post("/v1/chat/completions", openAIEndpoint(cm, true, debug, loader, threads, ctxSize, f16, mumutex, mu))
	app.Post("/chat/completions", openAIEndpoint(cm, true, debug, loader, threads, ctxSize, f16, mumutex, mu))

	app.Post("/v1/completions", openAIEndpoint(cm, false, debug, loader, threads, ctxSize, f16, mumutex, mu))
	app.Post("/completions", openAIEndpoint(cm, false, debug, loader, threads, ctxSize, f16, mumutex, mu))

	app.Get("/v1/models", listModels(loader))
	app.Get("/models", listModels(loader))

	return app
}
