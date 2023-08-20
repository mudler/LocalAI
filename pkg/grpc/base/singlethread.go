package base

import (
	"sync"

	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
)

// SingleThread are backends that does not support multiple requests.
// There will be only one request being served at the time.
// This is useful for models that are not thread safe and cannot run
// multiple requests at the same time.
type SingleThread struct {
	Base
	backendBusy sync.Mutex
}

// Locking returns true if the backend needs to lock resources
func (llm *SingleThread) Locking() bool {
	return true
}

func (llm *SingleThread) Lock() {
	llm.backendBusy.Lock()
}

func (llm *SingleThread) Unlock() {
	llm.backendBusy.Unlock()
}

func (llm *SingleThread) Busy() bool {
	r := llm.backendBusy.TryLock()
	if r {
		llm.backendBusy.Unlock()
	}
	return r
}

// backends may wish to call this to capture the gopsutil info, then enhance with additional memory usage details?
func (llm *SingleThread) Status() (pb.StatusResponse, error) {
	mud := memoryUsage()

	state := pb.StatusResponse_READY
	if llm.Busy() {
		state = pb.StatusResponse_BUSY
	}

	return pb.StatusResponse{
		State:  state,
		Memory: mud,
	}, nil
}
