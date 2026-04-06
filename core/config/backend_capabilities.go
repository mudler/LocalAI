package config

import (
	"slices"
	"strings"
)

// Usecase name constants — the canonical string values used in gallery entries,
// model configs (known_usecases), and UsecaseInfoMap keys.
const (
	UsecaseChat            = "chat"
	UsecaseCompletion      = "completion"
	UsecaseEdit            = "edit"
	UsecaseVision          = "vision"
	UsecaseEmbeddings      = "embeddings"
	UsecaseTokenize        = "tokenize"
	UsecaseImage           = "image"
	UsecaseVideo           = "video"
	UsecaseTranscript      = "transcript"
	UsecaseTTS             = "tts"
	UsecaseSoundGeneration = "sound_generation"
	UsecaseRerank          = "rerank"
	UsecaseDetection       = "detection"
	UsecaseVAD             = "vad"
)

// GRPCMethod identifies a Backend service RPC from backend.proto.
type GRPCMethod string

const (
	MethodPredict            GRPCMethod = "Predict"
	MethodPredictStream      GRPCMethod = "PredictStream"
	MethodEmbedding          GRPCMethod = "Embedding"
	MethodGenerateImage      GRPCMethod = "GenerateImage"
	MethodGenerateVideo      GRPCMethod = "GenerateVideo"
	MethodAudioTranscription GRPCMethod = "AudioTranscription"
	MethodTTS                GRPCMethod = "TTS"
	MethodTTSStream          GRPCMethod = "TTSStream"
	MethodSoundGeneration    GRPCMethod = "SoundGeneration"
	MethodTokenizeString     GRPCMethod = "TokenizeString"
	MethodDetect             GRPCMethod = "Detect"
	MethodRerank             GRPCMethod = "Rerank"
	MethodVAD                GRPCMethod = "VAD"
)

// UsecaseInfo describes a single known_usecase value and how it maps
// to the gRPC backend API.
type UsecaseInfo struct {
	// Flag is the ModelConfigUsecase bitmask value.
	Flag ModelConfigUsecase
	// GRPCMethod is the primary Backend service RPC this usecase maps to.
	GRPCMethod GRPCMethod
	// IsModifier is true when this usecase doesn't map to its own gRPC RPC
	// but modifies how another RPC behaves (e.g., vision uses Predict with images).
	IsModifier bool
	// DependsOn names the usecase(s) this modifier requires (e.g., "chat").
	DependsOn string
	// Description is a human/LLM-readable explanation of what this usecase means.
	Description string
}

// UsecaseInfoMap maps each known_usecase string to its gRPC and semantic info.
var UsecaseInfoMap = map[string]UsecaseInfo{
	UsecaseChat: {
		Flag:        FLAG_CHAT,
		GRPCMethod:  MethodPredict,
		Description: "Conversational/instruction-following via the Predict RPC with chat templates.",
	},
	UsecaseCompletion: {
		Flag:        FLAG_COMPLETION,
		GRPCMethod:  MethodPredict,
		Description: "Text completion via the Predict RPC with a completion template.",
	},
	UsecaseEdit: {
		Flag:        FLAG_EDIT,
		GRPCMethod:  MethodPredict,
		Description: "Text editing via the Predict RPC with an edit template.",
	},
	UsecaseVision: {
		Flag:        FLAG_VISION,
		GRPCMethod:  MethodPredict,
		IsModifier:  true,
		DependsOn:   UsecaseChat,
		Description: "The model accepts images alongside text in the Predict RPC. For llama-cpp this requires an mmproj file.",
	},
	UsecaseEmbeddings: {
		Flag:        FLAG_EMBEDDINGS,
		GRPCMethod:  MethodEmbedding,
		Description: "Vector embedding generation via the Embedding RPC.",
	},
	UsecaseTokenize: {
		Flag:        FLAG_TOKENIZE,
		GRPCMethod:  MethodTokenizeString,
		Description: "Tokenization via the TokenizeString RPC without running inference.",
	},
	UsecaseImage: {
		Flag:        FLAG_IMAGE,
		GRPCMethod:  MethodGenerateImage,
		Description: "Image generation via the GenerateImage RPC (Stable Diffusion, Flux, etc.).",
	},
	UsecaseVideo: {
		Flag:        FLAG_VIDEO,
		GRPCMethod:  MethodGenerateVideo,
		Description: "Video generation via the GenerateVideo RPC.",
	},
	UsecaseTranscript: {
		Flag:        FLAG_TRANSCRIPT,
		GRPCMethod:  MethodAudioTranscription,
		Description: "Speech-to-text via the AudioTranscription RPC.",
	},
	UsecaseTTS: {
		Flag:        FLAG_TTS,
		GRPCMethod:  MethodTTS,
		Description: "Text-to-speech via the TTS RPC.",
	},
	UsecaseSoundGeneration: {
		Flag:        FLAG_SOUND_GENERATION,
		GRPCMethod:  MethodSoundGeneration,
		Description: "Music/sound generation via the SoundGeneration RPC (not speech).",
	},
	UsecaseRerank: {
		Flag:        FLAG_RERANK,
		GRPCMethod:  MethodRerank,
		Description: "Document reranking via the Rerank RPC.",
	},
	UsecaseDetection: {
		Flag:        FLAG_DETECTION,
		GRPCMethod:  MethodDetect,
		Description: "Object detection via the Detect RPC with bounding boxes.",
	},
	UsecaseVAD: {
		Flag:        FLAG_VAD,
		GRPCMethod:  MethodVAD,
		Description: "Voice activity detection via the VAD RPC.",
	},
}

// BackendCapability describes which gRPC methods and usecases a backend supports.
// Derived from reviewing actual implementations in backend/go/ and backend/python/.
type BackendCapability struct {
	// GRPCMethods lists the Backend service RPCs this backend implements.
	GRPCMethods []GRPCMethod
	// PossibleUsecases lists all usecase strings this backend can support.
	PossibleUsecases []string
	// DefaultUsecases lists the conservative safe defaults.
	DefaultUsecases []string
	// AcceptsImages indicates multimodal image input in Predict.
	AcceptsImages bool
	// AcceptsVideos indicates multimodal video input in Predict.
	AcceptsVideos bool
	// AcceptsAudios indicates multimodal audio input in Predict.
	AcceptsAudios bool
	// Description is a human-readable summary of the backend.
	Description string
}

// BackendCapabilities maps each backend name (as used in model configs and gallery
// entries) to its verified capabilities. This is the single source of truth for
// what each backend supports.
//
// Backend names use hyphens (e.g., "llama-cpp") matching the gallery convention.
// Use NormalizeBackendName() for names with dots (e.g., "llama.cpp").
var BackendCapabilities = map[string]BackendCapability{
	// --- LLM / text generation backends ---
	"llama-cpp": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodPredictStream, MethodEmbedding, MethodTokenizeString},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseEdit, UsecaseEmbeddings, UsecaseTokenize, UsecaseVision},
		DefaultUsecases:  []string{UsecaseChat},
		AcceptsImages:    true, // requires mmproj
		Description:      "llama.cpp GGUF models — LLM inference with optional vision via mmproj",
	},
	"vllm": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodPredictStream, MethodEmbedding},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseEmbeddings, UsecaseVision},
		DefaultUsecases:  []string{UsecaseChat},
		AcceptsImages:    true,
		AcceptsVideos:    true,
		Description:      "vLLM engine — high-throughput LLM serving with optional multimodal",
	},
	"vllm-omni": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodPredictStream, MethodGenerateImage, MethodGenerateVideo, MethodTTS},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseImage, UsecaseVideo, UsecaseTTS, UsecaseVision},
		DefaultUsecases:  []string{UsecaseChat},
		AcceptsImages:    true,
		AcceptsVideos:    true,
		AcceptsAudios:    true,
		Description:      "vLLM omni-modal — supports text, image, video generation and TTS",
	},
	"transformers": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodPredictStream, MethodEmbedding, MethodTTS, MethodSoundGeneration},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseEmbeddings, UsecaseTTS, UsecaseSoundGeneration},
		DefaultUsecases:  []string{UsecaseChat},
		Description:      "HuggingFace transformers — general-purpose Python inference",
	},
	"mlx": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodPredictStream, MethodEmbedding},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseEmbeddings},
		DefaultUsecases:  []string{UsecaseChat},
		Description:      "Apple MLX framework — optimized for Apple Silicon",
	},
	"mlx-distributed": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodPredictStream, MethodEmbedding},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseEmbeddings},
		DefaultUsecases:  []string{UsecaseChat},
		Description:      "MLX distributed inference across multiple Apple Silicon devices",
	},
	"mlx-vlm": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodPredictStream, MethodEmbedding},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseEmbeddings, UsecaseVision},
		DefaultUsecases:  []string{UsecaseChat, UsecaseVision},
		AcceptsImages:    true,
		AcceptsAudios:    true,
		Description:      "MLX vision-language models with multimodal input",
	},
	"mlx-audio": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodTTS},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseTTS},
		DefaultUsecases:  []string{UsecaseChat},
		Description:      "MLX audio models — text generation and TTS",
	},

	// --- Image/video generation backends ---
	"diffusers": {
		GRPCMethods:      []GRPCMethod{MethodGenerateImage, MethodGenerateVideo},
		PossibleUsecases: []string{UsecaseImage, UsecaseVideo},
		DefaultUsecases:  []string{UsecaseImage},
		Description:      "HuggingFace diffusers — Stable Diffusion, Flux, video generation",
	},
	"stablediffusion": {
		GRPCMethods:      []GRPCMethod{MethodGenerateImage},
		PossibleUsecases: []string{UsecaseImage},
		DefaultUsecases:  []string{UsecaseImage},
		Description:      "Stable Diffusion native backend",
	},
	"stablediffusion-ggml": {
		GRPCMethods:      []GRPCMethod{MethodGenerateImage},
		PossibleUsecases: []string{UsecaseImage},
		DefaultUsecases:  []string{UsecaseImage},
		Description:      "Stable Diffusion via GGML quantized models",
	},

	// --- Speech-to-text backends ---
	"whisper": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription, MethodVAD},
		PossibleUsecases: []string{UsecaseTranscript, UsecaseVAD},
		DefaultUsecases:  []string{UsecaseTranscript},
		Description:      "OpenAI Whisper — speech recognition and voice activity detection",
	},
	"faster-whisper": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription},
		PossibleUsecases: []string{UsecaseTranscript},
		DefaultUsecases:  []string{UsecaseTranscript},
		Description:      "CTranslate2-accelerated Whisper for faster transcription",
	},
	"whisperx": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription},
		PossibleUsecases: []string{UsecaseTranscript},
		DefaultUsecases:  []string{UsecaseTranscript},
		Description:      "WhisperX — Whisper with word-level timestamps and speaker diarization",
	},
	"moonshine": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription},
		PossibleUsecases: []string{UsecaseTranscript},
		DefaultUsecases:  []string{UsecaseTranscript},
		Description:      "Moonshine speech recognition",
	},
	"nemo": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription},
		PossibleUsecases: []string{UsecaseTranscript},
		DefaultUsecases:  []string{UsecaseTranscript},
		Description:      "NVIDIA NeMo speech recognition",
	},
	"qwen-asr": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription},
		PossibleUsecases: []string{UsecaseTranscript},
		DefaultUsecases:  []string{UsecaseTranscript},
		Description:      "Qwen automatic speech recognition",
	},
	"voxtral": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription},
		PossibleUsecases: []string{UsecaseTranscript},
		DefaultUsecases:  []string{UsecaseTranscript},
		Description:      "Voxtral speech recognition",
	},
	"vibevoice": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription, MethodTTS},
		PossibleUsecases: []string{UsecaseTranscript, UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTranscript, UsecaseTTS},
		Description:      "VibeVoice — bidirectional speech (transcription and synthesis)",
	},

	// --- TTS backends ---
	"piper": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "Piper — fast neural TTS optimized for Raspberry Pi",
	},
	"kokoro": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "Kokoro TTS",
	},
	"coqui": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "Coqui TTS — multi-speaker neural synthesis",
	},
	"kitten-tts": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "Kitten TTS",
	},
	"outetts": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "OuteTTS",
	},
	"pocket-tts": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "Pocket TTS — lightweight text-to-speech",
	},
	"qwen-tts": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "Qwen TTS",
	},
	"faster-qwen3-tts": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "Faster Qwen3 TTS — accelerated Qwen TTS",
	},
	"fish-speech": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "Fish Speech TTS",
	},
	"neutts": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "NeuTTS — neural text-to-speech",
	},
	"chatterbox": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "Chatterbox TTS",
	},
	"voxcpm": {
		GRPCMethods:      []GRPCMethod{MethodTTS, MethodTTSStream},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		Description:      "VoxCPM TTS with streaming support",
	},

	// --- Sound generation backends ---
	"ace-step": {
		GRPCMethods:      []GRPCMethod{MethodTTS, MethodSoundGeneration},
		PossibleUsecases: []string{UsecaseTTS, UsecaseSoundGeneration},
		DefaultUsecases:  []string{UsecaseSoundGeneration},
		Description:      "ACE-Step — music and sound generation",
	},
	"acestep-cpp": {
		GRPCMethods:      []GRPCMethod{MethodSoundGeneration},
		PossibleUsecases: []string{UsecaseSoundGeneration},
		DefaultUsecases:  []string{UsecaseSoundGeneration},
		Description:      "ACE-Step C++ — native sound generation",
	},
	"transformers-musicgen": {
		GRPCMethods:      []GRPCMethod{MethodTTS, MethodSoundGeneration},
		PossibleUsecases: []string{UsecaseTTS, UsecaseSoundGeneration},
		DefaultUsecases:  []string{UsecaseSoundGeneration},
		Description:      "Meta MusicGen via transformers — music generation from text",
	},

	// --- Utility backends ---
	"rerankers": {
		GRPCMethods:      []GRPCMethod{MethodRerank},
		PossibleUsecases: []string{UsecaseRerank},
		DefaultUsecases:  []string{UsecaseRerank},
		Description:      "Cross-encoder reranking models",
	},
	"rfdetr": {
		GRPCMethods:      []GRPCMethod{MethodDetect},
		PossibleUsecases: []string{UsecaseDetection},
		DefaultUsecases:  []string{UsecaseDetection},
		Description:      "RF-DETR object detection",
	},
	"silero-vad": {
		GRPCMethods:      []GRPCMethod{MethodVAD},
		PossibleUsecases: []string{UsecaseVAD},
		DefaultUsecases:  []string{UsecaseVAD},
		Description:      "Silero VAD — voice activity detection",
	},
}

// NormalizeBackendName converts backend names to the canonical hyphenated form
// used in gallery entries (e.g., "llama.cpp" → "llama-cpp").
func NormalizeBackendName(backend string) string {
	return strings.ReplaceAll(backend, ".", "-")
}

// GetBackendCapability returns the capability info for a backend, or nil if unknown.
// Handles backend name normalization.
func GetBackendCapability(backend string) *BackendCapability {
	if cap, ok := BackendCapabilities[NormalizeBackendName(backend)]; ok {
		return &cap
	}
	return nil
}

// PossibleUsecasesForBackend returns all usecases a backend can support.
// Returns nil if the backend is unknown.
func PossibleUsecasesForBackend(backend string) []string {
	if cap := GetBackendCapability(backend); cap != nil {
		return cap.PossibleUsecases
	}
	return nil
}

// DefaultUsecasesForBackend returns the conservative default usecases.
// Returns nil if the backend is unknown.
func DefaultUsecasesForBackendCap(backend string) []string {
	if cap := GetBackendCapability(backend); cap != nil {
		return cap.DefaultUsecases
	}
	return nil
}

// IsValidUsecaseForBackend checks whether a usecase is in a backend's possible set.
// Returns true for unknown backends (permissive fallback).
func IsValidUsecaseForBackend(backend, usecase string) bool {
	cap := GetBackendCapability(backend)
	if cap == nil {
		return true // unknown backend — don't restrict
	}
	return slices.Contains(cap.PossibleUsecases, usecase)
}

// AllBackendNames returns a sorted list of all known backend names.
func AllBackendNames() []string {
	names := make([]string, 0, len(BackendCapabilities))
	for name := range BackendCapabilities {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
