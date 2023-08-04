package grpc

import (
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/grpc/whisper/api"
)

type TokenizationResponse struct {
	Length int      `json:"length"`
	Tokens []string `json:"tokens"`
}

type LLM interface {
	Predict(*pb.PredictOptions) (string, error)
	PredictStream(*pb.PredictOptions, chan string) error
	Load(*pb.ModelOptions) error
	Embeddings(*pb.PredictOptions) ([]float32, error)
	GenerateImage(*pb.GenerateImageRequest) error
	AudioTranscription(*pb.TranscriptRequest) (api.Result, error)
	TTS(*pb.TTSRequest) error
	TokenizeString(*pb.PredictOptions) (TokenizationResponse, error)
}

func newReply(s string) *pb.Reply {
	return &pb.Reply{Message: []byte(s)}
}
