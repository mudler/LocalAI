package base

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"

	"github.com/go-skynet/LocalAI/pkg/grpc"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/grpc/whisper/api"
)

type Base struct {
}

func (llm *Base) Load(opts *pb.ModelOptions) error {
	return fmt.Errorf("unimplemented")

}

func (llm *Base) Predict(opts *pb.PredictOptions) (string, error) {
	return "", fmt.Errorf("unimplemented")
}

func (llm *Base) PredictStream(opts *pb.PredictOptions, results chan string) error {
	return fmt.Errorf("unimplemented")
}

func (llm *Base) Embeddings(opts *pb.PredictOptions) ([]float32, error) {
	return []float32{}, fmt.Errorf("unimplemented")
}

func (llm *Base) GenerateImage(*pb.GenerateImageRequest) error {
	return fmt.Errorf("unimplemented")
}

func (llm *Base) AudioTranscription(*pb.TranscriptRequest) (api.Result, error) {
	return api.Result{}, fmt.Errorf("unimplemented")
}

func (llm *Base) TTS(*pb.TTSRequest) error {
	return fmt.Errorf("unimplemented")
}

func (llm *Base) TokenizeString(opts *pb.PredictOptions) (grpc.TokenizationResponse, error) {
	return grpc.TokenizationResponse{}, fmt.Errorf("unimplemented")
}
