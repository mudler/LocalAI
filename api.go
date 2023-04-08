package main

import (
	"embed"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	llama "github.com/go-skynet/go-llama.cpp"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

type OpenAIResponse struct {
	Created int      `json:"created"`
	Object  string   `json:"chat.completion"`
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Index        int     `json:"index"`
	FinishReason string  `json:"finish_reason"`
	Message      Message `json:"message"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

//go:embed index.html
var indexHTML embed.FS

func api(defaultModel *llama.LLama, loader *ModelLoader, listenAddr string, threads int) error {
	app := fiber.New()

	// Default middleware config
	app.Use(recover.New())
	app.Use(cors.New())

	app.Use("/", filesystem.New(filesystem.Config{
		Root:         http.FS(indexHTML),
		NotFoundFile: "index.html",
	}))

	// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
	var mutex = &sync.Mutex{}
	mu := map[string]*sync.Mutex{}
	var mumutex = &sync.Mutex{}

	// openAI compatible API endpoint
	app.Post("/v1/chat/completions", func(c *fiber.Ctx) error {

		var err error
		var model *llama.LLama

		// Get input data from the request body
		input := new(struct {
			Messages []Message `json:"messages"`
			Model    string    `json:"model"`
		})
		if err := c.BodyParser(input); err != nil {
			return err
		}

		if input.Model == "" {
			if defaultModel == nil {
				return fmt.Errorf("no default model loaded, and no model specified")
			}
			model = defaultModel
		} else {
			model, err = loader.LoadModel(input.Model)
			if err != nil {
				return err
			}
		}

		// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
		if input.Model != "" {
			mumutex.Lock()
			l, ok := mu[input.Model]
			if !ok {
				m := &sync.Mutex{}
				mu[input.Model] = m
				l = m
			}
			mumutex.Unlock()
			l.Lock()
			defer l.Unlock()
		} else {
			mutex.Lock()
			defer mutex.Unlock()
		}

		// Set the parameters for the language model prediction
		topP, err := strconv.ParseFloat(c.Query("topP", "0.9"), 64) // Default value of topP is 0.9
		if err != nil {
			return err
		}

		topK, err := strconv.Atoi(c.Query("topK", "40")) // Default value of topK is 40
		if err != nil {
			return err
		}

		temperature, err := strconv.ParseFloat(c.Query("temperature", "0.5"), 64) // Default value of temperature is 0.5
		if err != nil {
			return err
		}

		tokens, err := strconv.Atoi(c.Query("tokens", "128")) // Default value of tokens is 128
		if err != nil {
			return err
		}

		mess := []string{}
		for _, i := range input.Messages {
			mess = append(mess, i.Content)
		}

		predInput := strings.Join(mess, "\n")

		// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
		templatedInput, err := loader.TemplatePrefix(input.Model, struct {
			Input string
		}{Input: predInput})
		if err == nil {
			predInput = templatedInput
		}

		// Generate the prediction using the language model
		prediction, err := model.Predict(
			predInput,
			llama.SetTemperature(temperature),
			llama.SetTopP(topP),
			llama.SetTopK(topK),
			llama.SetTokens(tokens),
			llama.SetThreads(threads),
		)
		if err != nil {
			return err
		}

		// Return the prediction in the response body
		return c.JSON(OpenAIResponse{
			Model:   input.Model,
			Choices: []Choice{{Message: Message{Role: "assistant", Content: prediction}}},
		})
	})

	/*
		curl --location --request POST 'http://localhost:8080/predict' --header 'Content-Type: application/json' --data-raw '{
		    "text": "What is an alpaca?",
		    "topP": 0.8,
		    "topK": 50,
		    "temperature": 0.7,
		    "tokens": 100
		}'
	*/
	// Endpoint to generate the prediction
	app.Post("/predict", func(c *fiber.Ctx) error {
		mutex.Lock()
		defer mutex.Unlock()
		// Get input data from the request body
		input := new(struct {
			Text string `json:"text"`
		})
		if err := c.BodyParser(input); err != nil {
			return err
		}

		// Set the parameters for the language model prediction
		topP, err := strconv.ParseFloat(c.Query("topP", "0.9"), 64) // Default value of topP is 0.9
		if err != nil {
			return err
		}

		topK, err := strconv.Atoi(c.Query("topK", "40")) // Default value of topK is 40
		if err != nil {
			return err
		}

		temperature, err := strconv.ParseFloat(c.Query("temperature", "0.5"), 64) // Default value of temperature is 0.5
		if err != nil {
			return err
		}

		tokens, err := strconv.Atoi(c.Query("tokens", "128")) // Default value of tokens is 128
		if err != nil {
			return err
		}

		// Generate the prediction using the language model
		prediction, err := defaultModel.Predict(
			input.Text,
			llama.SetTemperature(temperature),
			llama.SetTopP(topP),
			llama.SetTopK(topK),
			llama.SetTokens(tokens),
			llama.SetThreads(threads),
		)
		if err != nil {
			return err
		}

		// Return the prediction in the response body
		return c.JSON(struct {
			Prediction string `json:"prediction"`
		}{
			Prediction: prediction,
		})
	})

	// Start the server
	app.Listen(listenAddr)
	return nil
}
