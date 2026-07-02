package schema

import (
	"encoding/json"
	"time"

	gopsutil "github.com/shirou/gopsutil/v3/process"
)

type BackendMonitorRequest struct {
	BasicModelRequest
}

// ModelLoadRequest asks LocalAI to pre-load a model into memory by name, so the
// first request that uses it pays no cold-start load cost. For a realtime
// pipeline model, every configured sub-model (VAD, transcription, LLM, TTS,
// sound_detection, voice_recognition) is loaded instead of the pipeline stub.
// It is the inverse of the /backend/shutdown request.
type ModelLoadRequest struct {
	BasicModelRequest
}

// ModelLoadResponse reports the outcome of a /backend/load call.
type ModelLoadResponse struct {
	// Loaded lists the model names actually resident in memory after the call.
	// For a pipeline model these are its sub-models, not the pipeline name.
	Loaded []string `json:"loaded"`
	// Message is a short human-readable status ("model loaded", or an error).
	Message string `json:"message"`
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
	Prompt         string            `json:"prompt" yaml:"prompt"`                                       // text description of the video to generate
	NegativePrompt string            `json:"negative_prompt" yaml:"negative_prompt"`                     // things to avoid in the output
	StartImage     string            `json:"start_image" yaml:"start_image"`                             // URL or base64 of the first frame
	EndImage       string            `json:"end_image" yaml:"end_image"`                                 // URL or base64 of the last frame
	Audio          string            `json:"audio,omitempty" yaml:"audio,omitempty"`                     // URL or base64 audio for audio-conditioned generation
	Width          int32             `json:"width" yaml:"width"`                                         // output width in pixels
	Height         int32             `json:"height" yaml:"height"`                                       // output height in pixels
	NumFrames      int32             `json:"num_frames" yaml:"num_frames"`                               // total number of frames to generate
	FPS            int32             `json:"fps" yaml:"fps"`                                             // frames per second
	Seconds        string            `json:"seconds,omitempty" yaml:"seconds,omitempty"`                 // duration in seconds (alternative to num_frames)
	Size           string            `json:"size,omitempty" yaml:"size,omitempty"`                       // WxH shorthand (e.g. "512x512")
	InputReference string            `json:"input_reference,omitempty" yaml:"input_reference,omitempty"` // reference image or video URL
	Seed           int32             `json:"seed" yaml:"seed"`                                           // random seed for reproducibility
	CFGScale       float32           `json:"cfg_scale" yaml:"cfg_scale"`                                 // classifier-free guidance scale
	Step           int32             `json:"step" yaml:"step"`                                           // number of diffusion steps
	ResponseFormat string            `json:"response_format" yaml:"response_format"`                     // output format (url or b64_json)
	Params         map[string]string `json:"params,omitempty" yaml:"params,omitempty"`                   // backend-specific generation parameters
}

// @Description TTS request body
type TTSRequest struct {
	BasicModelRequest
	Input      string `json:"input" yaml:"input"`                                         // text input
	Voice      string `json:"voice" yaml:"voice"`                                         // voice audio file or speaker id
	Backend    string `json:"backend" yaml:"backend"`                                     // backend engine override
	Language   string `json:"language,omitempty" yaml:"language,omitempty"`               // (optional) language to use with TTS model
	Format     string `json:"response_format,omitempty" yaml:"response_format,omitempty"` // (optional) output format
	Stream     bool   `json:"stream,omitempty" yaml:"stream,omitempty"`                   // (optional) enable streaming TTS
	SampleRate int    `json:"sample_rate,omitempty" yaml:"sample_rate,omitempty"`         // (optional) desired output sample rate
	// Instructions is a free-form, per-request style/voice description. It maps to
	// the OpenAI `instructions` field and is forwarded to the backend so expressive
	// TTS models (e.g. Qwen3-TTS CustomVoice/VoiceDesign) can vary tone or designed
	// voice per request instead of only via the static YAML option.
	Instructions string `json:"instructions,omitempty" yaml:"instructions,omitempty"`
	// Params carries optional, backend-specific per-request generation parameters
	// (LocalAI extension, e.g. Chatterbox exaggeration/cfg_weight/temperature).
	Params map[string]string `json:"params,omitempty" yaml:"params,omitempty"`
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
	Prompt    string    `json:"prompt,omitempty"`    // Text prompt (for SAM 3 PCS mode)
	Points    []float32 `json:"points,omitempty"`    // Point coordinates as [x,y,label,...] triples (label: 1=pos, 0=neg)
	Boxes     []float32 `json:"boxes,omitempty"`     // Box coordinates as [x1,y1,x2,y2,...] quads
	Threshold float32   `json:"threshold,omitempty"` // Detection confidence threshold
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

// DepthRequest is the request body for the /v1/depth endpoint. It exposes the
// full Depth Anything 3 output surface; the include_* flags and exports let a
// caller ask for less work (e.g. depth only, or depth+pose without the point
// cloud).
type DepthRequest struct {
	BasicModelRequest
	Image             string   `json:"image"`                        // URL or base64-encoded image to analyze
	Dst               string   `json:"dst,omitempty"`                // optional output directory for exports (glb/colmap)
	IncludeDepth      bool     `json:"include_depth,omitempty"`      // return the per-pixel depth map
	IncludeConfidence bool     `json:"include_confidence,omitempty"` // return the per-pixel confidence map (DualDPT)
	IncludePose       bool     `json:"include_pose,omitempty"`       // return camera extrinsics/intrinsics (DualDPT)
	IncludeSky        bool     `json:"include_sky,omitempty"`        // return the per-pixel sky map (mono models)
	IncludePoints     bool     `json:"include_points,omitempty"`     // back-project to a 3D point cloud (DualDPT)
	PointsConfThresh  float32  `json:"points_conf_thresh,omitempty"` // keep points with confidence >= this threshold
	Exports           []string `json:"exports,omitempty"`            // requested exports: "glb", "colmap"
}

// DepthResponse is the JSON response for the /v1/depth endpoint, mirroring the
// DepthResponse proto.
type DepthResponse struct {
	Width       int32     `json:"width"`
	Height      int32     `json:"height"`
	Depth       []float32 `json:"depth,omitempty"`        // width*height row-major metric depth
	Confidence  []float32 `json:"confidence,omitempty"`   // width*height row-major confidence (DualDPT)
	Sky         []float32 `json:"sky,omitempty"`          // width*height row-major sky map (mono)
	Extrinsics  []float32 `json:"extrinsics,omitempty"`   // 12 floats, 3x4 row-major (world-to-camera)
	Intrinsics  []float32 `json:"intrinsics,omitempty"`   // 9 floats, 3x3 row-major
	NumPoints   int32     `json:"num_points,omitempty"`   // number of 3D points
	Points      []float32 `json:"points,omitempty"`       // num_points*3 xyz, world space
	PointColors string    `json:"point_colors,omitempty"` // base64-encoded num_points*3 uint8 rgb
	ExportPaths []string  `json:"export_paths,omitempty"` // paths written for the requested exports
	IsMetric    bool      `json:"is_metric"`              // depth is in metric units
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
	Verified         bool       `json:"verified"`
	Distance         float32    `json:"distance"`
	Threshold        float32    `json:"threshold"`
	Confidence       float32    `json:"confidence"`
	Model            string     `json:"model"`
	Img1Area         FacialArea `json:"img1_area"`
	Img2Area         FacialArea `json:"img2_area"`
	ProcessingTimeMs float32    `json:"processing_time_ms,omitempty"`
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

// RouterDecideRequest is the input for POST /api/router/decide — the
// programmatic decision-oracle endpoint. Given the name of a router
// model (a ModelConfig that carries a `router:` block) and a prompt,
// the endpoint returns the classifier's label set plus the candidate
// model the in-band RouteModel middleware would have chosen. The
// endpoint does NOT rewrite any request, forward to a backend, or
// record a row in the decision store — it is a pure decision oracle
// for external routers that want LocalAI's classifier opinion without
// committing LocalAI to handle the request.
type RouterDecideRequest struct {
	// Router is the name of the router model (a ModelConfig with a
	// `router:` block). Required.
	Router string `json:"router"`
	// Input is the user-visible prompt text to classify. Required.
	// Schema-shape extraction (chat-message concatenation, etc.) is
	// the caller's responsibility — matches the Probe contract used
	// by the in-band middleware.
	Input string `json:"input"`
}

// RouterDecideResponse carries the classifier's decision plus the
// resolved candidate. Mirrors router.Decision with the addition of
// Candidate/Fallback so the caller learns which downstream model
// would have served the request without re-implementing the
// label-set → candidate match locally.
type RouterDecideResponse struct {
	// Router echoes the requested router model.
	Router string `json:"router"`
	// Classifier is the classifier name that produced the decision
	// (e.g. "score").
	Classifier string `json:"classifier"`
	// Labels is the set of active policy labels.
	Labels []string `json:"labels"`
	// Candidate is the model that would be routed to. Empty when no
	// candidate covers Labels AND no fallback is configured.
	Candidate string `json:"candidate,omitempty"`
	// Fallback is true when Candidate is the router's configured
	// fallback because no candidate covered Labels. Lets callers
	// distinguish "matched" from "fell back" without comparing names.
	Fallback bool `json:"fallback,omitempty"`
	// Score is the top label's softmax probability (the
	// classifier-side confidence signal).
	Score float64 `json:"score"`
	// LatencyMs is the classifier's wall-clock cost.
	LatencyMs int64 `json:"latency_ms"`
	// Cached is true when the decision came from the L2 embedding
	// cache rather than a fresh classifier run.
	Cached bool `json:"cached,omitempty"`
	// CacheSimilarity carries the cosine similarity of the cache hit
	// (0 when not cached).
	CacheSimilarity float64 `json:"cache_similarity,omitempty"`
	// NearestSimilarity is the cosine similarity of the closest KNN
	// corpus entry — populated by the knn classifier even when the
	// decision fell back because the probe was out of corpus range.
	// 0 for other classifiers.
	NearestSimilarity float64 `json:"nearest_similarity,omitempty"`
}

// RouterCorpusEntry is one labelled exemplar submitted to
// POST /api/router/{name}/corpus. The text is embedded server-side
// with the router's knn.embedding_model; labels must be declared in
// the router's policies.
type RouterCorpusEntry struct {
	Text   string   `json:"text"`
	Labels []string `json:"labels"`
}

// RouterCorpusAddRequest is the input for POST /api/router/{name}/corpus —
// bulk-seeds the KNN routing corpus. Corpus input is API-only by
// design: entries may contain example user content, so they are never
// entered through (or displayed in) the UI.
type RouterCorpusAddRequest struct {
	Entries []RouterCorpusEntry `json:"entries"`
}

// RouterCorpusAddResponse reports the outcome of a corpus seed call.
type RouterCorpusAddResponse struct {
	Router string `json:"router"`
	// Added is how many entries were embedded, persisted, and indexed.
	Added int `json:"added"`
	// Skipped counts entries whose text was already in the corpus —
	// duplicates are rejected rather than double-weighted.
	Skipped int `json:"skipped"`
	// Total is the corpus size after the call.
	Total int `json:"total"`
	// LabelCounts is the per-label exemplar count after the call.
	LabelCounts map[string]int `json:"label_counts"`
}

// RouterCorpusStatsResponse is the inspection surface for a router's
// KNN corpus: counts and configuration only — entry texts are never
// returned by any endpoint.
type RouterCorpusStatsResponse struct {
	Router         string         `json:"router"`
	StoreName      string         `json:"store_name"`
	EmbeddingModel string         `json:"embedding_model"`
	Total          int            `json:"total"`
	LabelCounts    map[string]int `json:"label_counts"`
	// EmbeddingModels lists the embedder fingerprints present in the
	// persisted corpus; more than one means part of the corpus is
	// pending re-embedding on the next load.
	EmbeddingModels []string `json:"embedding_models,omitempty"`
}

// RouterCorpusClearResponse reports how many entries a
// DELETE /api/router/{name}/corpus removed.
type RouterCorpusClearResponse struct {
	Router  string `json:"router"`
	Cleared int    `json:"cleared"`
}

// PIIDecideRequest is the input for POST /api/pii/decide — the
// programmatic PII-decision oracle. External routers call it before
// dispatching a request to learn whether the content carries PII and
// what action the configured pattern set would take. The endpoint
// inspects the text and returns findings + a suggested action; it
// does NOT mutate the input, record an audit event, or rewrite any
// downstream request. The caller composes the decision with its own
// policy (mask, block, or allow).
type PIIDecideRequest struct {
	// Text is the user-visible content to inspect. Required.
	Text string `json:"text"`
}

// PIIDecideResponse carries the redactor's findings.
// SuggestedAction is derived from the action ordering used by the
// internal redactor (block > mask > allow) so callers don't need to
// replicate that logic.
type PIIDecideResponse struct {
	// Findings is one entry per matched span — pattern id, byte
	// range, and audit-safe hash prefix (never the matched value).
	Findings []PIIFinding `json:"findings"`
	// SuggestedAction is the strongest action across all findings:
	// "block", "mask", or "allow" (no findings, or all findings
	// resolved to the allow action).
	SuggestedAction string `json:"suggested_action"`
	// RedactedPreview is the input with mask-action spans replaced
	// by their placeholders. Identical to Text when no findings or
	// when the strongest action is block/allow (which don't rewrite
	// content).
	RedactedPreview string `json:"redacted_preview"`
}

// PIIFinding mirrors pii.Span on the wire. Pattern is the pattern id
// that matched (e.g. "email"). HashPrefix is the first 8 chars of
// sha256(matched value) — lets admins correlate recurring leaks
// without recovering the value itself.
type PIIFinding struct {
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Pattern    string `json:"pattern"`
	HashPrefix string `json:"hash_prefix"`
}
