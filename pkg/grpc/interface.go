package grpc

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
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
	AudioTranscription(*pb.TranscriptRequest) (pb.TranscriptResult, error)
	TTS(*pb.TTSRequest) error
	SoundGeneration(*pb.SoundGenerationRequest) error
	TokenizeString(*pb.PredictOptions) (pb.TokenizationResponse, error)
	Status() (pb.StatusResponse, error)

	StoresSet(*pb.StoresSetOptions) error
	StoresDelete(*pb.StoresDeleteOptions) error
	StoresGet(*pb.StoresGetOptions) (pb.StoresGetResult, error)
	StoresFind(*pb.StoresFindOptions) (pb.StoresFindResult, error)

	VAD(*pb.VADRequest) (pb.VADResponse, error)
}

func newReply(s string) *pb.Reply {
	return &pb.Reply{Message: []byte(s)}
}
