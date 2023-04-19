package api

import (
	"fmt"
	"strings"
	"sync"

	model "github.com/go-skynet/LocalAI/pkg/model"
	gptj "github.com/go-skynet/go-gpt4all-j.cpp"
	llama "github.com/go-skynet/go-llama.cpp"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

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
	Batch     int  `json:"batch"`
	F16       bool `json:"f16kv"`
	IgnoreEOS bool `json:"ignore_eos"`

	Seed int `json:"seed"`
}

// https://platform.openai.com/docs/api-reference/completions
func openAIEndpoint(chat bool, loader *model.ModelLoader, threads, ctx int, f16 bool, defaultMutex *sync.Mutex, mutexMap *sync.Mutex, mutexes map[string]*sync.Mutex) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		var err error
		var model *llama.LLama
		var gptModel *gptj.GPTJ

		input := new(OpenAIRequest)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		if input.Model == "" {
			return fmt.Errorf("no model specified")
		} else {
			// Try to load the model with both
			var llamaerr error
			llamaOpts := []llama.ModelOption{}
			if ctx != 0 {
				llamaOpts = append(llamaOpts, llama.SetContext(ctx))
			}
			if f16 {
				llamaOpts = append(llamaOpts, llama.EnableF16Memory)
			}

			model, llamaerr = loader.LoadLLaMAModel(input.Model, llamaOpts...)
			if llamaerr != nil {
				gptModel, err = loader.LoadGPTJModel(input.Model)
				if err != nil {
					return fmt.Errorf("llama: %s gpt: %s", llamaerr.Error(), err.Error()) // llama failed first, so we want to catch both errors
				}
			}
		}

		// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
		if input.Model != "" {
			mutexMap.Lock()
			l, ok := mutexes[input.Model]
			if !ok {
				m := &sync.Mutex{}
				mutexes[input.Model] = m
				l = m
			}
			mutexMap.Unlock()
			l.Lock()
			defer l.Unlock()
		} else {
			defaultMutex.Lock()
			defer defaultMutex.Unlock()
		}

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
			for _, i := range input.Messages {
				mess = append(mess, i.Content)
			}

			predInput = strings.Join(mess, "\n")
		}

		// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
		templatedInput, err := loader.TemplatePrefix(input.Model, struct {
			Input string
		}{Input: predInput})
		if err == nil {
			predInput = templatedInput
		}

		result := []Choice{}

		n := input.N

		if input.N == 0 {
			n = 1
		}

		var predFunc func() (string, error)
		switch {
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
			var prediction string

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

		// Return the prediction in the response body
		return c.JSON(OpenAIResponse{
			Model:   input.Model,
			Choices: result,
		})
	}
}

func Start(loader *model.ModelLoader, listenAddr string, threads, ctxSize int, f16 bool) error {
	app := fiber.New()

	// Default middleware config
	app.Use(recover.New())
	app.Use(cors.New())

	// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
	var mutex = &sync.Mutex{}
	mu := map[string]*sync.Mutex{}
	var mumutex = &sync.Mutex{}

	// openAI compatible API endpoint
	app.Post("/v1/chat/completions", openAIEndpoint(true, loader, threads, ctxSize, f16, mutex, mumutex, mu))
	app.Post("/v1/completions", openAIEndpoint(false, loader, threads, ctxSize, f16, mutex, mumutex, mu))
	app.Get("/v1/models", func(c *fiber.Ctx) error {
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
	})

	// Start the server
	app.Listen(listenAddr)
	return nil
}
