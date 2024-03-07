package backend

import (
	"context"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/rs/zerolog/log"

	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/grpc"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/utils"
)

type LLMRequest struct {
	Id     int // TODO Remove if not used.
	Text   string
	Images []string
	// TODO: Other Modalities?
}

type TokenUsage struct {
	Prompt     int
	Completion int
}

type LLMResponse struct {
	Request  *LLMRequest
	Response string // should this be []byte?
	Usage    TokenUsage
}

// TODO: Does this belong here or in core/services/openai.go?
type LLMResponseBundle struct {
	Request  *schema.OpenAIRequest
	Response []schema.Choice
	Usage    TokenUsage
}

type LLMBackendService struct {
	bcl        *config.BackendConfigLoader
	ml         *model.ModelLoader
	appConfig  *config.ApplicationConfig
	ftMutex    sync.Mutex
	cutstrings map[string]*regexp.Regexp
}

func NewLLMBackendService(ml *model.ModelLoader, bcl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) *LLMBackendService {
	return &LLMBackendService{
		bcl:        bcl,
		ml:         ml,
		appConfig:  appConfig,
		ftMutex:    sync.Mutex{},
		cutstrings: make(map[string]*regexp.Regexp),
	}
}

// TODO: Should ctx param be removed and replaced with hardcoded req.Context?
func (llmbs *LLMBackendService) Inference(ctx context.Context, req *LLMRequest, bc *config.BackendConfig, enableTokenChannel bool) (
	resultChannel <-chan utils.ErrorOr[*LLMResponse], tokenChannel <-chan utils.ErrorOr[*LLMResponse], err error) {

	threads := bc.Threads
	if threads == 0 && llmbs.appConfig.Threads != 0 {
		threads = llmbs.appConfig.Threads
	}

	grpcOpts := gRPCModelOpts(bc)

	var inferenceModel grpc.Backend

	opts := modelOpts(bc, llmbs.appConfig, []model.Option{
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
		model.WithThreads(uint32(threads)), // some models uses this to allocate threads during startup
		model.WithAssetDir(llmbs.appConfig.AssetsDestination),
		model.WithModel(bc.Model),
		model.WithContext(llmbs.appConfig.Context),
	})

	if bc.Backend != "" {
		opts = append(opts, model.WithBackendString(bc.Backend))
	}

	// Check if bc.Model exists, if it doesn't try to load it from the gallery
	if llmbs.appConfig.AutoloadGalleries { // experimental
		if _, err := os.Stat(bc.Model); os.IsNotExist(err) {
			utils.ResetDownloadTimers()
			// if we failed to load the model, we try to download it
			err := gallery.InstallModelFromGalleryByName(llmbs.appConfig.Galleries, bc.Model, llmbs.appConfig.ModelPath, gallery.GalleryModel{}, utils.DisplayDownloadFunction)
			if err != nil {
				return nil, nil, err
			}
		}
	}

	if bc.Backend == "" {
		inferenceModel, err = llmbs.ml.GreedyLoader(opts...)
	} else {
		inferenceModel, err = llmbs.ml.BackendLoader(opts...)
	}

	if err != nil {
		return nil, nil, err
	}

	grpcPredOpts := gRPCPredictOpts(bc, llmbs.appConfig.ModelPath)
	grpcPredOpts.Prompt = req.Text
	grpcPredOpts.Images = req.Images

	log.Debug().Msgf("LLMBS Inference grpcPredOpts: %+v", grpcPredOpts)

	tokenUsage := TokenUsage{}

	promptInfo, pErr := inferenceModel.TokenizeString(ctx, grpcPredOpts)
	if pErr == nil && promptInfo.Length > 0 {
		tokenUsage.Prompt = int(promptInfo.Length)
	}

	log.Debug().Msgf("LLMBS Inference tokenized: %+v", promptInfo)

	rawResultChannel := make(chan utils.ErrorOr[*LLMResponse])
	// TODO this next line is the biggest argument for taking named return values _back_ out!!!
	var rawTokenChannel chan utils.ErrorOr[*LLMResponse]

	if enableTokenChannel {
		rawTokenChannel = make(chan utils.ErrorOr[*LLMResponse])

		// TODO Needs better name
		ss := ""

		go func() {
			var partialRune []byte
			err := inferenceModel.PredictStream(ctx, grpcPredOpts, func(chars []byte) {
				partialRune = append(partialRune, chars...)

				for len(partialRune) > 0 {
					r, size := utf8.DecodeRune(partialRune)
					if r == utf8.RuneError {
						// incomplete rune, wait for more bytes
						break
					}

					tokenUsage.Completion++
					rawTokenChannel <- utils.ErrorOr[*LLMResponse]{Value: &LLMResponse{
						Response: string(r),
						Usage:    tokenUsage,
					}}

					ss += string(r)

					partialRune = partialRune[size:]
				}
			})
			close(rawTokenChannel)
			if err != nil {
				rawResultChannel <- utils.ErrorOr[*LLMResponse]{Error: err}
			} else {
				rawResultChannel <- utils.ErrorOr[*LLMResponse]{Value: &LLMResponse{
					Response: ss,
					Usage:    tokenUsage,
				}}
			}
			close(rawResultChannel)
		}()
	} else {
		go func() {
			reply, err := inferenceModel.Predict(ctx, grpcPredOpts)
			log.Debug().Msgf("LLMBS Inference reply: %+v", reply)
			if err != nil {
				rawResultChannel <- utils.ErrorOr[*LLMResponse]{Error: err}
				close(rawResultChannel)
			} else {
				rawResultChannel <- utils.ErrorOr[*LLMResponse]{Value: &LLMResponse{
					Response: string(reply.Message),
					Usage:    tokenUsage,
				}}
				close(rawResultChannel)
			}
		}()
	}

	resultChannel = rawResultChannel
	tokenChannel = rawTokenChannel
	return
}

// TODO: Should predInput be a seperate param still, or should this fn handle extracting it from request??
func (llmbs *LLMBackendService) GenerateText(predInput string, request *schema.OpenAIRequest, bc *config.BackendConfig,
	mappingFn func(*LLMResponse) schema.Choice, enableCompletionChannels bool, enableTokenChannels bool) (
	// Returns:
	resultChannel <-chan utils.ErrorOr[*LLMResponseBundle], completionChannels []<-chan utils.ErrorOr[*LLMResponse], tokenChannels []<-chan utils.ErrorOr[*LLMResponse], err error) {

	// log.Debug().Msgf("LLMBackendService::GenerateText %+v", request)

	rawChannel := make(chan utils.ErrorOr[*LLMResponseBundle])
	resultChannel = rawChannel

	if request.N == 0 { // number of completions to return
		request.N = 1
	}
	images := []string{}
	for _, m := range request.Messages {
		images = append(images, m.StringImages...)
	}

	for i := 0; i < request.N; i++ {
		individualResultChannel, tokenChannel, err := llmbs.Inference(request.Context, &LLMRequest{
			Text:   predInput,
			Images: images,
		}, bc, enableTokenChannels)
		if err != nil {
			// This is a weird error case, and I'm not entirely sure what best to do to clean up until I'm sure how to trigger it. Best guess here..
			// return nil, nil, nil, err
			continue // TODO if this is the right answer invert conditional
		}
		completionChannels = append(completionChannels, individualResultChannel)
		tokenChannels = append(tokenChannels, tokenChannel)
	}

	go func() {
		// utils.SliceOfChannelsResultSynchronizerFatalErrors(completionChannels, rawChannel, mappingFn)
		initialBundle := LLMResponseBundle{
			Request:  request,
			Response: []schema.Choice{},
			Usage:    TokenUsage{},
		}

		wg := utils.SliceOfChannelsReducer(completionChannels, rawChannel, func(iv utils.ErrorOr[*LLMResponse], ov utils.ErrorOr[*LLMResponseBundle]) utils.ErrorOr[*LLMResponseBundle] {
			if iv.Error != nil {
				ov.Error = iv.Error
				// TODO: Decide if we should wipe partials or not?
				return ov
			}
			ov.Value.Usage.Prompt += iv.Value.Usage.Prompt
			ov.Value.Usage.Completion += iv.Value.Usage.Completion
			ov.Value.Response = append(ov.Value.Response, mappingFn(iv.Value))
			return ov
		}, utils.ErrorOr[*LLMResponseBundle]{Value: &initialBundle})
		wg.Wait()

		close(rawChannel)
	}()
	return
}

func (llmbs *LLMBackendService) ModelInference(ctx context.Context, s string, images []string, bc *config.BackendConfig, tokenCallback func(string, TokenUsage) bool) (func() (LLMResponse, error), error) {
	modelFile := bc.Model

	grpcOpts := gRPCModelOpts(bc)

	var inferenceModel grpc.Backend
	var err error

	opts := modelOpts(bc, llmbs.appConfig, []model.Option{
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
		model.WithThreads(uint32(bc.Threads)), // some models uses this to allocate threads during startup
		model.WithAssetDir(llmbs.appConfig.AssetsDestination),
		model.WithModel(modelFile),
		model.WithContext(llmbs.appConfig.Context),
	})

	if bc.Backend != "" {
		opts = append(opts, model.WithBackendString(bc.Backend))
	}

	// Check if the modelFile exists, if it doesn't try to load it from the gallery
	if llmbs.appConfig.AutoloadGalleries { // experimental
		if _, err := os.Stat(modelFile); os.IsNotExist(err) {
			utils.ResetDownloadTimers()
			// if we failed to load the model, we try to download it
			err := gallery.InstallModelFromGalleryByName(llmbs.appConfig.Galleries, modelFile, llmbs.appConfig.ModelPath, gallery.GalleryModel{}, utils.DisplayDownloadFunction)
			if err != nil {
				return nil, err
			}
		}
	}

	if bc.Backend == "" {
		inferenceModel, err = llmbs.ml.GreedyLoader(opts...)
	} else {
		inferenceModel, err = llmbs.ml.BackendLoader(opts...)
	}

	if err != nil {
		return nil, err
	}

	// in GRPC, the backend is supposed to answer to 1 single token if stream is not supported
	fn := func() (LLMResponse, error) {
		opts := gRPCPredictOpts(bc, llmbs.appConfig.ModelPath)
		opts.Prompt = s
		opts.Images = images

		tokenUsage := TokenUsage{}

		// check the per-model feature flag for usage, since tokenCallback may have a cost.
		// Defaults to off as for now it is still experimental
		if bc.FeatureFlag.Enabled("usage") {
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
			return LLMResponse{
				Response: string(reply.Message),
				Usage:    tokenUsage,
			}, err
		}
	}

	return fn, nil
}

func (llmbs *LLMBackendService) Finetune(config config.BackendConfig, input, prediction string) string {
	if config.Echo {
		prediction = input + prediction
	}

	for _, c := range config.Cutstrings {
		llmbs.ftMutex.Lock()
		reg, ok := llmbs.cutstrings[c]
		if !ok {
			llmbs.cutstrings[c] = regexp.MustCompile(c)
			reg = llmbs.cutstrings[c]
		}
		llmbs.ftMutex.Unlock()
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
