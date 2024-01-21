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

	"github.com/go-skynet/LocalAI/api/schema"
	"github.com/google/uuid"

	"github.com/go-skynet/LocalAI/api/backend"
	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
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
func ImageEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		m, input, err := readInput(c, o, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		if m == "" {
			m = model.StableDiffusionBackend
		}
		log.Debug().Msgf("Loading model: %+v", m)

		config, input, err := readConfig(m, input, cm, o.Loader, o.Debug, 0, 0, false)
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
			outputFile, err := os.CreateTemp(o.ImageDir, "b64")
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
		if input.ResponseFormat.Type == "b64_json" {
			b64JSON = true
		}
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
					tempDir = o.ImageDir
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

				fn, err := backend.ImageGeneration(height, width, mode, step, input.Seed, positive_prompt, negative_prompt, src, output, o.Loader, *config, o)
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
