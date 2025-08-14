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
	"github.com/mudler/LocalAI/core/http/middleware"
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
func ImageEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			log.Error().Msg("Image Endpoint - Invalid Input")
			return fiber.ErrBadRequest
		}

		config, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			log.Error().Msg("Image Endpoint - Invalid Config")
			return fiber.ErrBadRequest
		}

		// Process input images (for img2img/inpainting)
		src := ""
		if input.File != "" {
			src = processImageFile(input.File, appConfig.GeneratedContentDir)
			if src != "" {
				defer os.RemoveAll(src)
			}
		}

		// Process multiple input images
		var inputImages []string
		if len(input.Files) > 0 {
			for _, file := range input.Files {
				processedFile := processImageFile(file, appConfig.GeneratedContentDir)
				if processedFile != "" {
					inputImages = append(inputImages, processedFile)
					defer os.RemoveAll(processedFile)
				}
			}
		}

		// Process reference images
		var refImages []string
		if len(input.RefImages) > 0 {
			for _, file := range input.RefImages {
				processedFile := processImageFile(file, appConfig.GeneratedContentDir)
				if processedFile != "" {
					refImages = append(refImages, processedFile)
					defer os.RemoveAll(processedFile)
				}
			}
		}

		log.Debug().Msgf("Parameter Config: %+v", config)

		switch config.Backend {
		case "stablediffusion":
			config.Backend = model.StableDiffusionGGMLBackend
		case "":
			config.Backend = model.StableDiffusionGGMLBackend
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
					tempDir = filepath.Join(appConfig.GeneratedContentDir, "images")
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

				// Use the first input image as src if available, otherwise use the original src
				inputSrc := src
				if len(inputImages) > 0 {
					inputSrc = inputImages[0]
				}

				fn, err := backend.ImageGeneration(height, width, mode, step, *config.Seed, positive_prompt, negative_prompt, inputSrc, output, ml, *config, appConfig, refImages)
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

// processImageFile handles a single image file (URL or base64) and returns the path to the temporary file
func processImageFile(file string, generatedContentDir string) string {
	fileData := []byte{}
	var err error

	// check if file is an URL, if so download it and save it to a temporary file
	if strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://") {
		out, err := downloadFile(file)
		if err != nil {
			log.Error().Err(err).Msgf("Failed downloading file: %s", file)
			return ""
		}
		defer os.RemoveAll(out)

		fileData, err = os.ReadFile(out)
		if err != nil {
			log.Error().Err(err).Msgf("Failed reading downloaded file: %s", out)
			return ""
		}
	} else {
		// base 64 decode the file and write it somewhere that we will cleanup
		fileData, err = base64.StdEncoding.DecodeString(file)
		if err != nil {
			log.Error().Err(err).Msgf("Failed decoding base64 file")
			return ""
		}
	}

	// Create a temporary file
	outputFile, err := os.CreateTemp(generatedContentDir, "b64")
	if err != nil {
		log.Error().Err(err).Msg("Failed creating temporary file")
		return ""
	}

	// write the base64 result
	writer := bufio.NewWriter(outputFile)
	_, err = writer.Write(fileData)
	if err != nil {
		outputFile.Close()
		log.Error().Err(err).Msg("Failed writing to temporary file")
		return ""
	}
	outputFile.Close()

	return outputFile.Name()
}
