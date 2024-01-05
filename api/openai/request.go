package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	config "github.com/go-skynet/LocalAI/api/config"
	options "github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/api/schema"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

func readInput(c *fiber.Ctx, o *options.Option, randomModel bool) (string, *schema.OpenAIRequest, error) {
	loader := o.Loader
	input := new(schema.OpenAIRequest)
	ctx, cancel := context.WithCancel(o.Context)
	input.Context = ctx
	input.Cancel = cancel
	// Get input data from the request body
	if err := c.BodyParser(input); err != nil {
		return "", nil, fmt.Errorf("failed parsing request body: %w", err)
	}

	modelFile := input.Model

	if c.Params("model") != "" {
		modelFile = c.Params("model")
	}

	received, _ := json.Marshal(input)

	log.Debug().Msgf("Request received: %s", string(received))

	// Set model from bearer token, if available
	bearer := strings.TrimLeft(c.Get("authorization"), "Bearer ")
	bearerExists := bearer != "" && loader.ExistsInModelPath(bearer)

	// If no model was specified, take the first available
	if modelFile == "" && !bearerExists && randomModel {
		models, _ := loader.ListModels()
		if len(models) > 0 {
			modelFile = models[0]
			log.Debug().Msgf("No model specified, using: %s", modelFile)
		} else {
			log.Debug().Msgf("No model specified, returning error")
			return "", nil, fmt.Errorf("no model specified")
		}
	}

	// If a model is found in bearer token takes precedence
	if bearerExists {
		log.Debug().Msgf("Using model from bearer token: %s", bearer)
		modelFile = bearer
	}
	return modelFile, input, nil
}

// this function check if the string is an URL, if it's an URL downloads the image in memory
// encodes it in base64 and returns the base64 string
func getBase64Image(s string) (string, error) {
	if strings.HasPrefix(s, "http") {
		// download the image
		resp, err := http.Get(s)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		// read the image data into memory
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}

		// encode the image data in base64
		encoded := base64.StdEncoding.EncodeToString(data)

		// return the base64 string
		return encoded, nil
	}

	// if the string instead is prefixed with "data:image/jpeg;base64,", drop it
	if strings.HasPrefix(s, "data:image/jpeg;base64,") {
		return strings.ReplaceAll(s, "data:image/jpeg;base64,", ""), nil
	}
	return "", fmt.Errorf("not valid string")
}

func updateConfig(config *config.Config, input *schema.OpenAIRequest) {
	if input.Echo {
		config.Echo = input.Echo
	}
	if input.TopK != 0 {
		config.TopK = input.TopK
	}
	if input.TopP != 0 {
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

	if input.Temperature != 0 {
		config.Temperature = input.Temperature
	}

	if input.Maxtokens != 0 {
		config.Maxtokens = input.Maxtokens
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

	// Decode each request's message content
	index := 0
	for i, m := range input.Messages {
		switch content := m.Content.(type) {
		case string:
			input.Messages[i].StringContent = content
		case []interface{}:
			dat, _ := json.Marshal(content)
			c := []schema.Content{}
			json.Unmarshal(dat, &c)
			for _, pp := range c {
				if pp.Type == "text" {
					input.Messages[i].StringContent = pp.Text
				} else if pp.Type == "image_url" {
					// Detect if pp.ImageURL is an URL, if it is download the image and encode it in base64:
					base64, err := getBase64Image(pp.ImageURL.URL)
					if err == nil {
						input.Messages[i].StringImages = append(input.Messages[i].StringImages, base64) // TODO: make sure that we only return base64 stuff
						// set a placeholder for each image
						input.Messages[i].StringContent = fmt.Sprintf("[img-%d]", index) + input.Messages[i].StringContent
						index++
					} else {
						fmt.Print("Failed encoding image", err)
					}
				}
			}
		}
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

	if input.Mirostat != 0 {
		config.LLMConfig.Mirostat = input.Mirostat
	}

	if input.MirostatETA != 0 {
		config.LLMConfig.MirostatETA = input.MirostatETA
	}

	if input.MirostatTAU != 0 {
		config.LLMConfig.MirostatTAU = input.MirostatTAU
	}

	if input.TypicalP != 0 {
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

func readConfig(modelFile string, input *schema.OpenAIRequest, cm *config.ConfigLoader, loader *model.ModelLoader, debug bool, threads, ctx int, f16 bool) (*config.Config, *schema.OpenAIRequest, error) {
	// Load a config file if present after the model name
	modelConfig := filepath.Join(loader.ModelPath, modelFile+".yaml")

	var cfg *config.Config

	defaults := func() {
		cfg = config.DefaultConfig(modelFile)
		cfg.ContextSize = ctx
		cfg.Threads = threads
		cfg.F16 = f16
		cfg.Debug = debug
	}

	cfgExisting, exists := cm.GetConfig(modelFile)
	if !exists {
		if _, err := os.Stat(modelConfig); err == nil {
			if err := cm.LoadConfig(modelConfig); err != nil {
				return nil, nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
			}
			cfgExisting, exists = cm.GetConfig(modelFile)
			if exists {
				cfg = &cfgExisting
			} else {
				defaults()
			}
		} else {
			defaults()
		}
	} else {
		cfg = &cfgExisting
	}

	// Set the parameters for the language model prediction
	updateConfig(cfg, input)

	// Don't allow 0 as setting
	if cfg.Threads == 0 {
		if threads != 0 {
			cfg.Threads = threads
		} else {
			cfg.Threads = 4
		}
	}

	// Enforce debug flag if passed from CLI
	if debug {
		cfg.Debug = true
	}

	return cfg, input, nil
}
