package model

import grpc "github.com/mudler/LocalAI/pkg/grpc"

type Model struct {
	ID      string `json:"id"`
	address string
	client  grpc.Backend
}

func NewModel(ID, address string) *Model {
	return &Model{
		ID:      ID,
		address: address,
	}
}

func (m *Model) GRPC(parallel bool, wd *WatchDog) grpc.Backend {
	if m.client != nil {
		return m.client
	}

	enableWD := false
	if wd != nil {
		enableWD = true
	}

	m.client = grpc.NewClient(m.address, parallel, wd, enableWD)
	return m.client
}
