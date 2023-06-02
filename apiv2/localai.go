package apiv2

import (
	"context"
	"fmt"
	"strings"

	"github.com/mitchellh/mapstructure"
)

type LocalAIServer struct {
	configManager *ConfigManager
}

func combineRequestAndConfig[RequestType any](configManager *ConfigManager, model string, requestFromInput *RequestType) (*RequestType, error) {

	splitFnName := strings.Split(printCurrentFunctionName(2), ".")

	endpointName := splitFnName[len(splitFnName)-1]

	lookup := ConfigRegistration{Model: model, Endpoint: endpointName}

	config, exists := configManager.GetConfig(lookup)

	if !exists {
		return nil, fmt.Errorf("Config not found for %+v", lookup)
	}

	// fmt.Printf("Model: %s\nConfig: %+v\nrequestFromInput: %+v\n", model, config, requestFromInput)

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

	fmt.Printf("AFTER rD: %T\n%+v\n\n", request, request)

	return &request, nil
}

// CancelFineTune implements StrictServerInterface
func (*LocalAIServer) CancelFineTune(ctx context.Context, request CancelFineTuneRequestObject) (CancelFineTuneResponseObject, error) {
	panic("unimplemented")
}

// CreateChatCompletion implements StrictServerInterface
func (las *LocalAIServer) CreateChatCompletion(ctx context.Context, request CreateChatCompletionRequestObject) (CreateChatCompletionResponseObject, error) {

	chatRequest, err := combineRequestAndConfig(las.configManager, request.Body.Model, request.Body)

	if err != nil {
		fmt.Printf("CreateChatCompletion ERROR combining config and input!\n%s\n", err.Error())
		return nil, err
	}

	fmt.Printf("\n===CreateChatCompletion===\n%+v\n", chatRequest)

	fmt.Printf("\n\n!! TYPED CreateChatCompletion !!\ntemperature %f\n top_p %f \n %d\n", *chatRequest.Temperature, *chatRequest.TopP, *chatRequest.XLocalaiExtensions.TopK)

	fmt.Printf("chatRequest: %+v\nlen(messages): %d", chatRequest, len(chatRequest.Messages))
	for i, m := range chatRequest.Messages {
		fmt.Printf("message #%d: %+v", i, m)
	}

	return CreateChatCompletion200JSONResponse{}, nil

	// panic("unimplemented")
}

// CreateCompletion implements StrictServerInterface
func (*LocalAIServer) CreateCompletion(ctx context.Context, request CreateCompletionRequestObject) (CreateCompletionResponseObject, error) {
	panic("unimplemented")
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
