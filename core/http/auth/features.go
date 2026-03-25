package auth

// RouteFeature maps a route pattern + HTTP method to a required feature.
type RouteFeature struct {
	Method  string // "POST", "GET", "*" (any)
	Pattern string // Echo route pattern, e.g. "/v1/chat/completions"
	Feature string // Feature constant, e.g. FeatureChat
}

// RouteFeatureRegistry is the single source of truth for endpoint -> feature mappings.
// To gate a new endpoint, add an entry here -- no other file changes needed.
var RouteFeatureRegistry = []RouteFeature{
	// Chat / Completions
	{"POST", "/v1/chat/completions", FeatureChat},
	{"POST", "/chat/completions", FeatureChat},
	{"POST", "/v1/completions", FeatureChat},
	{"POST", "/completions", FeatureChat},
	{"POST", "/v1/engines/:model/completions", FeatureChat},
	{"POST", "/v1/edits", FeatureChat},
	{"POST", "/edits", FeatureChat},

	// Anthropic
	{"POST", "/v1/messages", FeatureChat},
	{"POST", "/messages", FeatureChat},

	// Open Responses
	{"POST", "/v1/responses", FeatureChat},
	{"POST", "/responses", FeatureChat},
	{"GET", "/v1/responses", FeatureChat},
	{"GET", "/responses", FeatureChat},

	// Embeddings
	{"POST", "/v1/embeddings", FeatureEmbeddings},
	{"POST", "/embeddings", FeatureEmbeddings},
	{"POST", "/v1/engines/:model/embeddings", FeatureEmbeddings},

	// Images
	{"POST", "/v1/images/generations", FeatureImages},
	{"POST", "/images/generations", FeatureImages},
	{"POST", "/v1/images/inpainting", FeatureImages},
	{"POST", "/images/inpainting", FeatureImages},

	// Audio transcription
	{"POST", "/v1/audio/transcriptions", FeatureAudioTranscription},
	{"POST", "/audio/transcriptions", FeatureAudioTranscription},

	// Audio speech / TTS
	{"POST", "/v1/audio/speech", FeatureAudioSpeech},
	{"POST", "/audio/speech", FeatureAudioSpeech},
	{"POST", "/tts", FeatureAudioSpeech},
	{"POST", "/v1/text-to-speech/:voice-id", FeatureAudioSpeech},

	// VAD
	{"POST", "/vad", FeatureVAD},
	{"POST", "/v1/vad", FeatureVAD},

	// Detection
	{"POST", "/v1/detection", FeatureDetection},

	// Video
	{"POST", "/video", FeatureVideo},

	// Sound generation
	{"POST", "/v1/sound-generation", FeatureSound},

	// Realtime
	{"GET", "/v1/realtime", FeatureRealtime},
	{"POST", "/v1/realtime/sessions", FeatureRealtime},
	{"POST", "/v1/realtime/transcription_session", FeatureRealtime},
	{"POST", "/v1/realtime/calls", FeatureRealtime},

	// MCP
	{"POST", "/v1/mcp/chat/completions", FeatureMCP},
	{"POST", "/mcp/v1/chat/completions", FeatureMCP},
	{"POST", "/mcp/chat/completions", FeatureMCP},

	// Tokenize
	{"POST", "/v1/tokenize", FeatureTokenize},

	// Rerank
	{"POST", "/v1/rerank", FeatureRerank},

	// Stores
	{"POST", "/stores/set", FeatureStores},
	{"POST", "/stores/delete", FeatureStores},
	{"POST", "/stores/get", FeatureStores},
	{"POST", "/stores/find", FeatureStores},

	// Fine-tuning
	{"POST", "/api/fine-tuning/jobs", FeatureFineTuning},
	{"GET", "/api/fine-tuning/jobs", FeatureFineTuning},
	{"GET", "/api/fine-tuning/jobs/:id", FeatureFineTuning},
	{"POST", "/api/fine-tuning/jobs/:id/stop", FeatureFineTuning},
	{"DELETE", "/api/fine-tuning/jobs/:id", FeatureFineTuning},
	{"GET", "/api/fine-tuning/jobs/:id/progress", FeatureFineTuning},
	{"GET", "/api/fine-tuning/jobs/:id/checkpoints", FeatureFineTuning},
	{"POST", "/api/fine-tuning/jobs/:id/export", FeatureFineTuning},
	{"GET", "/api/fine-tuning/jobs/:id/download", FeatureFineTuning},
	{"POST", "/api/fine-tuning/datasets", FeatureFineTuning},

	// Quantization
	{"POST", "/api/quantization/jobs", FeatureQuantization},
	{"GET", "/api/quantization/jobs", FeatureQuantization},
	{"GET", "/api/quantization/jobs/:id", FeatureQuantization},
	{"POST", "/api/quantization/jobs/:id/stop", FeatureQuantization},
	{"DELETE", "/api/quantization/jobs/:id", FeatureQuantization},
	{"GET", "/api/quantization/jobs/:id/progress", FeatureQuantization},
	{"POST", "/api/quantization/jobs/:id/import", FeatureQuantization},
	{"GET", "/api/quantization/jobs/:id/download", FeatureQuantization},
}

// FeatureMeta describes a feature for the admin API/UI.
type FeatureMeta struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	DefaultValue bool   `json:"default"`
}

// AgentFeatureMetas returns metadata for agent features.
func AgentFeatureMetas() []FeatureMeta {
	return []FeatureMeta{
		{FeatureAgents, "Agents", false},
		{FeatureSkills, "Skills", false},
		{FeatureCollections, "Collections", false},
		{FeatureMCPJobs, "MCP CI Jobs", false},
	}
}

// GeneralFeatureMetas returns metadata for general features.
func GeneralFeatureMetas() []FeatureMeta {
	return []FeatureMeta{
		{FeatureFineTuning, "Fine-Tuning", false},
		{FeatureQuantization, "Quantization", false},
	}
}

// APIFeatureMetas returns metadata for API endpoint features.
func APIFeatureMetas() []FeatureMeta {
	return []FeatureMeta{
		{FeatureChat, "Chat Completions", true},
		{FeatureImages, "Image Generation", true},
		{FeatureAudioSpeech, "Audio Speech / TTS", true},
		{FeatureAudioTranscription, "Audio Transcription", true},
		{FeatureVAD, "Voice Activity Detection", true},
		{FeatureDetection, "Detection", true},
		{FeatureVideo, "Video Generation", true},
		{FeatureEmbeddings, "Embeddings", true},
		{FeatureSound, "Sound Generation", true},
		{FeatureRealtime, "Realtime", true},
		{FeatureRerank, "Rerank", true},
		{FeatureTokenize, "Tokenize", true},
		{FeatureMCP, "MCP", true},
		{FeatureStores, "Stores", true},
	}
}
