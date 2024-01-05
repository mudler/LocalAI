package backend

import (
	"encoding/base64"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/schema"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

func ImageGeneration(height, width, mode, step, seed int, positive_prompt, negative_prompt, src, dst string, loader *model.ModelLoader, c schema.Config, o *schema.StartupOptions) (func() error, error) {

	opts := modelOpts(c, o, []model.Option{
		model.WithBackendString(c.Backend),
		model.WithAssetDir(o.AssetsDestination),
		model.WithThreads(uint32(c.Threads)),
		model.WithContext(o.Context),
		model.WithModel(c.Model),
		model.WithLoadGRPCLoadModelOpts(&proto.ModelOptions{
			CUDA:          c.CUDA || c.Diffusers.CUDA,
			SchedulerType: c.Diffusers.SchedulerType,
			PipelineType:  c.Diffusers.PipelineType,
			CFGScale:      c.Diffusers.CFGScale,
			LoraAdapter:   c.LoraAdapter,
			LoraScale:     c.LoraScale,
			LoraBase:      c.LoraBase,
			IMG2IMG:       c.Diffusers.IMG2IMG,
			CLIPModel:     c.Diffusers.ClipModel,
			CLIPSubfolder: c.Diffusers.ClipSubFolder,
			CLIPSkip:      int32(c.Diffusers.ClipSkip),
			ControlNet:    c.Diffusers.ControlNet,
		}),
		model.WithExternalBackends(o.ExternalGRPCBackends, false),
	})

	inferenceModel, err := loader.BackendLoader(
		opts...,
	)
	if err != nil {
		return nil, err
	}

	fn := func() error {
		_, err := inferenceModel.GenerateImage(
			o.Context,
			&proto.GenerateImageRequest{
				Height:           int32(height),
				Width:            int32(width),
				Mode:             int32(mode),
				Step:             int32(step),
				Seed:             int32(seed),
				CLIPSkip:         int32(c.Diffusers.ClipSkip),
				PositivePrompt:   positive_prompt,
				NegativePrompt:   negative_prompt,
				Dst:              dst,
				Src:              src,
				EnableParameters: c.Diffusers.EnableParameters,
			})
		return err
	}

	return fn, nil
}

func ImageGenerationOpenAIRequest(modelName string, input *schema.OpenAIRequest, cl *services.ConfigLoader, ml *model.ModelLoader, startupOptions *schema.StartupOptions) (*schema.OpenAIResponse, error) {
	id := uuid.New().String()
	created := int(time.Now().Unix())

	if modelName == "" {
		modelName = model.StableDiffusionBackend
	}
	log.Debug().Msgf("Loading model: %+v", modelName)

	config, input, err := ReadConfigFromFileAndCombineWithOpenAIRequest(modelName, input, cl, startupOptions)
	if err != nil {
		return nil, fmt.Errorf("failed reading parameters from request: %w", err)
	}

	src := ""
	if input.File != "" {
		if strings.HasPrefix(input.File, "http://") || strings.HasPrefix(input.File, "https://") {
			src, err = utils.CreateTempFileFromUrl(input.File, "", "image-src")
			if err != nil {
				return nil, fmt.Errorf("failed downloading file:%w", err)
			}
		} else {
			src, err = utils.CreateTempFileFromBase64(input.File, "", "base64-image-src")
			if err != nil {
				return nil, fmt.Errorf("error creating temporary image source file: %w", err)
			}
		}
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
		return nil, fmt.Errorf("invalid value for 'size'")
	}
	width, err := strconv.Atoi(sizeParts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid value for 'size'")
	}
	height, err := strconv.Atoi(sizeParts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid value for 'size'")
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
				tempDir = startupOptions.ImageDir
			}
			// Create a temporary file
			outputFile, err := os.CreateTemp(tempDir, "b64")
			if err != nil {
				return nil, err
			}
			outputFile.Close()
			output := outputFile.Name() + ".png"
			// Rename the temporary file
			err = os.Rename(outputFile.Name(), output)
			if err != nil {
				return nil, err
			}

			fn, err := ImageGeneration(height, width, mode, step, input.Seed, positive_prompt, negative_prompt, src, output, ml, *config, startupOptions)
			if err != nil {
				return nil, err
			}
			if err := fn(); err != nil {
				return nil, err
			}

			item := &schema.Item{}

			if b64JSON {
				defer os.RemoveAll(output)
				data, err := os.ReadFile(output)
				if err != nil {
					return nil, err
				}
				item.B64JSON = base64.StdEncoding.EncodeToString(data)
			} else {
				base := filepath.Base(output)
				item.URL = path.Join(startupOptions.ImageDir, base)
			}

			result = append(result, *item)
		}
	}

	return &schema.OpenAIResponse{
		ID:      id,
		Created: created,
		Data:    result,
	}, nil
}
