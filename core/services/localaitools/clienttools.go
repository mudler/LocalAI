package localaitools

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
)

// ToolGroup tells which family a tool belongs to.
type ToolGroup string

const (
	// GroupManage tools manage the instance (models, backends, settings).
	// They are for administrators.
	GroupManage ToolGroup = "manage"
	// GroupClient tools run inference (images, speech, text analysis). Any
	// user may use them, within their own permissions.
	GroupClient ToolGroup = "client"
)

// Names of the client tools.
const (
	ToolGenerateImage   = "generate_image"
	ToolSpeak           = "speak"
	ToolTranscribeAudio = "transcribe_audio"
	ToolDescribeImage   = "describe_image"
	ToolEmbed           = "embed"
	ToolDetect          = "detect"
	ToolRerank          = "rerank"
	ToolGenerateVideo   = "generate_video"
	ToolGenerateSound   = "generate_sound"
	ToolVoice           = "voice"
	ToolFace            = "face"
	ToolVoiceProfile    = "voice_profile"
	ToolListModels      = "list_models"
	ToolUpscaleImage    = "upscale_image"
	ToolTransformAudio  = "transform_audio"
)

// ClientToolSpec is what a client tool needs from the API: the route it
// calls, the feature permission that route checks, and the model use case
// the model must have. The API route enforces the permission, because the
// call reaches it as the acting user; this table is for tests and for the
// coverage check "every inference route has a tool".
//
// A tool with an action parameter (voice, face, voice_profile) or an optional input that
// picks another route (generate_image with an image) has one row per route:
// Action tells them apart. The first row of a tool is its default.
type ClientToolSpec struct {
	Tool string
	// Action names the variant of a tool that has several routes. Empty for
	// the main route.
	Action  string
	Method  string
	Route   string
	Feature string // NoFeature when the route checks none
	// Flag is the use case an installed model must have. FLAG_ANY (zero): the
	// tool needs no model, as voice_profile, which only manages data.
	Flag config.ModelConfigUsecase
	// Capability names the use case in messages, for example "image".
	Capability string
}

// NoFeature is the Feature of a client tool whose route needs no feature
// permission.
const NoFeature = ""

// ClientTools lists the client tools in registration order.
var ClientTools = []ClientToolSpec{
	{ToolGenerateImage, "", "POST", "/v1/images/generations", auth.FeatureImages, config.FLAG_IMAGE, "image generation"},
	{ToolGenerateImage, ActionEdit, "POST", "/v1/images/inpainting", auth.FeatureImages, config.FLAG_IMAGE, "image generation"},
	{ToolSpeak, "", "POST", "/v1/audio/speech", auth.FeatureAudioSpeech, config.FLAG_TTS, "text-to-speech"},
	{ToolTranscribeAudio, "", "POST", "/v1/audio/transcriptions", auth.FeatureAudioTranscription, config.FLAG_TRANSCRIPT, "transcription"},
	{ToolDescribeImage, "", "POST", "/v1/chat/completions", auth.FeatureChat, config.FLAG_VISION, "vision"},
	{ToolEmbed, "", "POST", "/v1/embeddings", auth.FeatureEmbeddings, config.FLAG_EMBEDDINGS, "embedding"},
	{ToolDetect, "", "POST", "/v1/detection", auth.FeatureDetection, config.FLAG_DETECTION, "detection"},
	{ToolRerank, "", "POST", "/v1/rerank", auth.FeatureRerank, config.FLAG_RERANK, "rerank"},
	{ToolGenerateVideo, "", "POST", "/video", auth.FeatureVideo, config.FLAG_VIDEO, "video generation"},
	{ToolGenerateSound, "", "POST", "/v1/sound-generation", auth.FeatureSound, config.FLAG_SOUND_GENERATION, "sound generation"},
	{ToolVoice, ActionVerify, "POST", "/v1/voice/verify", auth.FeatureVoiceRecognition, config.FLAG_SPEAKER_RECOGNITION, "speaker recognition"},
	{ToolVoice, ActionIdentify, "POST", "/v1/voice/identify", auth.FeatureVoiceRecognition, config.FLAG_SPEAKER_RECOGNITION, "speaker recognition"},
	{ToolFace, ActionVerify, "POST", "/v1/face/verify", auth.FeatureFaceRecognition, config.FLAG_FACE_RECOGNITION, "face recognition"},
	{ToolFace, ActionIdentify, "POST", "/v1/face/identify", auth.FeatureFaceRecognition, config.FLAG_FACE_RECOGNITION, "face recognition"},
	{ToolVoiceProfile, ActionCreate, "POST", "/api/voice-profiles", auth.FeatureAudioSpeech, config.FLAG_ANY, "voice profiles"},
	{ToolVoiceProfile, ActionList, "GET", "/api/voice-profiles", auth.FeatureAudioSpeech, config.FLAG_ANY, "voice profiles"},
	{ToolVoiceProfile, ActionDelete, "DELETE", "/api/voice-profiles/:id", auth.FeatureAudioSpeech, config.FLAG_ANY, "voice profiles"},
	{ToolUpscaleImage, "", "POST", "/v1/images/upscale", auth.FeatureImages, config.FLAG_UPSCALE, "image upscaling"},
	{ToolTransformAudio, "", "POST", "/audio/transformations", auth.FeatureAudioTransform, config.FLAG_AUDIO_TRANSFORM, "audio transformation"},
	// list_models has no feature: any signed-in user may list the models the
	// route shows them (their own allow-list applies).
	{ToolListModels, "", "GET", "/v1/models/capabilities", NoFeature, config.FLAG_ANY, "models"},
}

// ActionEdit is the Action of the generate_image row that repaints an image.
const ActionEdit = "edit"

// clientSpec returns the first (main) spec of a client tool.
func clientSpec(tool string) ClientToolSpec {
	for _, s := range ClientTools {
		if s.Tool == tool {
			return s
		}
	}
	return ClientToolSpec{}
}

// clientSpecAction returns the spec of one action of a client tool.
func clientSpecAction(tool, action string) ClientToolSpec {
	for _, s := range ClientTools {
		if s.Tool == tool && s.Action == action {
			return s
		}
	}
	return ClientToolSpec{}
}

// ToolGroupOf returns the group of a tool name: GroupClient for a client
// tool, GroupManage for every other tool.
func ToolGroupOf(name string) ToolGroup {
	for _, s := range ClientTools {
		if s.Tool == name {
			return GroupClient
		}
	}
	return GroupManage
}

// ToolGroups maps each client tool to its group, for a caller that wants to
// register or filter the two groups apart. A tool that is not in the map is a
// manage tool.
func ToolGroups() map[string]ToolGroup {
	out := make(map[string]ToolGroup, len(ClientTools))
	for _, s := range ClientTools {
		out[s.Tool] = GroupClient
	}
	return out
}
