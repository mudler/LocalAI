package base

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"
	"os"
	"sync"

	"github.com/go-skynet/LocalAI/api/schema"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	gopsutil "github.com/shirou/gopsutil/v3/process"
)

type Base struct {
	backendBusy sync.Mutex
	State       pb.StatusResponse_State
}

func (llm *Base) Locking() bool {
	return false
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
	llm.State = pb.StatusResponse_BUSY
}

func (llm *Base) Unlock() {
	llm.State = pb.StatusResponse_READY
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

func (llm *Base) AudioTranscription(*pb.TranscriptRequest) (schema.Result, error) {
	return schema.Result{}, fmt.Errorf("unimplemented")
}

func (llm *Base) TTS(*pb.TTSRequest) error {
	return fmt.Errorf("unimplemented")
}

func (llm *Base) TokenizeString(opts *pb.PredictOptions) (pb.TokenizationResponse, error) {
	return pb.TokenizationResponse{}, fmt.Errorf("unimplemented")
}

// backends may wish to call this to capture the gopsutil info, then enhance with additional memory usage details?
func (llm *Base) Status() (pb.StatusResponse, error) {

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

	return pb.StatusResponse{
		State:  llm.State,
		Memory: &mud,
	}, nil
}
