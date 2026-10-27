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
	{"POST", "/api/chats", FeatureChats},
	{"GET", "/api/chats", FeatureChats},
	{"GET", "/api/chats/:id", FeatureChats},
	{"DELETE", "/api/chats/:id", FeatureChats},
	{"PATCH", "/api/chats/:id", FeatureChats},
	{"GET", "/api/chats/search", FeatureChats},
	{"POST", "/api/chats/import", FeatureChats},
	{"POST", "/api/chats/:id/regenerate", FeatureChats},
	{"POST", "/api/chats/:id/messages/:seq/edit", FeatureChats},
	{"POST", "/api/chats/:id/fork", FeatureChats},
	{"GET", "/api/chats/:id/export", FeatureChats},
	{"GET", "/api/chats/:id/agents", FeatureChats},
	{"GET", "/api/chats/:id/agents/:agent_id/messages", FeatureChats},
	{"POST", "/api/chats/:id/agents/:agent_id/stop", FeatureChats},
	{"GET", "/api/chats/:id/messages", FeatureChats},
	{"POST", "/api/chats/:id/messages", FeatureChats},
	{"PATCH", "/api/chats/:id/pending/:pid", FeatureChats},
	{"DELETE", "/api/chats/:id/pending/:pid", FeatureChats},
	{"POST", "/api/chats/:id/send-now", FeatureChats},
	{"POST", "/api/chats/:id/stop", FeatureChats},
	{"GET", "/api/chats/:id/events", FeatureChats},
	{"GET", "/api/chats/files", FeatureChats},
	{"DELETE", "/api/chats/files", FeatureChats},
	{"GET", "/api/chats/files/content", FeatureChats},
	{"PUT", "/api/chats/files/content", FeatureChats},
	{"POST", "/api/chats/files/folders", FeatureChats},
	{"POST", "/api/chats/files/move", FeatureChats},
	{"GET", "/api/chats/files/trash", FeatureChats},
	{"POST", "/api/chats/files/undo", FeatureChats},
	{"GET", "/api/chats/files/history", FeatureChats},
	{"GET", "/api/chats/files/search", FeatureChats},
	{"GET", "/api/chats/skills", FeatureChats},
	{"POST", "/api/chats/skills", FeatureChats},
	{"GET", "/api/chats/skills/:layer/:name", FeatureChats},
	{"PUT", "/api/chats/skills/:layer/:name", FeatureChats},
	{"DELETE", "/api/chats/skills/:layer/:name", FeatureChats},
	{"PUT", "/api/chats/:id/skills", FeatureChats},
	{"GET", "/api/chats/:id/addons", FeatureChats},
	{"PUT", "/api/chats/:id/addons", FeatureChats},
	{"GET", "/api/chats/notifications", FeatureChats},
	{"DELETE", "/api/chats/notifications", FeatureChats},
	{"GET", "/api/chats/notifications/count", FeatureChats},
	{"GET", "/api/chats/conversion", FeatureChats},
	{"POST", "/api/chats/notifications/read", FeatureChats},
	{"GET", "/api/chats/notifications/events", FeatureChats},
	{"GET", "/api/chats/triggers", FeatureChats},
	{"POST", "/api/chats/triggers", FeatureChats},
	{"GET", "/api/chats/triggers/preview", FeatureChats},
	{"GET", "/api/chats/triggers/proposals", FeatureChats},
	{"POST", "/api/chats/triggers/proposals/:id/confirm", FeatureChats},
	{"PUT", "/api/chats/triggers/proposals/:id", FeatureChats},
	{"DELETE", "/api/chats/triggers/proposals/:id", FeatureChats},
	{"GET", "/api/chats/triggers/:id", FeatureChats},
	{"PUT", "/api/chats/triggers/:id", FeatureChats},
	{"DELETE", "/api/chats/triggers/:id", FeatureChats},
	{"POST", "/api/chats/triggers/:id/run", FeatureChats},
	{"POST", "/api/chats/triggers/:id/enable", FeatureChats},
	{"POST", "/api/chats/triggers/:id/disable", FeatureChats},
	{"GET", "/api/chats/triggers/:id/runs", FeatureChats},
	{"GET", "/api/chats/push/key", FeatureChats},
	{"POST", "/api/chats/push/subscribe", FeatureChats},
	{"DELETE", "/api/chats/push/subscribe", FeatureChats},
	{"GET", "/api/chats/connectors", FeatureChats},
	{"POST", "/api/chats/connectors", FeatureChats},
	{"GET", "/api/chats/connectors/:id", FeatureChats},
	{"PUT", "/api/chats/connectors/:id", FeatureChats},
	{"DELETE", "/api/chats/connectors/:id", FeatureChats},
	{"POST", "/api/chats/connectors/:id/enable", FeatureChats},
	{"POST", "/api/chats/connectors/:id/disable", FeatureChats},
	{"POST", "/api/chats/connectors/:id/test", FeatureChats},
	{"GET", "/api/chats/connectors/:id/senders", FeatureChats},
	{"POST", "/api/chats/connectors/:id/senders/:sender/allow", FeatureChats},
	{"GET", "/api/chats/mcp-connectors", FeatureChats},
	{"POST", "/api/chats/mcp-connectors", FeatureChats},
	{"GET", "/api/chats/mcp-connectors/:id", FeatureChats},
	{"PUT", "/api/chats/mcp-connectors/:id", FeatureChats},
	{"DELETE", "/api/chats/mcp-connectors/:id", FeatureChats},
	{"POST", "/api/chats/mcp-connectors/:id/enable", FeatureChats},
	{"POST", "/api/chats/mcp-connectors/:id/disable", FeatureChats},
	{"POST", "/api/chats/mcp-connectors/:id/test", FeatureChats},
	{"GET", "/api/chats/:id/tools", FeatureChats},
	{"PUT", "/api/chats/:id/mcp", FeatureChats},
	{"GET", "/api/chats/:id/mcp/prompts", FeatureChats},
	{"POST", "/api/chats/:id/mcp/prompts", FeatureChats},
	{"GET", "/api/chats/:id/mcp/resources", FeatureChats},
	{"POST", "/api/chats/:id/mcp/resources/read", FeatureChats},
	{"GET", "/api/chats/:id/mcp-apps/resource", FeatureChats},
	{"POST", "/api/chats/:id/mcp-apps/call", FeatureChats},
	{"GET", "/api/chats/agents", FeatureChats},
	{"POST", "/api/chats/agents", FeatureChats},
	{"GET", "/api/chats/agents/templates", FeatureChats},
	{"POST", "/api/chats/agents/templates/:id/create", FeatureChats},
	{"POST", "/api/chats/agents/import", FeatureChats},
	{"GET", "/api/chats/agents/:id/export", FeatureChats},
	{"GET", "/api/chats/agents/:id/runs", FeatureChats},
	{"GET", "/api/chats/agents/:id", FeatureChats},
	{"PUT", "/api/chats/agents/:id", FeatureChats},
	{"DELETE", "/api/chats/agents/:id", FeatureChats},
	{"POST", "/api/motion/sessions", FeatureMotion},
	{"GET", "/api/motion/sessions/:id", FeatureMotion},
	{"DELETE", "/api/motion/sessions/:id", FeatureMotion},
	{"GET", "/api/motion/sessions/:id/poses", FeatureMotion},
	{"POST", "/api/motion/sessions/:id/tickets", FeatureMotion},
	{"POST", "/api/addons/dispatch", FeatureAddons},
	{"POST", "/api/addons/relay", FeatureAddons},
	{"POST", "/api/addons/proposals", FeatureAddons},
	{"GET", "/api/addons/proposals", FeatureAddons},
	{"GET", "/api/addons/proposals/:id", FeatureAddons},
	{"POST", "/api/addons/proposals/:id/confirm", FeatureAddons},
	{"POST", "/api/addons/proposals/:id/cancel", FeatureAddons},
	{"POST", "/api/addons/proposals/:id/reconcile", FeatureAddons},
	{"POST", "/api/addons/proposals/:id/resolve", FeatureAddons},
	{"GET", "/api/addons/resources/:resourceId/proposals", FeatureAddons},
	{"GET", "/api/addons/resources", FeatureAddons},
	{"POST", "/api/addons/resources", FeatureAddons},
	{"GET", "/api/addons/resources/:resourceId/grants", FeatureAddons},
	{"PUT", "/api/addons/resources/:resourceId/grants/:userId", FeatureAddons},
	{"DELETE", "/api/addons/resources/:resourceId/grants/:userId", FeatureAddons},
	{"GET", "/api/addons/resources/:resourceId/users", FeatureAddons},
	{"POST", "/api/addons/resources/:resourceId/rename", FeatureAddons},
	{"POST", "/api/addons/resources/:resourceId/delete", FeatureAddons},
	{"POST", "/api/addons/resources/:resourceId/apply", FeatureAddons},
	{"POST", "/api/addons/resources/:resourceId/changes/:changeId/undo", FeatureAddons},
	{"GET", "/api/addons/resources/:resourceId/operations/:operationId", FeatureAddons},
	{"GET", "/api/addons/conversations/:conversationId/selection/:addonId", FeatureAddons},
	{"PUT", "/api/addons/conversations/:conversationId/selection/:addonId", FeatureAddons},
	{"DELETE", "/api/addons/conversations/:conversationId/selection/:addonId", FeatureAddons},
	{"GET", "/api/addons/artifacts/:id", FeatureAddons},
	{"GET", "/api/addons/artifacts/:id/download", FeatureAddons},
	{"GET", "/api/addons/apps", FeatureAddons},
	{"GET", "/api/addons/apps/:id/icon", FeatureAddons},
	{"POST", "/api/addons/apps/:id/start", FeatureAddons},
	{"POST", "/api/addons/apps/:id/stop", FeatureAddons},
	{"GET", "/api/addons/:id/assets/:digest/*", FeatureAddons},
	{"GET", "/api/addons/:id/recovery", FeatureAddons},
	{"POST", "/api/addons/:id/recovery/backup", FeatureAddons},
	{"POST", "/api/addons/:id/recovery/verify", FeatureAddons},
	{"POST", "/api/addons/:id/recovery/restore", FeatureAddons},
	{"POST", "/api/addons/:id/recovery/delete-backup", FeatureAddons},
	{"POST", "/api/addons/:id/recovery/prune", FeatureAddons},
	{"POST", "/api/addons/:id/purge", FeatureAddons},
	{"GET", "/api/addons/:id/update", FeatureAddons},
	{"POST", "/api/addons/:id/update/check", FeatureAddons},
	{"POST", "/api/addons/:id/upgrade", FeatureAddons},
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
	{"POST", "/v1/images/upscale", FeatureImages},
	{"POST", "/images/upscale", FeatureImages},

	// Audio transcription
	{"POST", "/v1/audio/transcriptions", FeatureAudioTranscription},
	{"POST", "/audio/transcriptions", FeatureAudioTranscription},

	// Audio diarization (speaker turns)
	{"POST", "/v1/audio/diarization", FeatureAudioDiarization},
	{"POST", "/audio/diarization", FeatureAudioDiarization},

	// Audio classification (sound-event tagging)
	{"POST", "/v1/audio/classification", FeatureAudioClassification},
	{"POST", "/audio/classification", FeatureAudioClassification},

	// Audio speech / TTS
	{"POST", "/v1/audio/speech", FeatureAudioSpeech},
	{"POST", "/audio/speech", FeatureAudioSpeech},
	{"POST", "/tts", FeatureAudioSpeech},
	{"GET", "/v1/audio/voices", FeatureAudioSpeech},
	{"GET", "/audio/voices", FeatureAudioSpeech},
	{"POST", "/v1/text-to-speech/:voice-id", FeatureAudioSpeech},
	{"GET", "/api/voice-profiles", FeatureAudioSpeech},
	{"GET", "/api/voice-profiles/:id/audio", FeatureAudioSpeech},

	// VAD
	{"POST", "/vad", FeatureVAD},
	{"POST", "/v1/vad", FeatureVAD},

	// Detection
	{"POST", "/v1/detection", FeatureDetection},

	// Time-series forecasting
	{"POST", "/v1/forecast", FeatureForecast},

	// Decisions API (OpenAI and SystemOne wire contracts)
	{"POST", "/v1/decisions", FeatureDecisions},
	{"POST", "/v1/systemone", FeatureDecisions},
	{"POST", "/v1/systemone/permute", FeatureDecisions},
	{"POST", "/v1/systemone/separate", FeatureDecisions},

	// Face recognition
	{"POST", "/v1/face/verify", FeatureFaceRecognition},
	{"POST", "/v1/face/analyze", FeatureFaceRecognition},
	{"POST", "/v1/face/embed", FeatureFaceRecognition},
	{"POST", "/v1/face/register", FeatureFaceRecognition},
	{"POST", "/v1/face/identify", FeatureFaceRecognition},
	{"POST", "/v1/face/forget", FeatureFaceRecognition},

	// Voice (speaker) recognition
	{"POST", "/v1/voice/verify", FeatureVoiceRecognition},
	{"POST", "/v1/voice/analyze", FeatureVoiceRecognition},
	{"POST", "/v1/voice/embed", FeatureVoiceRecognition},
	{"POST", "/v1/voice/register", FeatureVoiceRecognition},
	{"POST", "/v1/voice/identify", FeatureVoiceRecognition},
	{"POST", "/v1/voice/forget", FeatureVoiceRecognition},

	// Audio transform (echo cancellation, noise suppression, voice conversion, etc.)
	{"POST", "/audio/transformations", FeatureAudioTransform},
	{"POST", "/audio/transform", FeatureAudioTransform},
	{"GET", "/audio/transformations/stream", FeatureAudioTransform},

	// Video
	{"POST", "/video", FeatureVideo},

	// 3D generation
	{"POST", "/3d/generations", Feature3D},
	{"POST", "/3d/remesh", Feature3D},
	{"POST", "/3d/animate", Feature3D},

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
	{"POST", "/v1/detokenize", FeatureTokenize},

	// Rerank
	{"POST", "/v1/rerank", FeatureRerank},

	// Moderation
	{"POST", "/v1/moderations", FeatureModeration},
	{"POST", "/moderations", FeatureModeration},

	// Stores
	{"POST", "/stores/set", FeatureStores},
	{"POST", "/stores/delete", FeatureStores},
	{"POST", "/stores/get", FeatureStores},
	{"POST", "/stores/find", FeatureStores},

	// Fine-tuning
	{"GET", "/api/fine-tuning/status", FeatureFineTuning},
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

	// PII analyze/redact service (the events log stays admin-gated in-handler)
	{"POST", "/api/pii/analyze", FeaturePIIFilter},
	{"POST", "/api/pii/redact", FeaturePIIFilter},

	// Quantization
	{"GET", "/api/quantization/status", FeatureQuantization},
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
		{FeatureCollections, "Collections", false},
		{FeatureLocalAIAssistant, "LocalAI Assistant", false},
	}
}

// GeneralFeatureMetas returns metadata for general features.
func GeneralFeatureMetas() []FeatureMeta {
	return []FeatureMeta{
		{FeatureAddons, "Addons", false},
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
		{FeatureAudioDiarization, "Audio Diarization", true},
		{FeatureAudioClassification, "Audio Classification", true},
		{FeatureVAD, "Voice Activity Detection", true},
		{FeatureDetection, "Detection", true},
		{FeatureVideo, "Video Generation", true},
		{Feature3D, "3D Generation", true},
		{FeatureMotion, "Motion Capture", true},
		{FeatureChats, "Chats", true},
		{FeatureEmbeddings, "Embeddings", true},
		{FeatureSound, "Sound Generation", true},
		{FeatureRealtime, "Realtime", true},
		{FeatureModeration, "Moderation", true},
		{FeatureRerank, "Rerank", true},
		{FeatureTokenize, "Tokenize", true},
		{FeatureMCP, "MCP", true},
		{FeatureStores, "Stores", true},
		{FeatureFaceRecognition, "Face Recognition", true},
		{FeatureVoiceRecognition, "Voice Recognition", true},
		{FeatureAudioTransform, "Audio Transform", true},
		{FeaturePIIFilter, "PII Analyze / Redact", true},
		{FeatureDecisions, "Decisions", true},
		{FeatureForecast, "Forecast", true},
	}
}
