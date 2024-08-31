package model

import grpc "github.com/mudler/LocalAI/pkg/grpc"

type Model struct {
	address string
	client  grpc.Backend
}

func NewModel(address string) *Model {
	return &Model{
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
