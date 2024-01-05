package grpc

import (
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/schema"
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
	AudioTranscription(*pb.TranscriptRequest) (schema.WhisperResult, error)
	TTS(*pb.TTSRequest) error
	TokenizeString(*pb.PredictOptions) (pb.TokenizationResponse, error)
	Status() (pb.StatusResponse, error)
}

func newReply(s string) *pb.Reply {
	return &pb.Reply{Message: []byte(s)}
}
