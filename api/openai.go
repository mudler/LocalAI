package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
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
	Object  string   `json:"object,omitempty"`
	ID      string   `json:"id,omitempty"`
	Model   string   `json:"model,omitempty"`
	Choices []Choice `json:"choices,omitempty"`
}

type Choice struct {
	Index        int      `json:"index,omitempty"`
	FinishReason string   `json:"finish_reason,omitempty"`
	Message      *Message `json:"message,omitempty"`
	Delta        *Message `json:"delta,omitempty"`
	Text         string   `json:"text,omitempty"`
}

type Message struct {
	Role    string `json:"role,omitempty" yaml:"role"`
	Content string `json:"content,omitempty" yaml:"content"`
}

type OpenAIModel struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

type OpenAIRequest struct {
	Model string `json:"model" yaml:"model"`

	// Prompt is read only by completion API calls
	Prompt string `json:"prompt" yaml:"prompt"`

	// Edit endpoint
	Instruction string `json:"instruction" yaml:"instruction"`
	Input       string `json:"input" yaml:"input"`

	Stop []string `json:"stop" yaml:"stop"`

	// Messages is read only by chat/completion API calls
	Messages []Message `json:"messages" yaml:"messages"`

	Stream bool `json:"stream"`
	Echo   bool `json:"echo"`
	// Common options between all the API calls
	TopP        float64 `json:"top_p" yaml:"top_p"`
	TopK        int     `json:"top_k" yaml:"top_k"`
	Temperature float64 `json:"temperature" yaml:"temperature"`
	Maxtokens   int     `json:"max_tokens" yaml:"max_tokens"`

	N int `json:"n"`

	// Custom parameters - not present in the OpenAI API
	Batch         int     `json:"batch" yaml:"batch"`
	F16           bool    `json:"f16" yaml:"f16"`
	IgnoreEOS     bool    `json:"ignore_eos" yaml:"ignore_eos"`
	RepeatPenalty float64 `json:"repeat_penalty" yaml:"repeat_penalty"`
	Keep          int     `json:"n_keep" yaml:"n_keep"`

	Seed int `json:"seed" yaml:"seed"`
}

func defaultRequest(modelFile string) OpenAIRequest {
	return OpenAIRequest{
		TopP:        0.7,
		TopK:        80,
		Maxtokens:   512,
		Temperature: 0.9,
		Model:       modelFile,
	}
}

func updateConfig(config *Config, input *OpenAIRequest) {
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

	if len(input.Stop) != 0 {
		config.StopWords = append(config.StopWords, input.Stop...)
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
}

func readConfig(cm ConfigMerger, c *fiber.Ctx, loader *model.ModelLoader, debug bool, threads, ctx int, f16 bool) (*Config, *OpenAIRequest, error) {
	input := new(OpenAIRequest)
	// Get input data from the request body
	if err := c.BodyParser(input); err != nil {
		return nil, nil, err
	}

	modelFile := input.Model
	received, _ := json.Marshal(input)

	log.Debug().Msgf("Request received: %s", string(received))

	// Set model from bearer token, if available
	bearer := strings.TrimLeft(c.Get("authorization"), "Bearer ")
	bearerExists := bearer != "" && loader.ExistsInModelPath(bearer)

	// If no model was specified, take the first available
	if modelFile == "" && !bearerExists {
		models, _ := loader.ListModels()
		if len(models) > 0 {
			modelFile = models[0]
			log.Debug().Msgf("No model specified, using: %s", modelFile)
		} else {
			log.Debug().Msgf("No model specified, returning error")
			return nil, nil, fmt.Errorf("no model specified")
		}
	}

	// If a model is found in bearer token takes precedence
	if bearerExists {
		log.Debug().Msgf("Using model from bearer token: %s", bearer)
		modelFile = bearer
	}

	// Load a config file if present after the model name
	modelConfig := filepath.Join(loader.ModelPath, modelFile+".yaml")
	if _, err := os.Stat(modelConfig); err == nil {
		if err := cm.LoadConfig(modelConfig); err != nil {
			return nil, nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
		}
	}

	var config *Config
	cfg, exists := cm[modelFile]
	if !exists {
		config = &Config{
			OpenAIRequest: defaultRequest(modelFile),
		}
	} else {
		config = &cfg
	}

	// Set the parameters for the language model prediction
	updateConfig(config, input)

	if threads != 0 {
		config.Threads = threads
	}
	if ctx != 0 {
		config.ContextSize = ctx
	}
	if f16 {
		config.F16 = true
	}

	if debug {
		config.Debug = true
	}

	return config, input, nil
}

// https://platform.openai.com/docs/api-reference/completions
func completionEndpoint(cm ConfigMerger, debug bool, loader *model.ModelLoader, threads, ctx int, f16 bool) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		config, input, err := readConfig(cm, c, loader, debug, threads, ctx, f16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)

		predInput := input.Prompt
		templateFile := config.Model

		if config.TemplateConfig.Completion != "" {
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

		result, err := ComputeChoices(predInput, input, config, loader, func(s string, c *[]Choice) {
			*c = append(*c, Choice{Text: s})
		}, nil)
		if err != nil {
			return err
		}

		resp := &OpenAIResponse{
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "text_completion",
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}

func chatEndpoint(cm ConfigMerger, debug bool, loader *model.ModelLoader, threads, ctx int, f16 bool) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		config, input, err := readConfig(cm, c, loader, debug, threads, ctx, f16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)

		var predInput string

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

		if input.Stream {
			log.Debug().Msgf("Stream request received")
			c.Context().SetContentType("text/event-stream")
			//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
			//	c.Set("Content-Type", "text/event-stream")
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")
			c.Set("Transfer-Encoding", "chunked")
		}

		templateFile := config.Model

		if config.TemplateConfig.Chat != "" {
			templateFile = config.TemplateConfig.Chat
		}

		// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
		templatedInput, err := loader.TemplatePrefix(templateFile, struct {
			Input string
		}{Input: predInput})
		if err == nil {
			predInput = templatedInput
			log.Debug().Msgf("Template found, input modified to: %s", predInput)
		}

		if input.Stream {
			responses := make(chan OpenAIResponse)

			go func() {
				ComputeChoices(predInput, input, config, loader, func(s string, c *[]Choice) {}, func(s string) bool {
					resp := OpenAIResponse{
						Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
						Choices: []Choice{{Delta: &Message{Role: "assistant", Content: s}}},
						Object:  "chat.completion.chunk",
					}

					responses <- resp
					return true
				})
				close(responses)
			}()

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {

				for ev := range responses {
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					enc.Encode(ev)

					fmt.Fprintf(w, "event: data\n\n")
					fmt.Fprintf(w, "data: %v\n\n", buf.String())
					log.Debug().Msgf("Sending chunk: %s", buf.String())
					w.Flush()
				}

				w.WriteString("event: data\n\n")
				resp := &OpenAIResponse{
					Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []Choice{{FinishReason: "stop"}},
				}
				respData, _ := json.Marshal(resp)

				w.WriteString(fmt.Sprintf("data: %s\n\n", respData))
				w.Flush()
			}))
			return nil
		}

		result, err := ComputeChoices(predInput, input, config, loader, func(s string, c *[]Choice) {
			*c = append(*c, Choice{Message: &Message{Role: "assistant", Content: s}})
		}, nil)
		if err != nil {
			return err
		}

		resp := &OpenAIResponse{
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "chat.completion",
		}

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}

func editEndpoint(cm ConfigMerger, debug bool, loader *model.ModelLoader, threads, ctx int, f16 bool) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		config, input, err := readConfig(cm, c, loader, debug, threads, ctx, f16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)

		predInput := input.Input
		templateFile := config.Model

		if config.TemplateConfig.Edit != "" {
			templateFile = config.TemplateConfig.Edit
		}

		// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
		templatedInput, err := loader.TemplatePrefix(templateFile, struct {
			Input       string
			Instruction string
		}{Input: predInput, Instruction: input.Instruction})
		if err == nil {
			predInput = templatedInput
			log.Debug().Msgf("Template found, input modified to: %s", predInput)
		}

		result, err := ComputeChoices(predInput, input, config, loader, func(s string, c *[]Choice) {
			*c = append(*c, Choice{Text: s})
		}, nil)
		if err != nil {
			return err
		}

		resp := &OpenAIResponse{
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "edit",
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}

func listModels(loader *model.ModelLoader, cm ConfigMerger) func(ctx *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		models, err := loader.ListModels()
		if err != nil {
			return err
		}
		var mm map[string]interface{} = map[string]interface{}{}

		dataModels := []OpenAIModel{}
		for _, m := range models {
			mm[m] = nil
			dataModels = append(dataModels, OpenAIModel{ID: m, Object: "model"})
		}

		for k := range cm {
			if _, exists := mm[k]; !exists {
				dataModels = append(dataModels, OpenAIModel{ID: k, Object: "model"})
			}
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
