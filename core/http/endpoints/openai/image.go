package openai

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/mudler/LocalAI/core/backend"

	"github.com/gofiber/fiber/v2"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

func downloadFile(url string) (string, error) {
	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.CreateTemp("", "image")
	if err != nil {
		return "", err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return out.Name(), err
}

//

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
// ImageEndpoint is the OpenAI Image generation API endpoint https://platform.openai.com/docs/api-reference/images/create
// @Summary Creates an image given a prompt.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/images/generations [post]
func ImageEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		m, input, err := readRequest(c, cl, ml, appConfig, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		if m == "" {
			m = model.StableDiffusionBackend
		}
		log.Debug().Msgf("Loading model: %+v", m)

		config, input, err := mergeRequestWithConfig(m, input, cl, ml, appConfig.Debug, 0, 0, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		src := ""
		if input.File != "" {

			fileData := []byte{}
			// check if input.File is an URL, if so download it and save it
			// to a temporary file
			if strings.HasPrefix(input.File, "http://") || strings.HasPrefix(input.File, "https://") {
				out, err := downloadFile(input.File)
				if err != nil {
					return fmt.Errorf("failed downloading file:%w", err)
				}
				defer os.RemoveAll(out)

				fileData, err = os.ReadFile(out)
				if err != nil {
					return fmt.Errorf("failed reading file:%w", err)
				}

			} else {
				// base 64 decode the file and write it somewhere
				// that we will cleanup
				fileData, err = base64.StdEncoding.DecodeString(input.File)
				if err != nil {
					return err
				}
			}

			// Create a temporary file
			outputFile, err := os.CreateTemp(appConfig.ImageDir, "b64")
			if err != nil {
				return err
			}
			// write the base64 result
			writer := bufio.NewWriter(outputFile)
			_, err = writer.Write(fileData)
			if err != nil {
				outputFile.Close()
				return err
			}
			outputFile.Close()
			src = outputFile.Name()
			defer os.RemoveAll(src)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)

		switch config.Backend {
		case "stablediffusion":
			config.Backend = model.StableDiffusionBackend
		case "tinydream":
			config.Backend = model.TinyDreamBackend
		case "":
			config.Backend = model.StableDiffusionBackend
		}

		if !strings.Contains(input.Size, "x") {
			input.Size = "512x512"
			log.Warn().Msgf("Invalid size, using default 512x512")
		}

		sizeParts := strings.Split(input.Size, "x")
		if len(sizeParts) != 2 {
			return fmt.Errorf("invalid value for 'size'")
		}
		width, err := strconv.Atoi(sizeParts[0])
		if err != nil {
			return fmt.Errorf("invalid value for 'size'")
		}
		height, err := strconv.Atoi(sizeParts[1])
		if err != nil {
			return fmt.Errorf("invalid value for 'size'")
		}

		b64JSON := config.ResponseFormat == "b64_json"

		// src and clip_skip
		var result []schema.Item
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
				step := config.Step
				if step == 0 {
					step = 15
				}

				if input.Mode != 0 {
					mode = input.Mode
				}

				if input.Step != 0 {
					step = input.Step
				}

				tempDir := ""
				if !b64JSON {
					tempDir = appConfig.ImageDir
				}
				// Create a temporary file
				outputFile, err := os.CreateTemp(tempDir, "b64")
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

				fn, err := backend.ImageGeneration(height, width, mode, step, *config.Seed, positive_prompt, negative_prompt, src, output, ml, *config, appConfig)
				if err != nil {
					return err
				}
				if err := fn(); err != nil {
					return err
				}

				item := &schema.Item{}

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

		id := uuid.New().String()
		created := int(time.Now().Unix())
		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Data:    result,
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
