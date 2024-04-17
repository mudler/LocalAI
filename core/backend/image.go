package backend

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/go-skynet/LocalAI/pkg/concurrency"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/model"
)

type ImageGenerationBackendService struct {
	ml                        *model.ModelLoader
	bcl                       *config.BackendConfigLoader
	appConfig                 *config.ApplicationConfig
	BaseUrlForGeneratedImages string
}

func NewImageGenerationBackendService(ml *model.ModelLoader, bcl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) *ImageGenerationBackendService {
	return &ImageGenerationBackendService{
		ml:        ml,
		bcl:       bcl,
		appConfig: appConfig,
	}
}

func (igbs *ImageGenerationBackendService) GenerateImage(request *schema.OpenAIRequest) <-chan concurrency.ErrorOr[*schema.OpenAIResponse] {
	resultChannel := make(chan concurrency.ErrorOr[*schema.OpenAIResponse])
	go func(request *schema.OpenAIRequest) {
		bc, request, err := igbs.bcl.LoadBackendConfigForModelAndOpenAIRequest(request.Model, request, igbs.appConfig)
		if err != nil {
			resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
			close(resultChannel)
			return
		}

		src := ""
		if request.File != "" {

			var fileData []byte
			// check if input.File is an URL, if so download it and save it
			// to a temporary file
			if strings.HasPrefix(request.File, "http://") || strings.HasPrefix(request.File, "https://") {
				out, err := downloadFile(request.File)
				if err != nil {
					resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: fmt.Errorf("failed downloading file:%w", err)}
					close(resultChannel)
					return
				}
				defer os.RemoveAll(out)

				fileData, err = os.ReadFile(out)
				if err != nil {
					resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: fmt.Errorf("failed reading file:%w", err)}
					close(resultChannel)
					return
				}

			} else {
				// base 64 decode the file and write it somewhere
				// that we will cleanup
				fileData, err = base64.StdEncoding.DecodeString(request.File)
				if err != nil {
					resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
					close(resultChannel)
					return
				}
			}

			// Create a temporary file
			outputFile, err := os.CreateTemp(igbs.appConfig.ImageDir, "b64")
			if err != nil {
				resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
				close(resultChannel)
				return
			}
			// write the base64 result
			writer := bufio.NewWriter(outputFile)
			_, err = writer.Write(fileData)
			if err != nil {
				outputFile.Close()
				resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
				close(resultChannel)
				return
			}
			outputFile.Close()
			src = outputFile.Name()
			defer os.RemoveAll(src)
		}

		log.Debug().Msgf("Parameter Config: %+v", bc)

		switch bc.Backend {
		case "stablediffusion":
			bc.Backend = model.StableDiffusionBackend
		case "tinydream":
			bc.Backend = model.TinyDreamBackend
		case "":
			bc.Backend = model.StableDiffusionBackend
			if bc.Model == "" {
				bc.Model = "stablediffusion_assets" // TODO: check?
			}
		}

		sizeParts := strings.Split(request.Size, "x")
		if len(sizeParts) != 2 {
			resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: fmt.Errorf("invalid value for 'size'")}
			close(resultChannel)
			return
		}
		width, err := strconv.Atoi(sizeParts[0])
		if err != nil {
			resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: fmt.Errorf("invalid value for 'size'")}
			close(resultChannel)
			return
		}
		height, err := strconv.Atoi(sizeParts[1])
		if err != nil {
			resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: fmt.Errorf("invalid value for 'size'")}
			close(resultChannel)
			return
		}

		b64JSON := false
		if request.ResponseFormat.Type == "b64_json" {
			b64JSON = true
		}
		// src and clip_skip
		var result []schema.Item
		for _, i := range bc.PromptStrings {
			n := request.N
			if request.N == 0 {
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
				step := bc.Step
				if step == 0 {
					step = 15
				}

				if request.Mode != 0 {
					mode = request.Mode
				}

				if request.Step != 0 {
					step = request.Step
				}

				tempDir := ""
				if !b64JSON {
					tempDir = igbs.appConfig.ImageDir
				}
				// Create a temporary file
				outputFile, err := os.CreateTemp(tempDir, "b64")
				if err != nil {
					resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
					close(resultChannel)
					return
				}
				outputFile.Close()
				output := outputFile.Name() + ".png"
				// Rename the temporary file
				err = os.Rename(outputFile.Name(), output)
				if err != nil {
					resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
					close(resultChannel)
					return
				}

				if request.Seed == nil {
					zVal := 0 // Idiomatic way to do this? Actually needed?
					request.Seed = &zVal
				}

				fn, err := imageGeneration(height, width, mode, step, *request.Seed, positive_prompt, negative_prompt, src, output, igbs.ml, bc, igbs.appConfig)
				if err != nil {
					resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
					close(resultChannel)
					return
				}
				if err := fn(); err != nil {
					resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
					close(resultChannel)
					return
				}

				item := &schema.Item{}

				if b64JSON {
					defer os.RemoveAll(output)
					data, err := os.ReadFile(output)
					if err != nil {
						resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
						close(resultChannel)
						return
					}
					item.B64JSON = base64.StdEncoding.EncodeToString(data)
				} else {
					base := filepath.Base(output)
					item.URL = igbs.BaseUrlForGeneratedImages + base
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
		resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Value: resp}
		close(resultChannel)
	}(request)
	return resultChannel
}

func imageGeneration(height, width, mode, step, seed int, positive_prompt, negative_prompt, src, dst string, loader *model.ModelLoader, backendConfig *config.BackendConfig, appConfig *config.ApplicationConfig) (func() error, error) {

	threads := backendConfig.Threads
	if *threads == 0 && appConfig.Threads != 0 {
		threads = &appConfig.Threads
	}

	gRPCOpts := gRPCModelOpts(backendConfig)

	opts := modelOpts(backendConfig, appConfig, []model.Option{
		model.WithBackendString(backendConfig.Backend),
		model.WithAssetDir(appConfig.AssetsDestination),
		model.WithThreads(uint32(*threads)),
		model.WithContext(appConfig.Context),
		model.WithModel(backendConfig.Model),
		model.WithLoadGRPCLoadModelOpts(gRPCOpts),
	})

	inferenceModel, err := loader.BackendLoader(
		opts...,
	)
	if err != nil {
		return nil, err
	}

	fn := func() error {
		_, err := inferenceModel.GenerateImage(
			appConfig.Context,
			&proto.GenerateImageRequest{
				Height:           int32(height),
				Width:            int32(width),
				Mode:             int32(mode),
				Step:             int32(step),
				Seed:             int32(seed),
				CLIPSkip:         int32(backendConfig.Diffusers.ClipSkip),
				PositivePrompt:   positive_prompt,
				NegativePrompt:   negative_prompt,
				Dst:              dst,
				Src:              src,
				EnableParameters: backendConfig.Diffusers.EnableParameters,
			})
		return err
	}

	return fn, nil
}

// TODO: Replace this function with pkg/downloader - no reason to have a (crappier) bespoke download file fn here, but get things working before that change.
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
