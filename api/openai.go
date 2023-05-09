package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/whisper"
	"github.com/gofiber/fiber/v2"
	"github.com/otiai10/copy"
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

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Item struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
	Object    string    `json:"object,omitempty"`
}

type OpenAIResponse struct {
	Created int      `json:"created,omitempty"`
	Object  string   `json:"object,omitempty"`
	ID      string   `json:"id,omitempty"`
	Model   string   `json:"model,omitempty"`
	Choices []Choice `json:"choices,omitempty"`
	Data    []Item   `json:"data,omitempty"`

	Usage OpenAIUsage `json:"usage"`
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

	// whisper
	File           string `json:"file" validate:"required"`
	ResponseFormat string `json:"response_format"`
	Language       string `json:"language"`

	// Prompt is read only by completion API calls
	Prompt interface{} `json:"prompt" yaml:"prompt"`

	// Edit endpoint
	Instruction string      `json:"instruction" yaml:"instruction"`
	Input       interface{} `json:"input" yaml:"input"`

	Stop interface{} `json:"stop" yaml:"stop"`

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

	MirostatETA float64 `json:"mirostat_eta" yaml:"mirostat_eta"`
	MirostatTAU float64 `json:"mirostat_tau" yaml:"mirostat_tau"`
	Mirostat    int     `json:"mirostat" yaml:"mirostat"`

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

// https://platform.openai.com/docs/api-reference/completions
func completionEndpoint(cm ConfigMerger, debug bool, loader *model.ModelLoader, threads, ctx int, f16 bool) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		config, input, err := readConfig(cm, c, loader, debug, threads, ctx, f16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)

		templateFile := config.Model

		if config.TemplateConfig.Completion != "" {
			templateFile = config.TemplateConfig.Completion
		}

		var result []Choice
		for _, i := range config.PromptStrings {
			// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
			templatedInput, err := loader.TemplatePrefix(templateFile, struct {
				Input string
			}{Input: i})
			if err == nil {
				i = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", i)
			}

			r, err := ComputeChoices(i, input, config, loader, func(s string, c *[]Choice) {
				*c = append(*c, Choice{Text: s})
			}, nil)
			if err != nil {
				return err
			}

			result = append(result, r...)
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

// https://platform.openai.com/docs/api-reference/embeddings
func embeddingsEndpoint(cm ConfigMerger, debug bool, loader *model.ModelLoader, threads, ctx int, f16 bool) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		config, input, err := readConfig(cm, c, loader, debug, threads, ctx, f16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)
		items := []Item{}

		for i, s := range config.InputToken {
			// get the model function to call for the result
			embedFn, err := ModelEmbedding("", s, loader, *config)
			if err != nil {
				return err
			}

			embeddings, err := embedFn()
			if err != nil {
				return err
			}
			items = append(items, Item{Embedding: embeddings, Index: i, Object: "embedding"})
		}

		for i, s := range config.InputStrings {
			// get the model function to call for the result
			embedFn, err := ModelEmbedding(s, []int{}, loader, *config)
			if err != nil {
				return err
			}

			embeddings, err := embedFn()
			if err != nil {
				return err
			}
			items = append(items, Item{Embedding: embeddings, Index: i, Object: "embedding"})
		}

		resp := &OpenAIResponse{
			Model:  input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Data:   items,
			Object: "list",
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}

func chatEndpoint(cm ConfigMerger, debug bool, loader *model.ModelLoader, threads, ctx int, f16 bool) func(c *fiber.Ctx) error {

	process := func(s string, req *OpenAIRequest, config *Config, loader *model.ModelLoader, responses chan OpenAIResponse) {
		ComputeChoices(s, req, config, loader, func(s string, c *[]Choice) {}, func(s string) bool {
			resp := OpenAIResponse{
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []Choice{{Delta: &Message{Role: "assistant", Content: s}}},
				Object:  "chat.completion.chunk",
			}
			log.Debug().Msgf("Sending goroutine: %s", s)

			responses <- resp
			return true
		})
		close(responses)
	}
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

			go process(predInput, input, config, loader, responses)

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
		respData, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", respData)

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

		templateFile := config.Model

		if config.TemplateConfig.Edit != "" {
			templateFile = config.TemplateConfig.Edit
		}

		var result []Choice
		for _, i := range config.InputStrings {
			// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
			templatedInput, err := loader.TemplatePrefix(templateFile, struct {
				Input       string
				Instruction string
			}{Input: i})
			if err == nil {
				i = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", i)
			}

			r, err := ComputeChoices(i, input, config, loader, func(s string, c *[]Choice) {
				*c = append(*c, Choice{Text: s})
			}, nil)
			if err != nil {
				return err
			}

			result = append(result, r...)
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

// https://platform.openai.com/docs/api-reference/audio/create
func transcriptEndpoint(cm ConfigMerger, debug bool, loader *model.ModelLoader, threads, ctx int, f16 bool) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		config, input, err := readConfig(cm, c, loader, debug, threads, ctx, f16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		// retrieve the file data from the request
		file, err := c.FormFile("file")
		if err != nil {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		log.Debug().Msgf("Audio file: %+v", file)

		dir, err := os.MkdirTemp("", "whisper")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)

		dst := filepath.Join(dir, path.Base(file.Filename))
		if err := copy.Copy(file.Filename, dst); err != nil {
			return err
		}

		log.Debug().Msgf("Audio file copied to: %+v", dst)

		tr, err := whisper.Transcript(filepath.Join(loader.ModelPath, config.Model), dst, input.Language)
		if err != nil {
			return err
		}

		log.Debug().Msgf("Trascribed: %+v", tr)
		// TODO: handle different outputs here
		return c.Status(http.StatusOK).JSON(fiber.Map{"text": tr})
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
