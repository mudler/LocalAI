package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/rs/zerolog/log"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/mudler/LocalAI/core/gallery"
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

func ModelInference(ctx context.Context, s string, messages []schema.Message, images, videos, audios []string, loader *model.ModelLoader, c config.BackendConfig, o *config.ApplicationConfig, tokenCallback func(string, TokenUsage) bool) (func() (LLMResponse, error), error) {
	modelFile := c.Model

	// Check if the modelFile exists, if it doesn't try to load it from the gallery
	if o.AutoloadGalleries { // experimental
		if _, err := os.Stat(modelFile); os.IsNotExist(err) {
			utils.ResetDownloadTimers()
			// if we failed to load the model, we try to download it
			err := gallery.InstallModelFromGallery(o.Galleries, modelFile, loader.ModelPath, gallery.GalleryModel{}, utils.DisplayDownloadFunction, o.EnforcePredownloadScans)
			if err != nil {
				return nil, err
			}
		}
	}

	opts := ModelOptions(c, o)
	inferenceModel, err := loader.Load(opts...)
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
			case []interface{}:
				// If using the tokenizer template, in case of multimodal we want to keep the multimodal content as and return only strings here
				data, _ := json.Marshal(ct)
				resultData := []struct {
					Text string `json:"text"`
				}{}
				json.Unmarshal(data, &resultData)
				for _, r := range resultData {
					protoMessages[i].Content += r.Text
				}
			default:
				return nil, fmt.Errorf("unsupported type for schema.Message.Content for inference: %T", ct)
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
		opts.Videos = videos
		opts.Audios = audios

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
			err := inferenceModel.PredictStream(ctx, opts, func(reply *proto.Reply) {
				msg := reply.Message
				partialRune = append(partialRune, msg...)

				tokenUsage.Prompt = int(reply.PromptTokens)
				tokenUsage.Completion = int(reply.Tokens)

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

				if len(msg) == 0 {
					tokenCallback("", tokenUsage)
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
			r, err := regexp.Compile(c)
			if err != nil {
				log.Fatal().Err(err).Msg("failed to compile regex")
			}
			cutstrings[c] = r
			reg = cutstrings[c]
		}
		mu.Unlock()
		prediction = reg.ReplaceAllString(prediction, "")
	}

	// extract results from the response which can be for instance inside XML tags
	var predResult string
	for _, r := range config.ExtractRegex {
		mu.Lock()
		reg, ok := cutstrings[r]
		if !ok {
			regex, err := regexp.Compile(r)
			if err != nil {
				log.Fatal().Err(err).Msg("failed to compile regex")
			}
			cutstrings[r] = regex
			reg = regex
		}
		mu.Unlock()
		predResult += reg.FindString(prediction)
	}
	if predResult != "" {
		prediction = predResult
	}

	for _, c := range config.TrimSpace {
		prediction = strings.TrimSpace(strings.TrimPrefix(prediction, c))
	}

	for _, c := range config.TrimSuffix {
		prediction = strings.TrimSpace(strings.TrimSuffix(prediction, c))
	}
	return prediction
}
