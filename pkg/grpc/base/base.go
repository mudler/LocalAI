package base

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"
	"sync"

	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/grpc/whisper/api"
)

type Base struct {
	backendBusy sync.Mutex
	State       pb.StateResponse_State
}

func (llm *Base) Busy() bool {
	r := llm.backendBusy.TryLock()
	if r {
		llm.backendBusy.Unlock()
	}
	return r
}

func (llm *Base) Lock() {
	llm.backendBusy.Lock()
	llm.State = pb.StateResponse_BUSY
}

func (llm *Base) Unlock() {
	llm.State = pb.StateResponse_READY
	llm.backendBusy.Unlock()
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

func (llm *Base) TokenizeString(opts *pb.PredictOptions) (pb.TokenizationResponse, error) {
	return pb.TokenizationResponse{}, fmt.Errorf("unimplemented")
}

func (llm *Base) Status() (pb.StateResponse, error) {
	return pb.StateResponse{
		State: llm.State,
		// 0-value for memory to indicate that we didn't even attempt?
	}, nil
}
