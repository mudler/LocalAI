package localai

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	out, err := os.CreateTemp("", "video")
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
// VideoEndpoint
// @Summary Creates a video given a prompt.
// @Param request body schema.VideoRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /video [post]
func VideoEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.VideoRequest)
		if !ok || input.Model == "" {
			log.Error().Msg("Video Endpoint - Invalid Input")
			return fiber.ErrBadRequest
		}

		config, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			log.Error().Msg("Video Endpoint - Invalid Config")
			return fiber.ErrBadRequest
		}

		src := ""
		if input.StartImage != "" {

			var fileData []byte
			var err error
			// check if input.File is an URL, if so download it and save it
			// to a temporary file
			if strings.HasPrefix(input.StartImage, "http://") || strings.HasPrefix(input.StartImage, "https://") {
				out, err := downloadFile(input.StartImage)
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
				fileData, err = base64.StdEncoding.DecodeString(input.StartImage)
				if err != nil {
					return err
				}
			}

			// Create a temporary file
			outputFile, err := os.CreateTemp(appConfig.GeneratedContentDir, "b64")
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
			config.Backend = model.StableDiffusionGGMLBackend
		case "":
			config.Backend = model.StableDiffusionGGMLBackend
		}

		width := input.Width
		height := input.Height

		if width == 0 {
			width = 512
		}
		if height == 0 {
			height = 512
		}

		b64JSON := input.ResponseFormat == "b64_json"

		tempDir := ""
		if !b64JSON {
			tempDir = filepath.Join(appConfig.GeneratedContentDir, "videos")
		}
		// Create a temporary file
		outputFile, err := os.CreateTemp(tempDir, "b64")
		if err != nil {
			return err
		}
		outputFile.Close()

		// TODO: use mime type to determine the extension
		output := outputFile.Name() + ".mp4"

		// Rename the temporary file
		err = os.Rename(outputFile.Name(), output)
		if err != nil {
			return err
		}

		baseURL := c.BaseURL()

		fn, err := backend.VideoGeneration(
			height,
			width,
			input.Prompt,
			input.NegativePrompt,
			src,
			input.EndImage,
			output,
			input.NumFrames,
			input.FPS,
			input.Seed,
			input.CFGScale,
			input.Step,
			ml,
			*config,
			appConfig,
		)
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
			item.URL = baseURL + "/generated-videos/" + base
		}

		id := uuid.New().String()
		created := int(time.Now().Unix())
		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Data:    []schema.Item{*item},
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
