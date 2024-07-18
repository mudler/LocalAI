package schema

import (
	"github.com/mudler/LocalAI/core/p2p"
	gopsutil "github.com/shirou/gopsutil/v3/process"
)

type BackendMonitorRequest struct {
	Model string `json:"model" yaml:"model"`
}

type BackendMonitorResponse struct {
	MemoryInfo    *gopsutil.MemoryInfoStat
	MemoryPercent float32
	CPUPercent    float64
}

type GalleryResponse struct {
	ID        string `json:"uuid"`
	StatusURL string `json:"status"`
}

// @Description TTS request body
type TTSRequest struct {
	Model    string `json:"model" yaml:"model"` // model name or full path
	Input    string `json:"input" yaml:"input"` // text input
	Voice    string `json:"voice" yaml:"voice"` // voice audio file or speaker id
	Backend  string `json:"backend" yaml:"backend"`
	Language string `json:"language,omitempty" yaml:"language,omitempty"` // (optional) language to use with TTS model
}

type StoresSet struct {
	Store string `json:"store,omitempty" yaml:"store,omitempty"`

	Keys   [][]float32 `json:"keys" yaml:"keys"`
	Values []string    `json:"values" yaml:"values"`
}

type StoresDelete struct {
	Store string `json:"store,omitempty" yaml:"store,omitempty"`

	Keys [][]float32 `json:"keys"`
}

type StoresGet struct {
	Store string `json:"store,omitempty" yaml:"store,omitempty"`

	Keys [][]float32 `json:"keys" yaml:"keys"`
}

type StoresGetResponse struct {
	Keys   [][]float32 `json:"keys" yaml:"keys"`
	Values []string    `json:"values" yaml:"values"`
}

type StoresFind struct {
	Store string `json:"store,omitempty" yaml:"store,omitempty"`

	Key  []float32 `json:"key" yaml:"key"`
	Topk int       `json:"topk" yaml:"topk"`
}

type StoresFindResponse struct {
	Keys         [][]float32 `json:"keys" yaml:"keys"`
	Values       []string    `json:"values" yaml:"values"`
	Similarities []float32   `json:"similarities" yaml:"similarities"`
}

type P2PNodesResponse struct {
	Nodes          []p2p.NodeData `json:"nodes" yaml:"nodes"`
	FederatedNodes []p2p.NodeData `json:"federated_nodes" yaml:"federated_nodes"`
}
