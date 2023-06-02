package api

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	model "github.com/go-skynet/LocalAI/pkg/model"
	whisperutil "github.com/go-skynet/LocalAI/pkg/whisper"
	llama "github.com/go-skynet/go-llama.cpp"
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

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Item struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
	Object    string    `json:"object,omitempty"`

	// Images
	URL     string `json:"url,omitempty"`
	B64JSON string `json:"b64_json,omitempty"`
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
	File     string `json:"file" validate:"required"`
	Language string `json:"language"`
	//whisper/image
	ResponseFormat string `json:"response_format"`
	// image
	Size string `json:"size"`
	// Prompt is read only by completion/image API calls
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

	// Image (not supported by OpenAI)
	Mode int `json:"mode"`
	Step int `json:"step"`
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
func completionEndpoint(cm *ConfigMerger, o *Option) func(c *fiber.Ctx) error {
	process := func(s string, req *OpenAIRequest, config *Config, loader *model.ModelLoader, responses chan OpenAIResponse) {
		ComputeChoices(s, req, config, loader, func(s string, c *[]Choice) {}, func(s string) bool {
			resp := OpenAIResponse{
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []Choice{{Text: s}},
				Object:  "text_completion",
			}
			log.Debug().Msgf("Sending goroutine: %s", s)

			responses <- resp
			return true
		})
		close(responses)
	}

	return func(c *fiber.Ctx) error {
		model, input, err := readInput(c, o.loader, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("`input`: %+v", input)

		config, input, err := readConfig(model, input, cm, o.loader, o.debug, o.threads, o.ctxSize, o.f16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)

		if input.Stream {
			log.Debug().Msgf("Stream request received")
			c.Context().SetContentType("text/event-stream")
			//c.Response().Header.SetContentType(fiber.MIMETextHTMLCharsetUTF8)
			//c.Set("Content-Type", "text/event-stream")
			c.Set("Cache-Control", "no-cache")
			c.Set("Connection", "keep-alive")
			c.Set("Transfer-Encoding", "chunked")
		}

		templateFile := config.Model

		if config.TemplateConfig.Completion != "" {
			templateFile = config.TemplateConfig.Completion
		}

		if input.Stream {
			if (len(config.PromptStrings) > 1) {
				return errors.New("cannot handle more than 1 `PromptStrings` when `Stream`ing")
			}

			predInput := config.PromptStrings[0]

			// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
			templatedInput, err := o.loader.TemplatePrefix(templateFile, struct {
				Input string
			}{Input: predInput})
			if err == nil {
				predInput = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", predInput)
			}

			responses := make(chan OpenAIResponse)

			go process(predInput, input, config, o.loader, responses)

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {

				for ev := range responses {
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					enc.Encode(ev)

					log.Debug().Msgf("Sending chunk: %s", buf.String())
					fmt.Fprintf(w, "data: %v\n", buf.String())
					w.Flush()
				}

				resp := &OpenAIResponse{
					Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []Choice{{FinishReason: "stop"}},
				}
				respData, _ := json.Marshal(resp)

				w.WriteString(fmt.Sprintf("data: %s\n\n", respData))
				w.WriteString("data: [DONE]\n\n")
				w.Flush()
			}))
			return nil
		}

		var result []Choice
		for _, i := range config.PromptStrings {
			// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
			templatedInput, err := o.loader.TemplatePrefix(templateFile, struct {
				Input string
			}{Input: i})
			if err == nil {
				i = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", i)
			}

			r, err := ComputeChoices(i, input, config, o.loader, func(s string, c *[]Choice) {
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
func embeddingsEndpoint(cm *ConfigMerger, o *Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		model, input, err := readInput(c, o.loader, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		config, input, err := readConfig(model, input, cm, o.loader, o.debug, o.threads, o.ctxSize, o.f16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)
		items := []Item{}

		for i, s := range config.InputToken {
			// get the model function to call for the result
			embedFn, err := ModelEmbedding("", s, o.loader, *config)
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
			embedFn, err := ModelEmbedding(s, []int{}, o.loader, *config)
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

func chatEndpoint(cm *ConfigMerger, o *Option) func(c *fiber.Ctx) error {

	process := func(s string, req *OpenAIRequest, config *Config, loader *model.ModelLoader, responses chan OpenAIResponse) {
		initialMessage := OpenAIResponse{
			Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: []Choice{{Delta: &Message{Role: "assistant"}}},
			Object:  "chat.completion.chunk",
		}
		responses <- initialMessage

		ComputeChoices(s, req, config, loader, func(s string, c *[]Choice) {}, func(s string) bool {
			resp := OpenAIResponse{
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []Choice{{Delta: &Message{Content: s}}},
				Object:  "chat.completion.chunk",
			}
			log.Debug().Msgf("Sending goroutine: %s", s)

			responses <- resp
			return true
		})
		close(responses)
	}
	return func(c *fiber.Ctx) error {
		model, input, err := readInput(c, o.loader, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		config, input, err := readConfig(model, input, cm, o.loader, o.debug, o.threads, o.ctxSize, o.f16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)

		var predInput string

		mess := []string{}
		for _, i := range input.Messages {
			var content string
			r := config.Roles[i.Role]
			if r != "" {
				content = fmt.Sprint(r, " ", i.Content)
			} else {
				content = i.Content
			}

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
		templatedInput, err := o.loader.TemplatePrefix(templateFile, struct {
			Input string
		}{Input: predInput})
		if err == nil {
			predInput = templatedInput
			log.Debug().Msgf("Template found, input modified to: %s", predInput)
		}

		if input.Stream {
			responses := make(chan OpenAIResponse)

			go process(predInput, input, config, o.loader, responses)

			c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {

				for ev := range responses {
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					enc.Encode(ev)

					log.Debug().Msgf("Sending chunk: %s", buf.String())
					fmt.Fprintf(w, "data: %v\n", buf.String())
					w.Flush()
				}

				resp := &OpenAIResponse{
					Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []Choice{{FinishReason: "stop"}},
				}
				respData, _ := json.Marshal(resp)

				w.WriteString(fmt.Sprintf("data: %s\n\n", respData))
				w.WriteString("data: [DONE]\n\n")
				w.Flush()
			}))
			return nil
		}

		result, err := ComputeChoices(predInput, input, config, o.loader, func(s string, c *[]Choice) {
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

func editEndpoint(cm *ConfigMerger, o *Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		model, input, err := readInput(c, o.loader, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		config, input, err := readConfig(model, input, cm, o.loader, o.debug, o.threads, o.ctxSize, o.f16)
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
			templatedInput, err := o.loader.TemplatePrefix(templateFile, struct {
				Input       string
				Instruction string
			}{Input: i})
			if err == nil {
				i = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", i)
			}

			r, err := ComputeChoices(i, input, config, o.loader, func(s string, c *[]Choice) {
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

// https://platform.openai.com/docs/api-reference/images/create

/*
*

	curl http://localhost:8080/v1/images/generations \
	  -H "Content-Type: application/json" \
	  -d '{
	    "prompt": "A cute baby sea otter",
	    "n": 1,
	    "size": "512x512"
	  }'

*
*/
func imageEndpoint(cm *ConfigMerger, o *Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		m, input, err := readInput(c, o.loader, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		if m == "" {
			m = model.StableDiffusionBackend
		}
		log.Debug().Msgf("Loading model: %+v", m)

		config, input, err := readConfig(m, input, cm, o.loader, o.debug, 0, 0, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)

		// XXX: Only stablediffusion is supported for now
		if config.Backend == "" {
			config.Backend = model.StableDiffusionBackend
		}

		sizeParts := strings.Split(input.Size, "x")
		if len(sizeParts) != 2 {
			return fmt.Errorf("Invalid value for 'size'")
		}
		width, err := strconv.Atoi(sizeParts[0])
		if err != nil {
			return fmt.Errorf("Invalid value for 'size'")
		}
		height, err := strconv.Atoi(sizeParts[1])
		if err != nil {
			return fmt.Errorf("Invalid value for 'size'")
		}

		b64JSON := false
		if input.ResponseFormat == "b64_json" {
			b64JSON = true
		}

		var result []Item
		for _, i := range config.PromptStrings {
			n := input.N
			if input.N == 0 {
				n = 1
			}
			for j := 0; j < n; j++ {
				prompts := strings.Split(i, "|")
				positive_prompt := prompts[0]
				negative_prompt := ""
				if len(prompts) > 1 {
					negative_prompt = prompts[1]
				}

				mode := 0
				step := 15

				if input.Mode != 0 {
					mode = input.Mode
				}

				if input.Step != 0 {
					step = input.Step
				}

				tempDir := ""
				if !b64JSON {
					tempDir = o.imageDir
				}
				// Create a temporary file
				outputFile, err := ioutil.TempFile(tempDir, "b64")
				if err != nil {
					return err
				}
				outputFile.Close()
				output := outputFile.Name() + ".png"
				// Rename the temporary file
				err = os.Rename(outputFile.Name(), output)
				if err != nil {
					return err
				}

				baseURL := c.BaseURL()

				fn, err := ImageGeneration(height, width, mode, step, input.Seed, positive_prompt, negative_prompt, output, o.loader, *config)
				if err != nil {
					return err
				}
				if err := fn(); err != nil {
					return err
				}

				item := &Item{}

				if b64JSON {
					defer os.RemoveAll(output)
					data, err := os.ReadFile(output)
					if err != nil {
						return err
					}
					item.B64JSON = base64.StdEncoding.EncodeToString(data)
				} else {
					base := filepath.Base(output)
					item.URL = baseURL + "/generated-images/" + base
				}

				result = append(result, *item)
			}
		}

		resp := &OpenAIResponse{
			Data: result,
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}

// https://platform.openai.com/docs/api-reference/audio/create
func transcriptEndpoint(cm *ConfigMerger, o *Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		m, input, err := readInput(c, o.loader, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		config, input, err := readConfig(m, input, cm, o.loader, o.debug, o.threads, o.ctxSize, o.f16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}
		// retrieve the file data from the request
		file, err := c.FormFile("file")
		if err != nil {
			return err
		}
		f, err := file.Open()
		if err != nil {
			return err
		}
		defer f.Close()

		dir, err := os.MkdirTemp("", "whisper")

		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)

		dst := filepath.Join(dir, path.Base(file.Filename))
		dstFile, err := os.Create(dst)
		if err != nil {
			return err
		}

		if _, err := io.Copy(dstFile, f); err != nil {
			log.Debug().Msgf("Audio file copying error %+v - %+v - err %+v", file.Filename, dst, err)
			return err
		}

		log.Debug().Msgf("Audio file copied to: %+v", dst)

		whisperModel, err := o.loader.BackendLoader(model.WhisperBackend, config.Model, []llama.ModelOption{}, uint32(config.Threads))
		if err != nil {
			return err
		}

		if whisperModel == nil {
			return fmt.Errorf("could not load whisper model")
		}

		w, ok := whisperModel.(whisper.Model)
		if !ok {
			return fmt.Errorf("loader returned non-whisper object")
		}

		tr, err := whisperutil.Transcript(w, dst, input.Language, uint(config.Threads))
		if err != nil {
			return err
		}

		log.Debug().Msgf("Trascribed: %+v", tr)
		// TODO: handle different outputs here
		return c.Status(http.StatusOK).JSON(fiber.Map{"text": tr})
	}
}

func listModels(loader *model.ModelLoader, cm *ConfigMerger) func(ctx *fiber.Ctx) error {
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

		for _, k := range cm.ListConfigs() {
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
