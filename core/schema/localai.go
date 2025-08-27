package schema

import (
	"time"

	gopsutil "github.com/shirou/gopsutil/v3/process"
)

type BackendMonitorRequest struct {
	BasicModelRequest
}

type TokenMetricsRequest struct {
	BasicModelRequest
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

type VideoRequest struct {
	BasicModelRequest
	Prompt         string  `json:"prompt" yaml:"prompt"`
	NegativePrompt string  `json:"negative_prompt" yaml:"negative_prompt"`
	StartImage     string  `json:"start_image" yaml:"start_image"`
	EndImage       string  `json:"end_image" yaml:"end_image"`
	Width          int32   `json:"width" yaml:"width"`
	Height         int32   `json:"height" yaml:"height"`
	NumFrames      int32   `json:"num_frames" yaml:"num_frames"`
	FPS            int32   `json:"fps" yaml:"fps"`
	Seed           int32   `json:"seed" yaml:"seed"`
	CFGScale       float32 `json:"cfg_scale" yaml:"cfg_scale"`
	Step           int32   `json:"step" yaml:"step"`
	ResponseFormat string  `json:"response_format" yaml:"response_format"`
}

// @Description TTS request body
type TTSRequest struct {
	BasicModelRequest
	Input    string `json:"input" yaml:"input"` // text input
	Voice    string `json:"voice" yaml:"voice"` // voice audio file or speaker id
	Backend  string `json:"backend" yaml:"backend"`
	Language string `json:"language,omitempty" yaml:"language,omitempty"`               // (optional) language to use with TTS model
	Format   string `json:"response_format,omitempty" yaml:"response_format,omitempty"` // (optional) output format
}

// @Description VAD request body
type VADRequest struct {
	BasicModelRequest
	Audio []float32 `json:"audio" yaml:"audio"` // model name or full path
}

type VADSegment struct {
	Start float32 `json:"start" yaml:"start"`
	End   float32 `json:"end" yaml:"end"`
}

type VADResponse struct {
	Segments []VADSegment `json:"segments" yaml:"segments"`
}

type StoreCommon struct {
	Backend string `json:"backend,omitempty" yaml:"backend,omitempty"`
}
type StoresSet struct {
	Store string `json:"store,omitempty" yaml:"store,omitempty"`

	Keys   [][]float32 `json:"keys" yaml:"keys"`
	Values []string    `json:"values" yaml:"values"`
	StoreCommon
}

type StoresDelete struct {
	Store string `json:"store,omitempty" yaml:"store,omitempty"`

	Keys [][]float32 `json:"keys"`
	StoreCommon
}

type StoresGet struct {
	Store string `json:"store,omitempty" yaml:"store,omitempty"`

	Keys [][]float32 `json:"keys" yaml:"keys"`
	StoreCommon
}

type StoresGetResponse struct {
	Keys   [][]float32 `json:"keys" yaml:"keys"`
	Values []string    `json:"values" yaml:"values"`
}

type StoresFind struct {
	Store string `json:"store,omitempty" yaml:"store,omitempty"`

	Key  []float32 `json:"key" yaml:"key"`
	Topk int       `json:"topk" yaml:"topk"`
	StoreCommon
}

type StoresFindResponse struct {
	Keys         [][]float32 `json:"keys" yaml:"keys"`
	Values       []string    `json:"values" yaml:"values"`
	Similarities []float32   `json:"similarities" yaml:"similarities"`
}

type NodeData struct {
	Name          string
	ID            string
	TunnelAddress string
	ServiceID     string
	LastSeen      time.Time
}

func (d NodeData) IsOnline() bool {
	now := time.Now()
	// if the node was seen in the last 40 seconds, it's online
	return now.Sub(d.LastSeen) < 40*time.Second
}

type P2PNodesResponse struct {
	Nodes          []NodeData `json:"nodes" yaml:"nodes"`
	FederatedNodes []NodeData `json:"federated_nodes" yaml:"federated_nodes"`
}

type SysInfoModel struct {
	ID string `json:"id"`
}

type SystemInformationResponse struct {
	Backends []string       `json:"backends"`
	Models   []SysInfoModel `json:"loaded_models"`
}

type DetectionRequest struct {
	BasicModelRequest
	Image string `json:"image"`
}

type DetectionResponse struct {
	Detections []Detection `json:"detections"`
}

type Detection struct {
	X         float32 `json:"x"`
	Y         float32 `json:"y"`
	Width     float32 `json:"width"`
	Height    float32 `json:"height"`
	ClassName string  `json:"class_name"`
}
