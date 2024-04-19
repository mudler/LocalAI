package grpc

import (
	"github.com/go-skynet/LocalAI/core/schema"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
)

type LLM interface {
	Busy() bool
	Lock()
	Unlock()
	Locking() bool
	Predict(*pb.PredictOptions) (string, error)
	PredictStream(*pb.PredictOptions, chan string) error
	Load(*pb.ModelOptions) error
	Embeddings(*pb.PredictOptions) ([]float32, error)
	GenerateImage(*pb.GenerateImageRequest) error
	AudioTranscription(*pb.TranscriptRequest) (schema.TranscriptionResult, error)
	TTS(*pb.TTSRequest) error
	TokenizeString(*pb.PredictOptions) (pb.TokenizationResponse, error)
	Status() (pb.StatusResponse, error)

	StoresSet(*pb.StoresSetOptions) error
	StoresDelete(*pb.StoresDeleteOptions) error
	StoresGet(*pb.StoresGetOptions) (pb.StoresGetResult, error)
	StoresFind(*pb.StoresFindOptions) (pb.StoresFindResult, error)
}

func newReply(s string) *pb.Reply {
	return &pb.Reply{Message: []byte(s)}
}
