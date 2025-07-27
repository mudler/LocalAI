package base

// This is a wrapper to satisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"
	"os"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	gopsutil "github.com/shirou/gopsutil/v3/process"
)

// Base is a base class for all backends to implement
// Note: the backends that does not support multiple requests
// should use SingleThread instead
type Base struct {
}

func (llm *Base) Locking() bool {
	return false
}

func (llm *Base) Lock() {
	panic("not implemented")
}

func (llm *Base) Unlock() {
	panic("not implemented")
}

func (llm *Base) Busy() bool {
	return false
}

func (llm *Base) Load(opts *pb.ModelOptions) error {
	return fmt.Errorf("unimplemented")
}

func (llm *Base) Predict(opts *pb.PredictOptions) (string, error) {
	return "", fmt.Errorf("unimplemented")
}

func (llm *Base) PredictStream(opts *pb.PredictOptions, results chan string) error {
	close(results)
	return fmt.Errorf("unimplemented")
}

func (llm *Base) Embeddings(opts *pb.PredictOptions) ([]float32, error) {
	return []float32{}, fmt.Errorf("unimplemented")
}

func (llm *Base) GenerateImage(*pb.GenerateImageRequest) error {
	return fmt.Errorf("unimplemented")
}

func (llm *Base) GenerateVideo(*pb.GenerateVideoRequest) error {
	return fmt.Errorf("unimplemented")
}

func (llm *Base) AudioTranscription(*pb.TranscriptRequest) (pb.TranscriptResult, error) {
	return pb.TranscriptResult{}, fmt.Errorf("unimplemented")
}

func (llm *Base) TTS(*pb.TTSRequest) error {
	return fmt.Errorf("unimplemented")
}

func (llm *Base) SoundGeneration(*pb.SoundGenerationRequest) error {
	return fmt.Errorf("unimplemented")
}

func (llm *Base) Detect(*pb.DetectOptions) (pb.DetectResponse, error) {
	return pb.DetectResponse{}, fmt.Errorf("unimplemented")
}

func (llm *Base) TokenizeString(opts *pb.PredictOptions) (pb.TokenizationResponse, error) {
	return pb.TokenizationResponse{}, fmt.Errorf("unimplemented")
}

// backends may wish to call this to capture the gopsutil info, then enhance with additional memory usage details?
func (llm *Base) Status() (pb.StatusResponse, error) {
	return pb.StatusResponse{
		Memory: memoryUsage(),
	}, nil
}

func (llm *Base) StoresSet(*pb.StoresSetOptions) error {
	return fmt.Errorf("unimplemented")
}

func (llm *Base) StoresGet(*pb.StoresGetOptions) (pb.StoresGetResult, error) {
	return pb.StoresGetResult{}, fmt.Errorf("unimplemented")
}

func (llm *Base) StoresDelete(*pb.StoresDeleteOptions) error {
	return fmt.Errorf("unimplemented")
}

func (llm *Base) StoresFind(*pb.StoresFindOptions) (pb.StoresFindResult, error) {
	return pb.StoresFindResult{}, fmt.Errorf("unimplemented")
}

func (llm *Base) VAD(*pb.VADRequest) (pb.VADResponse, error) {
	return pb.VADResponse{}, fmt.Errorf("unimplemented")
}

func memoryUsage() *pb.MemoryUsageData {
	mud := pb.MemoryUsageData{
		Breakdown: make(map[string]uint64),
	}

	pid := int32(os.Getpid())

	backendProcess, err := gopsutil.NewProcess(pid)

	if err == nil {
		memInfo, err := backendProcess.MemoryInfo()
		if err == nil {
			mud.Total = memInfo.VMS // TEST, but rss seems reasonable first guess. Does include swap, but we might care about that.
			mud.Breakdown["gopsutil-RSS"] = memInfo.RSS
		}
	}
	return &mud
}
