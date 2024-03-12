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

	tokenUsage := TokenUsage{}

	promptInfo, pErr := inferenceModel.TokenizeString(ctx, grpcPredOpts)
	if pErr == nil && promptInfo.Length > 0 {
		tokenUsage.Prompt = int(promptInfo.Length)
	}

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
