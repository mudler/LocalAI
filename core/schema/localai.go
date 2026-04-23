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

// ─── Face recognition ──────────────────────────────────────────────
//
// FacialArea describes a bounding box for a detected face.
type FacialArea struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	W float32 `json:"w"`
	H float32 `json:"h"`
}

// FaceVerifyRequest compares two images to decide whether they depict
// the same person. Img1 and Img2 accept URL, base64, or data-URI.
type FaceVerifyRequest struct {
	BasicModelRequest
	Img1         string  `json:"img1"`
	Img2         string  `json:"img2"`
	Threshold    float32 `json:"threshold,omitempty"`
	AntiSpoofing bool    `json:"anti_spoofing,omitempty"`
}

type FaceVerifyResponse struct {
	Verified           bool       `json:"verified"`
	Distance           float32    `json:"distance"`
	Threshold          float32    `json:"threshold"`
	Confidence         float32    `json:"confidence"`
	Model              string     `json:"model"`
	Img1Area           FacialArea `json:"img1_area"`
	Img2Area           FacialArea `json:"img2_area"`
	ProcessingTimeMs   float32    `json:"processing_time_ms,omitempty"`
	// Liveness fields are only populated when the request set
	// anti_spoofing=true. Pointers keep them fully absent from the
	// JSON response otherwise, so callers can tell "not checked"
	// apart from "checked and fake" (which would collapse to zero
	// values with plain bool+omitempty).
	Img1IsReal         *bool    `json:"img1_is_real,omitempty"`
	Img1AntispoofScore *float32 `json:"img1_antispoof_score,omitempty"`
	Img2IsReal         *bool    `json:"img2_is_real,omitempty"`
	Img2AntispoofScore *float32 `json:"img2_antispoof_score,omitempty"`
}

// FaceAnalyzeRequest asks the backend for demographic attributes on
// every face detected in Img.
type FaceAnalyzeRequest struct {
	BasicModelRequest
	Img          string   `json:"img"`
	Actions      []string `json:"actions,omitempty"` // subset of {"age","gender","emotion","race"}
	AntiSpoofing bool     `json:"anti_spoofing,omitempty"`
}

type FaceAnalyzeResponse struct {
	Faces []FaceAnalysis `json:"faces"`
}

type FaceAnalysis struct {
	Region          FacialArea         `json:"region"`
	FaceConfidence  float32            `json:"face_confidence"`
	Age             float32            `json:"age,omitempty"`
	DominantGender  string             `json:"dominant_gender,omitempty"`
	Gender          map[string]float32 `json:"gender,omitempty"`
	DominantEmotion string             `json:"dominant_emotion,omitempty"`
	Emotion         map[string]float32 `json:"emotion,omitempty"`
	DominantRace    string             `json:"dominant_race,omitempty"`
	Race            map[string]float32 `json:"race,omitempty"`
	// Liveness fields — see FaceVerifyResponse for why these are pointers.
	IsReal         *bool    `json:"is_real,omitempty"`
	AntispoofScore *float32 `json:"antispoof_score,omitempty"`
}

// FaceEmbedRequest extracts a face embedding from an image. Distinct
// from /v1/embeddings (which is OpenAI-compatible and text-only); this
// endpoint accepts URL / base64 / data-URI image inputs.
type FaceEmbedRequest struct {
	BasicModelRequest
	Img string `json:"img"`
}

type FaceEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
	Dim       int       `json:"dim"`
	Model     string    `json:"model,omitempty"`
}

// FaceRegisterRequest enrolls a face into the 1:N recognition store.
type FaceRegisterRequest struct {
	BasicModelRequest
	Img    string            `json:"img"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Store  string            `json:"store,omitempty"` // vector store model; empty = local-store default
}

type FaceRegisterResponse struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	RegisteredAt time.Time `json:"registered_at"`
}

// FaceIdentifyRequest runs 1:N recognition: embed the probe and
// return the top-K nearest registered faces.
type FaceIdentifyRequest struct {
	BasicModelRequest
	Img       string  `json:"img"`
	TopK      int     `json:"top_k,omitempty"`
	Threshold float32 `json:"threshold,omitempty"` // optional cutoff on distance
	Store     string  `json:"store,omitempty"`
}

type FaceIdentifyResponse struct {
	Matches []FaceIdentifyMatch `json:"matches"`
}

type FaceIdentifyMatch struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels,omitempty"`
	Distance   float32           `json:"distance"`
	Confidence float32           `json:"confidence"`
	Match      bool              `json:"match"` // true when distance <= threshold
}

// FaceForgetRequest removes a previously-registered face by ID.
type FaceForgetRequest struct {
	BasicModelRequest
	ID    string `json:"id"`
	Store string `json:"store,omitempty"`
}

// ─── Voice (speaker) recognition ───────────────────────────────────
//
// VoiceVerifyRequest compares two audio clips and reports whether they
// were spoken by the same speaker. Audio1/Audio2 accept URL, base64,
// or data-URI (the HTTP layer materialises the bytes to a temp file
// before calling the gRPC backend).
type VoiceVerifyRequest struct {
	BasicModelRequest
	Audio1       string  `json:"audio1"`
	Audio2       string  `json:"audio2"`
	Threshold    float32 `json:"threshold,omitempty"`
	AntiSpoofing bool    `json:"anti_spoofing,omitempty"`
}

type VoiceVerifyResponse struct {
	Verified         bool    `json:"verified"`
	Distance         float32 `json:"distance"`
	Threshold        float32 `json:"threshold"`
	Confidence       float32 `json:"confidence"`
	Model            string  `json:"model"`
	ProcessingTimeMs float32 `json:"processing_time_ms,omitempty"`
}

// VoiceAnalyzeRequest asks the backend for demographic attributes
// (age, gender, emotion) inferred from the audio clip.
type VoiceAnalyzeRequest struct {
	BasicModelRequest
	Audio   string   `json:"audio"`
	Actions []string `json:"actions,omitempty"` // subset of {"age","gender","emotion"}
}

type VoiceAnalyzeResponse struct {
	Segments []VoiceAnalysis `json:"segments"`
}

type VoiceAnalysis struct {
	Start           float32            `json:"start"`
	End             float32            `json:"end"`
	Age             float32            `json:"age,omitempty"`
	DominantGender  string             `json:"dominant_gender,omitempty"`
	Gender          map[string]float32 `json:"gender,omitempty"`
	DominantEmotion string             `json:"dominant_emotion,omitempty"`
	Emotion         map[string]float32 `json:"emotion,omitempty"`
}

// VoiceEmbedRequest extracts a speaker embedding from an audio clip.
// Distinct from /v1/embeddings (OpenAI-compatible, text-only) — this
// endpoint accepts URL / base64 / data-URI audio inputs.
type VoiceEmbedRequest struct {
	BasicModelRequest
	Audio string `json:"audio"`
}

type VoiceEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
	Dim       int       `json:"dim"`
	Model     string    `json:"model,omitempty"`
}

// VoiceRegisterRequest enrolls a speaker into the 1:N identification store.
type VoiceRegisterRequest struct {
	BasicModelRequest
	Audio  string            `json:"audio"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Store  string            `json:"store,omitempty"`
}

type VoiceRegisterResponse struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	RegisteredAt time.Time `json:"registered_at"`
}

// VoiceIdentifyRequest runs 1:N recognition: embed the probe and
// return the top-K nearest registered speakers.
type VoiceIdentifyRequest struct {
	BasicModelRequest
	Audio     string  `json:"audio"`
	TopK      int     `json:"top_k,omitempty"`
	Threshold float32 `json:"threshold,omitempty"`
	Store     string  `json:"store,omitempty"`
}

type VoiceIdentifyResponse struct {
	Matches []VoiceIdentifyMatch `json:"matches"`
}

type VoiceIdentifyMatch struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels,omitempty"`
	Distance   float32           `json:"distance"`
	Confidence float32           `json:"confidence"`
	Match      bool              `json:"match"`
}

// VoiceForgetRequest removes a previously-registered speaker by ID.
type VoiceForgetRequest struct {
	BasicModelRequest
	ID    string `json:"id"`
	Store string `json:"store,omitempty"`
}

type ImportModelRequest struct {
	URI         string          `json:"uri"`
	Preferences json.RawMessage `json:"preferences,omitempty"`
}

// KnownBackend describes a backend that the importer knows about.
// Used by GET /backends/known to populate the import form dropdown.
type KnownBackend struct {
	Name        string `json:"name"`
	Modality    string `json:"modality"`
	AutoDetect  bool   `json:"auto_detect"`
	Description string `json:"description,omitempty"`
	// Installed is true when the backend is currently present on disk — i.e. it
	// appears in gallery.ListSystemBackends(systemState). Importer-registered or
	// curated pref-only backends default to false unless they also show up on
	// disk. The import form uses this to warn users that submitting an import
	// may trigger an automatic backend download.
	Installed bool `json:"installed"`
}

// SettingsResponse is the response type for settings API operations
type SettingsResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}
