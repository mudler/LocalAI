package model

import (
	"sync"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	process "github.com/mudler/go-processmanager"
)

type Model struct {
	ID      string `json:"id"`
	address string
	client  grpc.Backend
	process *process.Process
	sync.Mutex
}

func NewModel(ID, address string, process *process.Process) *Model {
	return &Model{
		ID:      ID,
		address: address,
		process: process,
	}
}

func (m *Model) Process() *process.Process {
	return m.process
}

func (m *Model) GRPC(parallel bool, wd *WatchDog) grpc.Backend {
	if m.client != nil {
		return m.client
	}

	enableWD := false
	if wd != nil {
		enableWD = true
	}

	m.Lock()
	defer m.Unlock()
	m.client = grpc.NewClient(m.address, parallel, wd, enableWD)
	return m.client
}
