package model

import (
	"sync"
	"sync/atomic"
	"time"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	process "github.com/mudler/go-processmanager"
)

// healthCheckTTL is the duration for which a successful health check is cached.
// Subsequent checkIsLoaded calls within this window skip the gRPC round-trip,
// avoiding serialization of concurrent requests behind ml.mu.Lock().
const healthCheckTTL = 30 * time.Second

type Model struct {
	ID              string `json:"id"`
	address         string
	client          grpc.Backend
	process         *process.Process
	lastHealthCheck time.Time
	healthMu        sync.Mutex
	sync.Mutex

	// metadataProbed records that this running instance answered the model
	// metadata probe; it answers the same way for the rest of its life.
	metadataProbed atomic.Bool
}

// MarkMetadataProbed records that this instance answered the model metadata
// probe.
func (m *Model) MarkMetadataProbed() { m.metadataProbed.Store(true) }

// MetadataProbed reports whether this instance answered the model metadata
// probe.
func (m *Model) MetadataProbed() bool { return m.metadataProbed.Load() }

func NewModel(ID, address string, process *process.Process) *Model {
	return &Model{
		ID:      ID,
		address: address,
		process: process,
	}
}

// NewModelWithClient creates a Model with a pre-configured gRPC client.
// Used in distributed mode where the client is wrapped with file staging.
func NewModelWithClient(ID, address string, client grpc.Backend) *Model {
	return &Model{
		ID:      ID,
		address: address,
		client:  client,
	}
}

func (m *Model) Process() *process.Process {
	return m.process
}

// IsRecentlyHealthy returns true if the model passed a health check within the TTL.
func (m *Model) IsRecentlyHealthy() bool {
	m.Lock()
	defer m.Unlock()
	return !m.lastHealthCheck.IsZero() && time.Since(m.lastHealthCheck) < healthCheckTTL
}

// MarkHealthy records the current time as the last successful health check.
func (m *Model) MarkHealthy() {
	m.Lock()
	defer m.Unlock()
	m.lastHealthCheck = time.Now()
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
