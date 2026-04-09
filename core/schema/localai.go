package schema

import (
	"encoding/json"
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

	EstimatedVRAMBytes   uint64 `json:"estimated_vram_bytes,omitempty"`
	EstimatedVRAMDisplay string `json:"estimated_vram_display,omitempty"`
	EstimatedSizeBytes   uint64 `json:"estimated_size_bytes,omitempty"`
	EstimatedSizeDisplay string `json:"estimated_size_display,omitempty"`
}

type VideoRequest struct {
	BasicModelRequest
	Prompt         string  `json:"prompt" yaml:"prompt"`                                     // text description of the video to generate
	NegativePrompt string  `json:"negative_prompt" yaml:"negative_prompt"`                   // things to avoid in the output
	StartImage     string  `json:"start_image" yaml:"start_image"`                           // URL or base64 of the first frame
	EndImage       string  `json:"end_image" yaml:"end_image"`                               // URL or base64 of the last frame
	Width          int32   `json:"width" yaml:"width"`                                       // output width in pixels
	Height         int32   `json:"height" yaml:"height"`                                     // output height in pixels
	NumFrames      int32   `json:"num_frames" yaml:"num_frames"`                             // total number of frames to generate
	FPS            int32   `json:"fps" yaml:"fps"`                                           // frames per second
	Seconds        string  `json:"seconds,omitempty" yaml:"seconds,omitempty"`               // duration in seconds (alternative to num_frames)
	Size           string  `json:"size,omitempty" yaml:"size,omitempty"`                     // WxH shorthand (e.g. "512x512")
	InputReference string  `json:"input_reference,omitempty" yaml:"input_reference,omitempty"` // reference image or video URL
	Seed           int32   `json:"seed" yaml:"seed"`                                         // random seed for reproducibility
	CFGScale       float32 `json:"cfg_scale" yaml:"cfg_scale"`                               // classifier-free guidance scale
	Step           int32   `json:"step" yaml:"step"`                                         // number of diffusion steps
	ResponseFormat string  `json:"response_format" yaml:"response_format"`                   // output format (url or b64_json)
}

// @Description TTS request body
type TTSRequest struct {
	BasicModelRequest
	Input    string `json:"input" yaml:"input"` // text input
	Voice    string `json:"voice" yaml:"voice"` // voice audio file or speaker id
	Backend  string `json:"backend" yaml:"backend"` // backend engine override
	Language string `json:"language,omitempty" yaml:"language,omitempty"`               // (optional) language to use with TTS model
	Format   string `json:"response_format,omitempty" yaml:"response_format,omitempty"` // (optional) output format
	Stream     bool   `json:"stream,omitempty" yaml:"stream,omitempty"`                   // (optional) enable streaming TTS
	SampleRate int    `json:"sample_rate,omitempty" yaml:"sample_rate,omitempty"`         // (optional) desired output sample rate
}

// @Description VAD request body
type VADRequest struct {
	BasicModelRequest
	Audio []float32 `json:"audio" yaml:"audio"` // raw audio samples as float32 PCM
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
	LlamaCPPNodes  []NodeData `json:"llama_cpp_nodes" yaml:"llama_cpp_nodes"`
	FederatedNodes []NodeData `json:"federated_nodes" yaml:"federated_nodes"`
	MLXNodes       []NodeData `json:"mlx_nodes" yaml:"mlx_nodes"`
}

type SysInfoModel struct {
	ID string `json:"id"`
}

type SystemInformationResponse struct {
	Backends []string       `json:"backends"`      // available backend engines
	Models   []SysInfoModel `json:"loaded_models"` // currently loaded models
}

type DetectionRequest struct {
	BasicModelRequest
	Image     string    `json:"image"`               // URL or base64-encoded image to analyze
	Prompt    string    `json:"prompt,omitempty"`     // Text prompt (for SAM 3 PCS mode)
	Points    []float32 `json:"points,omitempty"`     // Point coordinates as [x,y,label,...] triples (label: 1=pos, 0=neg)
	Boxes     []float32 `json:"boxes,omitempty"`      // Box coordinates as [x1,y1,x2,y2,...] quads
	Threshold float32   `json:"threshold,omitempty"`  // Detection confidence threshold
}

type DetectionResponse struct {
	Detections []Detection `json:"detections"`
}

type Detection struct {
	X          float32 `json:"x"`
	Y          float32 `json:"y"`
	Width      float32 `json:"width"`
	Height     float32 `json:"height"`
	ClassName  string  `json:"class_name"`
	Confidence float32 `json:"confidence,omitempty"`
	Mask       string  `json:"mask,omitempty"` // base64-encoded PNG segmentation mask
}

type ImportModelRequest struct {
	URI         string          `json:"uri"`
	Preferences json.RawMessage `json:"preferences,omitempty"`
}

// SettingsResponse is the response type for settings API operations
type SettingsResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}
