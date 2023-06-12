package apiv2

import (
	"context"
	"fmt"
	"strings"

	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/mitchellh/mapstructure"
	"github.com/rs/zerolog/log"
)

type LocalAIServer struct {
	configManager *ConfigManager
	loader        *model.ModelLoader
	engine        *LocalAIEngine
}

func combineRequestAndConfig[RequestType any](configManager *ConfigManager, model string, requestFromInput *RequestType) (*SpecificConfig[RequestType], error) {

	splitFnName := strings.Split(printCurrentFunctionName(2), ".")

	endpointName := splitFnName[len(splitFnName)-1]

	lookup := ConfigRegistration{Model: model, Endpoint: endpointName}

	config, exists := configManager.GetConfig(lookup)

	if !exists {
		return nil, fmt.Errorf("config not found for %+v", lookup)
	}

	request, ok := config.GetRequestDefaults().(RequestType)

	if !ok {
		return nil, fmt.Errorf("Config failed casting for %+v", lookup)
	}

	// configMergingConfig := GetConfigMergingDecoderConfig(&request)
	// configMergingDecoder, err := mapstructure.NewDecoder(&configMergingConfig)

	// if err != nil {
	// 	return nil, err
	// }

	// configMergingDecoder.Decode(requestFromInput)

	// TODO try decoding hooks again later. For testing, do a stupid copy
	decodeErr := mapstructure.Decode(structToStrippedMap(*requestFromInput), &request)

	if decodeErr != nil {
		return nil, decodeErr
	}

	return &SpecificConfig[RequestType]{
		ConfigStub: ConfigStub{
			Registration:  config.GetRegistration(),
			LocalSettings: config.GetLocalSettings(),
		},
		RequestDefaults: request,
	}, nil
}

// CancelFineTune implements StrictServerInterface
func (*LocalAIServer) CancelFineTune(ctx context.Context, request CancelFineTuneRequestObject) (CancelFineTuneResponseObject, error) {
	panic("unimplemented")
}

// CreateChatCompletion implements StrictServerInterface
func (las *LocalAIServer) CreateChatCompletion(ctx context.Context, request CreateChatCompletionRequestObject) (CreateChatCompletionResponseObject, error) {

	chatRequestConfig, err := combineRequestAndConfig(las.configManager, request.Body.Model, request.Body)

	if err != nil {
		return nil, fmt.Errorf("errpr during CreateChatCompletion, failed to combineRequestAndConfig: %w", err)
	}

	predict, err := las.engine.GetModelPredictionFunction(chatRequestConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to GetModelPredictionFunction: %w", err)
	}

	predictions, err := predict()
	if err != nil {
		return nil, fmt.Errorf("error during CreateChatCompletion calling model prediction function: %w", err)
	}

	resp := CreateChatCompletion200JSONResponse{}

	// People who know golang better: is there a cleaner way to do this kind of nil-safe init?
	var responseRole ChatCompletionResponseMessageRole = "asssistant" // Fallback on a reasonable guess
	ext := chatRequestConfig.GetRequest().XLocalaiExtensions
	if ext != nil {
		extr := ext.Roles
		if extr != nil {
			if extr.Assistant != nil {
				responseRole = ChatCompletionResponseMessageRole(*extr.Assistant) // Call for help here too - this really seems dirty. How should this be expressed?
			}
		}
	}

	for i, prediction := range predictions {
		resp.Choices = append(resp.Choices, CreateChatCompletionResponseChoice{
			Message: &ChatCompletionResponseMessage{
				Content: prediction,
				Role:    responseRole,
			},
			Index: &i,
		})
	}

	return resp, nil
}

// CreateCompletion implements StrictServerInterface
func (las *LocalAIServer) CreateCompletion(ctx context.Context, request CreateCompletionRequestObject) (CreateCompletionResponseObject, error) {

	modelName := request.Body.Model

	config, err := combineRequestAndConfig(las.configManager, modelName, request.Body)

	if err != nil {
		return nil, fmt.Errorf("[CreateCompletion] error in combineRequestAndConfig %w", err)
	}

	predict, err := las.engine.GetModelPredictionFunction(config, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to GetModelPredictionFunction: %w", err)
	}

	predictions, err := predict()
	if err != nil {
		return nil, fmt.Errorf("error during CreateChatCompletion calling model prediction function: %w", err)
	}

	log.Debug().Msgf("[CreateCompletion] predict() completed, %d", len(predictions))

	var choices []CreateCompletionResponseChoice
	for i, prediction := range predictions {
		log.Debug().Msgf("[CreateCompletion]%d: %s", i, prediction)
		choices = append(choices, CreateCompletionResponseChoice{
			Index: &i,
			Text:  &prediction,
			// TODO more?
		})
	}

	return CreateCompletion200JSONResponse{
		Model:   modelName,
		Choices: choices,
		// Usage need to be fixed in yaml
	}, nil
}

// CreateEdit implements StrictServerInterface
func (*LocalAIServer) CreateEdit(ctx context.Context, request CreateEditRequestObject) (CreateEditResponseObject, error) {
	panic("unimplemented")
}

// CreateEmbedding implements StrictServerInterface
func (*LocalAIServer) CreateEmbedding(ctx context.Context, request CreateEmbeddingRequestObject) (CreateEmbeddingResponseObject, error) {
	panic("unimplemented")
}

// CreateFile implements StrictServerInterface
func (*LocalAIServer) CreateFile(ctx context.Context, request CreateFileRequestObject) (CreateFileResponseObject, error) {
	panic("unimplemented")
}

// CreateFineTune implements StrictServerInterface
func (*LocalAIServer) CreateFineTune(ctx context.Context, request CreateFineTuneRequestObject) (CreateFineTuneResponseObject, error) {
	panic("unimplemented")
}

// CreateImage implements StrictServerInterface
func (*LocalAIServer) CreateImage(ctx context.Context, request CreateImageRequestObject) (CreateImageResponseObject, error) {
	panic("unimplemented")
}

// CreateImageEdit implements StrictServerInterface
func (*LocalAIServer) CreateImageEdit(ctx context.Context, request CreateImageEditRequestObject) (CreateImageEditResponseObject, error) {
	panic("unimplemented")
}

// CreateImageVariation implements StrictServerInterface
func (*LocalAIServer) CreateImageVariation(ctx context.Context, request CreateImageVariationRequestObject) (CreateImageVariationResponseObject, error) {
	panic("unimplemented")
}

// CreateModeration implements StrictServerInterface
func (*LocalAIServer) CreateModeration(ctx context.Context, request CreateModerationRequestObject) (CreateModerationResponseObject, error) {
	panic("unimplemented")
}

// CreateTranscription implements StrictServerInterface
func (*LocalAIServer) CreateTranscription(ctx context.Context, request CreateTranscriptionRequestObject) (CreateTranscriptionResponseObject, error) {
	panic("unimplemented")
}

// CreateTranslation implements StrictServerInterface
func (*LocalAIServer) CreateTranslation(ctx context.Context, request CreateTranslationRequestObject) (CreateTranslationResponseObject, error) {
	panic("unimplemented")
}

// DeleteFile implements StrictServerInterface
func (*LocalAIServer) DeleteFile(ctx context.Context, request DeleteFileRequestObject) (DeleteFileResponseObject, error) {
	panic("unimplemented")
}

// DeleteModel implements StrictServerInterface
func (*LocalAIServer) DeleteModel(ctx context.Context, request DeleteModelRequestObject) (DeleteModelResponseObject, error) {
	panic("unimplemented")
}

// DownloadFile implements StrictServerInterface
func (*LocalAIServer) DownloadFile(ctx context.Context, request DownloadFileRequestObject) (DownloadFileResponseObject, error) {
	panic("unimplemented")
}

// ListFiles implements StrictServerInterface
func (*LocalAIServer) ListFiles(ctx context.Context, request ListFilesRequestObject) (ListFilesResponseObject, error) {
	panic("unimplemented")
}

// ListFineTuneEvents implements StrictServerInterface
func (*LocalAIServer) ListFineTuneEvents(ctx context.Context, request ListFineTuneEventsRequestObject) (ListFineTuneEventsResponseObject, error) {
	panic("unimplemented")
}

// ListFineTunes implements StrictServerInterface
func (*LocalAIServer) ListFineTunes(ctx context.Context, request ListFineTunesRequestObject) (ListFineTunesResponseObject, error) {
	panic("unimplemented")
}

// ListModels implements StrictServerInterface
func (*LocalAIServer) ListModels(ctx context.Context, request ListModelsRequestObject) (ListModelsResponseObject, error) {
	panic("unimplemented")
}

// RetrieveFile implements StrictServerInterface
func (*LocalAIServer) RetrieveFile(ctx context.Context, request RetrieveFileRequestObject) (RetrieveFileResponseObject, error) {
	panic("unimplemented")
}

// RetrieveFineTune implements StrictServerInterface
func (*LocalAIServer) RetrieveFineTune(ctx context.Context, request RetrieveFineTuneRequestObject) (RetrieveFineTuneResponseObject, error) {
	panic("unimplemented")
}

// RetrieveModel implements StrictServerInterface
func (*LocalAIServer) RetrieveModel(ctx context.Context, request RetrieveModelRequestObject) (RetrieveModelResponseObject, error) {
	panic("unimplemented")
}

var _ StrictServerInterface = (*LocalAIServer)(nil)

// var _ ServerInterface = NewStrictHandler((*LocalAIServer)(nil), nil)
