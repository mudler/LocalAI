package api

import (
	"encoding/json"
	"errors"
	"fmt"
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
func openAIEndpoint(chat, debug bool, loader *model.ModelLoader, threads, ctx int, f16 bool, mutexMap *sync.Mutex, mutexes map[string]*sync.Mutex) func(c *fiber.Ctx) error {
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

		// Set the parameters for the language model prediction
		topP := input.TopP
		if topP == 0 {
			topP = 0.7
		}
		topK := input.TopK
		if topK == 0 {
			topK = 80
		}

		temperature := input.Temperature
		if temperature == 0 {
			temperature = 0.9
		}

		tokens := input.Maxtokens
		if tokens == 0 {
			tokens = 512
		}

		predInput := input.Prompt
		if chat {
			mess := []string{}
			// TODO: encode roles
			for _, i := range input.Messages {
				mess = append(mess, i.Content)
			}

			predInput = strings.Join(mess, "\n")
		}

		// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
		templatedInput, err := loader.TemplatePrefix(modelFile, struct {
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
					gpt2.SetTemperature(temperature),
					gpt2.SetTopP(topP),
					gpt2.SetTopK(topK),
					gpt2.SetTokens(tokens),
					gpt2.SetThreads(threads),
				}

				if input.Batch != 0 {
					predictOptions = append(predictOptions, gpt2.SetBatch(input.Batch))
				}

				if input.Seed != 0 {
					predictOptions = append(predictOptions, gpt2.SetSeed(input.Seed))
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
					gpt2.SetTemperature(temperature),
					gpt2.SetTopP(topP),
					gpt2.SetTopK(topK),
					gpt2.SetTokens(tokens),
					gpt2.SetThreads(threads),
				}

				if input.Batch != 0 {
					predictOptions = append(predictOptions, gpt2.SetBatch(input.Batch))
				}

				if input.Seed != 0 {
					predictOptions = append(predictOptions, gpt2.SetSeed(input.Seed))
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
					gptj.SetTemperature(temperature),
					gptj.SetTopP(topP),
					gptj.SetTopK(topK),
					gptj.SetTokens(tokens),
					gptj.SetThreads(threads),
				}

				if input.Batch != 0 {
					predictOptions = append(predictOptions, gptj.SetBatch(input.Batch))
				}

				if input.Seed != 0 {
					predictOptions = append(predictOptions, gptj.SetSeed(input.Seed))
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
					llama.SetTemperature(temperature),
					llama.SetTopP(topP),
					llama.SetTopK(topK),
					llama.SetTokens(tokens),
					llama.SetThreads(threads),
				}

				if debug {
					predictOptions = append(predictOptions, llama.Debug)
				}

				if input.Stop != "" {
					predictOptions = append(predictOptions, llama.SetStopWords(input.Stop))
				}

				if input.RepeatPenalty != 0 {
					predictOptions = append(predictOptions, llama.SetPenalty(input.RepeatPenalty))
				}

				if input.Keep != 0 {
					predictOptions = append(predictOptions, llama.SetNKeep(input.Keep))
				}

				if input.Batch != 0 {
					predictOptions = append(predictOptions, llama.SetBatch(input.Batch))
				}

				if input.F16 {
					predictOptions = append(predictOptions, llama.EnableF16KV)
				}

				if input.IgnoreEOS {
					predictOptions = append(predictOptions, llama.IgnoreEOS)
				}

				if input.Seed != 0 {
					predictOptions = append(predictOptions, llama.SetSeed(input.Seed))
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

			if input.Echo {
				prediction = predInput + prediction
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

func App(loader *model.ModelLoader, threads, ctxSize int, f16 bool, debug, disableMessage bool) *fiber.App {
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

	// Default middleware config
	app.Use(recover.New())
	app.Use(cors.New())

	// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
	mu := map[string]*sync.Mutex{}
	var mumutex = &sync.Mutex{}

	// openAI compatible API endpoint
	app.Post("/v1/chat/completions", openAIEndpoint(true, debug, loader, threads, ctxSize, f16, mumutex, mu))
	app.Post("/chat/completions", openAIEndpoint(true, debug, loader, threads, ctxSize, f16, mumutex, mu))

	app.Post("/v1/completions", openAIEndpoint(false, debug, loader, threads, ctxSize, f16, mumutex, mu))
	app.Post("/completions", openAIEndpoint(false, debug, loader, threads, ctxSize, f16, mumutex, mu))

	app.Get("/v1/models", listModels(loader))
	app.Get("/models", listModels(loader))

	return app
}
