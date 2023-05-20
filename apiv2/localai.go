package apiv2

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type LocalAIServer struct {
}

var _ ServerInterface = (*LocalAIServer)(nil)

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func sendError(w http.ResponseWriter, code int, message string) {
	localAiError := Error{
		Code:    code,
		Message: message,
	}
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(localAiError)
}

// It won't work, but it's worth a try.
const nyiErrorMessageFormatString = "%s is not yet implemented by LocalAI\nThere is no need to contact support about this error and retrying will not help.\nExpect an update at https://github.com/go-skynet/LocalAI if this changes!"

// Do we want or need an additional "wontfix" template that is even stronger than this?
const nyiDepreciatedErrorMessageFormatString = "%s is a depreciated portion of the OpenAI API, and is not yet implemented by LocalAI\nThere is no need to contact support about this error and retrying will not help."

// CancelFineTune implements ServerInterface
func (*LocalAIServer) CancelFineTune(w http.ResponseWriter, r *http.Request, fineTuneId string) {
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "Fine Tune"))
	return
}

// CreateAnswer implements ServerInterface
func (*LocalAIServer) CreateAnswer(w http.ResponseWriter, r *http.Request) {
	sendError(w, 501, fmt.Sprintf(nyiDepreciatedErrorMessageFormatString, "CreateAnswer"))
	return
}

// CreateChatCompletion implements ServerInterface
func (*LocalAIServer) CreateChatCompletion(w http.ResponseWriter, r *http.Request) {
	var chatRequest CreateChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&chatRequest); err != nil {
		sendError(w, http.StatusBadRequest, "Invalid CreateChatCompletionRequest")
		return
	}
}

// CreateClassification implements ServerInterface
func (*LocalAIServer) CreateClassification(w http.ResponseWriter, r *http.Request) {
	sendError(w, 501, fmt.Sprintf(nyiDepreciatedErrorMessageFormatString, "CreateClassification"))
	return
}

// CreateCompletion implements ServerInterface
func (*LocalAIServer) CreateCompletion(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateEdit implements ServerInterface
func (*LocalAIServer) CreateEdit(w http.ResponseWriter, r *http.Request) {
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "CreateEdit"))
	return
}

// CreateEmbedding implements ServerInterface
func (*LocalAIServer) CreateEmbedding(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// CreateFile implements ServerInterface
func (*LocalAIServer) CreateFile(w http.ResponseWriter, r *http.Request) {
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "Create File"))
	return
}

// CreateFineTune implements ServerInterface
func (*LocalAIServer) CreateFineTune(w http.ResponseWriter, r *http.Request) {
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "Create Fine Tune"))
	return
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
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "CreateModeration"))
	return
}

// CreateSearch implements ServerInterface
func (*LocalAIServer) CreateSearch(w http.ResponseWriter, r *http.Request, engineId string) {
	sendError(w, 501, fmt.Sprintf(nyiDepreciatedErrorMessageFormatString, "CreateSearch"))
	return
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
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "DeleteFile"))
	return
}

// DeleteModel implements ServerInterface
func (*LocalAIServer) DeleteModel(w http.ResponseWriter, r *http.Request, model string) {
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "DeleteModel"))
	return
}

// DownloadFile implements ServerInterface
func (*LocalAIServer) DownloadFile(w http.ResponseWriter, r *http.Request, fileId string) {
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "DownloadFile"))
	return
}

// ListEngines implements ServerInterface
func (*LocalAIServer) ListEngines(w http.ResponseWriter, r *http.Request) {
	sendError(w, 501, fmt.Sprintf(nyiDepreciatedErrorMessageFormatString, "List Engines"))
	return
}

// ListFiles implements ServerInterface
func (*LocalAIServer) ListFiles(w http.ResponseWriter, r *http.Request) {
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "ListFiles"))
	return
}

// ListFineTuneEvents implements ServerInterface
func (*LocalAIServer) ListFineTuneEvents(w http.ResponseWriter, r *http.Request, fineTuneId string, params ListFineTuneEventsParams) {
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "List Fine Tune Events"))
	return
}

// ListFineTunes implements ServerInterface
func (*LocalAIServer) ListFineTunes(w http.ResponseWriter, r *http.Request) {
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "List Fine Tunes"))
	return
}

// ListModels implements ServerInterface
func (*LocalAIServer) ListModels(w http.ResponseWriter, r *http.Request) {
	panic("unimplemented")
}

// RetrieveEngine implements ServerInterface
func (*LocalAIServer) RetrieveEngine(w http.ResponseWriter, r *http.Request, engineId string) {
	sendError(w, 501, fmt.Sprintf(nyiDepreciatedErrorMessageFormatString, "RetrieveEngine"))
	return
}

// RetrieveFile implements ServerInterface
func (*LocalAIServer) RetrieveFile(w http.ResponseWriter, r *http.Request, fileId string) {
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "RetrieveFile"))
	return
}

// RetrieveFineTune implements ServerInterface
func (*LocalAIServer) RetrieveFineTune(w http.ResponseWriter, r *http.Request, fineTuneId string) {
	sendError(w, 501, fmt.Sprintf(nyiErrorMessageFormatString, "Retrieve Fine Tune"))
	return
}

// RetrieveModel implements ServerInterface
func (*LocalAIServer) RetrieveModel(w http.ResponseWriter, r *http.Request, model string) {
	panic("unimplemented")
}
