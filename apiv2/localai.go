package apiv2

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"

	"github.com/mitchellh/mapstructure"
)

type LocalAIServer struct {
	configManager *ConfigManager
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ModelOnlyRequest struct {
	Model string `json:"model" yaml:"model"`
}

// This function grabs the name of the function that calls it, skipping up the callstack `skip` levels.
// This is probably a go war crime, but NJ method and all. It's an awesome way to index EndpointConfigMap
func printCurrentFunctionName(skip int) string {
	pc, _, _, _ := runtime.Caller(skip)
	funcName := runtime.FuncForPC(pc).Name()
	fmt.Println("Current function:", funcName)
	return funcName
}

func sendError(w http.ResponseWriter, code int, message string) {
	localAiError := Error{
		Code:    code,
		Message: message,
	}
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(localAiError)
}

// TODO: Is it a good idea to return "" in cases where the model isn't provided?
// Or is that actually an error condition?
// NO is a decent guess as any to start with?
// r *http.Request
func (server *LocalAIServer) getRequestModelName(body []byte) string {
	var modelOnlyRequest = ModelOnlyRequest{}
	if err := json.Unmarshal(body, &modelOnlyRequest); err != nil {
		fmt.Printf("ERR in getRequestModelName, %+v", err)
		return ""
	}
	return modelOnlyRequest.Model
}

func (server *LocalAIServer) combineRequestAndConfig(endpointName string, body []byte) (interface{}, error) {
	model := server.getRequestModelName(body)

	lookup := ConfigRegistration{Model: model, Endpoint: endpointName}

	config, exists := server.configManager.GetConfig(lookup)

	if !exists {
		return nil, fmt.Errorf("Config not found for %+v", lookup)
	}

	// fmt.Printf("Model: %s\nConfig: %+v\n", model, config)

	request := config.GetRequestDefaults()
	// fmt.Printf("BEFORE rD: %T\n%+v\n\n", request, request)
	tmpUnmarshal := map[string]interface{}{}
	if err := json.Unmarshal(body, &tmpUnmarshal); err != nil {
		return nil, fmt.Errorf("error unmarshalling json to temp map\n%w", err)
	}
	// fmt.Printf("$$$ tmpUnmarshal: %+v\n", tmpUnmarshal)
	mapstructure.Decode(tmpUnmarshal, &request)
	fmt.Printf("AFTER rD: %T\n%+v\n\n", request, request)
	return request, nil
}

func (server *LocalAIServer) getRequest(w http.ResponseWriter, r *http.Request) (interface{}, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		sendError(w, http.StatusBadRequest, "Failed to read body")
	}

	splitFnName := strings.Split(printCurrentFunctionName(2), ".")

	endpointName := splitFnName[len(splitFnName)-1]

	return server.combineRequestAndConfig(endpointName, body)
}

// CancelFineTune implements ServerInterface
func (*LocalAIServer) CancelFineTune(w http.ResponseWriter, r *http.Request, fineTuneId string) {
	panic("unimplemented")
}

// CreateChatCompletion implements ServerInterface
func (server *LocalAIServer) CreateChatCompletion(w http.ResponseWriter, r *http.Request) {
	fmt.Println("HIT APIv2 CreateChatCompletion!")

	request, err := server.getRequest(w, r)

	if err != nil {
		sendError(w, http.StatusBadRequest, err.Error())
	}

	// fmt.Printf("\n!!! Survived to attempt cast. BEFORE:\n\tType: %T\n\t%+v", request, request)

	chatRequest, castSuccess := request.(CreateChatCompletionRequest)

	if !castSuccess {
		sendError(w, http.StatusInternalServerError, "Cast Fail???")
		return
	}

	fmt.Printf("\n\n!! AFTER !!\ntemperature %f\n top_p %f \n %d\n", *chatRequest.Temperature, *chatRequest.TopP, *chatRequest.XLocalaiExtensions.TopK)

	fmt.Printf("chatRequest: %+v\nlen(messages): %d", chatRequest, len(chatRequest.Messages))
	for i, m := range chatRequest.Messages {
		fmt.Printf("message #%d: %+v", i, m)
	}
}

// switch chatRequest := requestDefault.(type) {
// case CreateChatCompletionRequest:

// CreateCompletion implements ServerInterface
func (*LocalAIServer) CreateCompletion(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateEdit implements ServerInterface
func (*LocalAIServer) CreateEdit(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateEmbedding implements ServerInterface
func (*LocalAIServer) CreateEmbedding(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateFile implements ServerInterface
func (*LocalAIServer) CreateFile(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateFineTune implements ServerInterface
func (*LocalAIServer) CreateFineTune(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateImage implements ServerInterface
func (*LocalAIServer) CreateImage(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateImageEdit implements ServerInterface
func (*LocalAIServer) CreateImageEdit(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateImageVariation implements ServerInterface
func (*LocalAIServer) CreateImageVariation(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateModeration implements ServerInterface
func (*LocalAIServer) CreateModeration(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateTranscription implements ServerInterface
func (*LocalAIServer) CreateTranscription(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateTranslation implements ServerInterface
func (*LocalAIServer) CreateTranslation(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// DeleteFile implements ServerInterface
func (*LocalAIServer) DeleteFile(w http.ResponseWriter, r *http.Request, fileId string) {
	panic("unimplemented")
}

// DeleteModel implements ServerInterface
func (*LocalAIServer) DeleteModel(w http.ResponseWriter, r *http.Request, model string) {
	panic("unimplemented")
}

// DownloadFile implements ServerInterface
func (*LocalAIServer) DownloadFile(w http.ResponseWriter, r *http.Request, fileId string) {
	panic("unimplemented")
}

// ListFiles implements ServerInterface
func (*LocalAIServer) ListFiles(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// ListFineTuneEvents implements ServerInterface
func (*LocalAIServer) ListFineTuneEvents(w http.ResponseWriter, r *http.Request, fineTuneId string, params ListFineTuneEventsParams) {
	panic("unimplemented")
}

// ListFineTunes implements ServerInterface
func (*LocalAIServer) ListFineTunes(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// ListModels implements ServerInterface
func (*LocalAIServer) ListModels(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// RetrieveFile implements ServerInterface
func (*LocalAIServer) RetrieveFile(w http.ResponseWriter, r *http.Request, fileId string) {
	panic("unimplemented")
}

// RetrieveFineTune implements ServerInterface
func (*LocalAIServer) RetrieveFineTune(w http.ResponseWriter, r *http.Request, fineTuneId string) {
	panic("unimplemented")
}

// RetrieveModel implements ServerInterface
func (*LocalAIServer) RetrieveModel(w http.ResponseWriter, r *http.Request, model string) {
	panic("unimplemented")
}

var _ ServerInterface = (*LocalAIServer)(nil)
