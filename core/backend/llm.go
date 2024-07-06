package backend

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
)

type LLMResponse struct {
	Response string // should this be []byte?
	Usage    TokenUsage
}

type TokenUsage struct {
	Prompt     int
	Completion int
}

func ModelInference(ctx context.Context, s string, messages []schema.Message, images []string, loader *model.ModelLoader, c config.BackendConfig, o *config.ApplicationConfig, tokenCallback func(string, TokenUsage) bool) (func() (LLMResponse, error), error) {
	modelFile := c.Model
	threads := c.Threads
	if *threads == 0 && o.Threads != 0 {
		threads = &o.Threads
	}
	grpcOpts := gRPCModelOpts(c)

	var inferenceModel grpc.Backend
	var err error

	opts := modelOpts(c, o, []model.Option{
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
		model.WithThreads(uint32(*threads)), // some models uses this to allocate threads during startup
		model.WithAssetDir(o.AssetsDestination),
		model.WithModel(modelFile),
		model.WithContext(o.Context),
	})

	if c.Backend != "" {
		opts = append(opts, model.WithBackendString(c.Backend))
	}

	// Check if the modelFile exists, if it doesn't try to load it from the gallery
	if o.AutoloadGalleries { // experimental
		if _, err := os.Stat(modelFile); os.IsNotExist(err) {
			utils.ResetDownloadTimers()
			// if we failed to load the model, we try to download it
			err := gallery.InstallModelFromGallery(o.Galleries, modelFile, loader.ModelPath, gallery.GalleryModel{}, utils.DisplayDownloadFunction)
			if err != nil {
				return nil, err
			}
		}
	}

	if c.Backend == "" {
		inferenceModel, err = loader.GreedyLoader(opts...)
	} else {
		inferenceModel, err = loader.BackendLoader(opts...)
	}

	if err != nil {
		return nil, err
	}

	var protoMessages []*proto.Message
	// if we are using the tokenizer template, we need to convert the messages to proto messages
	// unless the prompt has already been tokenized (non-chat endpoints + functions)
	if c.TemplateConfig.UseTokenizerTemplate && s == "" {
		protoMessages = make([]*proto.Message, len(messages), len(messages))
		for i, message := range messages {
			protoMessages[i] = &proto.Message{
				Role: message.Role,
			}
			switch ct := message.Content.(type) {
			case string:
				protoMessages[i].Content = ct
			default:
				return nil, fmt.Errorf("Unsupported type for schema.Message.Content for inference: %T", ct)
			}
		}
	}

	// in GRPC, the backend is supposed to answer to 1 single token if stream is not supported
	fn := func() (LLMResponse, error) {
		opts := gRPCPredictOpts(c, loader.ModelPath)
		opts.Prompt = s
		opts.Messages = protoMessages
		opts.UseTokenizerTemplate = c.TemplateConfig.UseTokenizerTemplate
		opts.Images = images

		tokenUsage := TokenUsage{}

		// check the per-model feature flag for usage, since tokenCallback may have a cost.
		// Defaults to off as for now it is still experimental
		if c.FeatureFlag.Enabled("usage") {
			userTokenCallback := tokenCallback
			if userTokenCallback == nil {
				userTokenCallback = func(token string, usage TokenUsage) bool {
					return true
				}
			}

			promptInfo, pErr := inferenceModel.TokenizeString(ctx, opts)
			if pErr == nil && promptInfo.Length > 0 {
				tokenUsage.Prompt = int(promptInfo.Length)
			}

			tokenCallback = func(token string, usage TokenUsage) bool {
				tokenUsage.Completion++
				return userTokenCallback(token, tokenUsage)
			}
		}

		if tokenCallback != nil {
			ss := ""

			var partialRune []byte
			err := inferenceModel.PredictStream(ctx, opts, func(chars []byte) {
				partialRune = append(partialRune, chars...)

				for len(partialRune) > 0 {
					r, size := utf8.DecodeRune(partialRune)
					if r == utf8.RuneError {
						// incomplete rune, wait for more bytes
						break
					}

					tokenCallback(string(r), tokenUsage)
					ss += string(r)

					partialRune = partialRune[size:]
				}
			})
			return LLMResponse{
				Response: ss,
				Usage:    tokenUsage,
			}, err
		} else {
			// TODO: Is the chicken bit the only way to get here? is that acceptable?
			reply, err := inferenceModel.Predict(ctx, opts)
			if err != nil {
				return LLMResponse{}, err
			}
			if tokenUsage.Prompt == 0 {
				tokenUsage.Prompt = int(reply.PromptTokens)
			}
			if tokenUsage.Completion == 0 {
				tokenUsage.Completion = int(reply.Tokens)
			}
			return LLMResponse{
				Response: string(reply.Message),
				Usage:    tokenUsage,
			}, err
		}
	}

	return fn, nil
}

var cutstrings map[string]*regexp.Regexp = make(map[string]*regexp.Regexp)
var mu sync.Mutex = sync.Mutex{}

func Finetune(config config.BackendConfig, input, prediction string) string {
	if config.Echo {
		prediction = input + prediction
	}

	for _, c := range config.Cutstrings {
		mu.Lock()
		reg, ok := cutstrings[c]
		if !ok {
			cutstrings[c] = regexp.MustCompile(c)
			reg = cutstrings[c]
		}
		mu.Unlock()
		prediction = reg.ReplaceAllString(prediction, "")
	}

	for _, c := range config.TrimSpace {
		prediction = strings.TrimSpace(strings.TrimPrefix(prediction, c))
	}

	for _, c := range config.TrimSuffix {
		prediction = strings.TrimSpace(strings.TrimSuffix(prediction, c))
	}
	return prediction
}
