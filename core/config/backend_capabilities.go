package config

import (
	"slices"
	"strings"
)

// Usecase name constants — the canonical string values used in gallery entries,
// model configs (known_usecases), and UsecaseInfoMap keys.
const (
	UsecaseChat                = "chat"
	UsecaseCompletion          = "completion"
	UsecaseEdit                = "edit"
	UsecaseVision              = "vision"
	UsecaseEmbeddings          = "embeddings"
	UsecaseTokenize            = "tokenize"
	UsecaseImage               = "image"
	UsecaseVideo               = "video"
	UsecaseTranscript          = "transcript"
	UsecaseTTS                 = "tts"
	UsecaseSoundGeneration     = "sound_generation"
	UsecaseRerank              = "rerank"
	UsecaseDetection           = "detection"
	UsecaseDepth               = "depth"
	UsecaseVAD                 = "vad"
	UsecaseAudioTransform      = "audio_transform"
	UsecaseDiarization         = "diarization"
	UsecaseSoundClassification = "sound_classification"
	UsecaseRealtimeAudio       = "realtime_audio"
	UsecaseFaceRecognition     = "face_recognition"
	UsecaseSpeakerRecognition  = "speaker_recognition"
	UsecaseTokenClassify       = "token_classify"
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
	MethodDepth              GRPCMethod = "Depth"
	MethodRerank             GRPCMethod = "Rerank"
	MethodVAD                GRPCMethod = "VAD"
	MethodAudioTransform     GRPCMethod = "AudioTransform"
	MethodDiarize            GRPCMethod = "Diarize"
	MethodSoundDetection     GRPCMethod = "SoundDetection"
	MethodAudioToAudioStream GRPCMethod = "AudioToAudioStream"
	MethodFaceVerify         GRPCMethod = "FaceVerify"
	MethodFaceAnalyze        GRPCMethod = "FaceAnalyze"
	MethodVoiceVerify        GRPCMethod = "VoiceVerify"
	MethodVoiceEmbed         GRPCMethod = "VoiceEmbed"
	MethodVoiceAnalyze       GRPCMethod = "VoiceAnalyze"
	MethodTokenClassify      GRPCMethod = "TokenClassify"
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
		Description: "Video generation via the GenerateVideo RPC, with optional image or audio conditioning when supported by the backend.",
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
	UsecaseDepth: {
		Flag:        FLAG_DEPTH,
		GRPCMethod:  MethodDepth,
		Description: "Per-pixel metric depth, camera pose and 3D point cloud via the Depth RPC (Depth Anything 3).",
	},
	UsecaseVAD: {
		Flag:        FLAG_VAD,
		GRPCMethod:  MethodVAD,
		Description: "Voice activity detection via the VAD RPC.",
	},
	UsecaseAudioTransform: {
		Flag:        FLAG_AUDIO_TRANSFORM,
		GRPCMethod:  MethodAudioTransform,
		Description: "Audio-in / audio-out transformations (echo cancellation, noise suppression, dereverberation, voice conversion) via the AudioTransform RPC.",
	},
	UsecaseDiarization: {
		Flag:        FLAG_DIARIZATION,
		GRPCMethod:  MethodDiarize,
		Description: "Speaker diarization (who-spoke-when, per-speaker segments) via the Diarize RPC.",
	},
	UsecaseSoundClassification: {
		Flag:        FLAG_SOUND_CLASSIFICATION,
		GRPCMethod:  MethodSoundDetection,
		Description: "Sound-event classification / audio tagging (scored AudioSet labels like baby cry, glass breaking, alarms) via the SoundDetection RPC.",
	},
	UsecaseRealtimeAudio: {
		Flag:        FLAG_REALTIME_AUDIO,
		GRPCMethod:  MethodAudioToAudioStream,
		Description: "Self-contained any-to-any audio model for the Realtime API — accepts microphone audio and emits speech + transcript (+ optional function calls) from a single backend via the AudioToAudioStream RPC.",
	},
	UsecaseFaceRecognition: {
		Flag:        FLAG_FACE_RECOGNITION,
		GRPCMethod:  MethodFaceVerify,
		Description: "Face recognition — verify identity, analyze attributes (age/gender/emotion) via FaceVerify and FaceAnalyze RPCs.",
	},
	UsecaseSpeakerRecognition: {
		Flag:        FLAG_SPEAKER_RECOGNITION,
		GRPCMethod:  MethodVoiceVerify,
		Description: "Speaker recognition — verify identity, embed and analyze voice via VoiceVerify, VoiceEmbed and VoiceAnalyze RPCs.",
	},
	UsecaseTokenClassify: {
		Flag:        FLAG_TOKEN_CLASSIFY,
		GRPCMethod:  MethodTokenClassify,
		Description: "Per-token classification (NER) via the TokenClassify RPC — the PII detector tier. Declared explicitly via known_usecases; never auto-guessed, since the token-classification head is not useful as general generation or embeddings.",
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
	// VoiceCloning describes the backend's per-request reference-audio
	// contract. Model variants that share a backend may narrow this further;
	// use VoiceCloningForModel for UI/API decisions.
	VoiceCloning *VoiceCloningCapability
	// Description is a human-readable summary of the backend.
	Description string
}

// VoiceCloningCapability is the model-facing contract for reusable reference
// voices. The first release intentionally accepts only browser-normalizable
// PCM WAV so every advertised backend sees the same input shape.
type VoiceCloningCapability struct {
	ReferenceTranscriptRequired bool     `json:"reference_transcript_required"`
	AcceptedAudioFormats        []string `json:"accepted_audio_formats"`
}

func referenceVoiceCloning() *VoiceCloningCapability {
	return &VoiceCloningCapability{
		ReferenceTranscriptRequired: true,
		AcceptedAudioFormats:        []string{"audio/wav"},
	}
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
	// privacy-filter is the standalone GGML engine (backend/cpp/privacy-filter,
	// wrapping privacy-filter.cpp) for the openai-privacy-filter PII/NER token
	// classifier — the dedicated TokenClassify path that replaces the
	// patched-llama.cpp route. Never auto-guessed; declared explicitly via
	// known_usecases: [token_classify].
	"privacy-filter": {
		GRPCMethods:      []GRPCMethod{MethodTokenClassify},
		PossibleUsecases: []string{UsecaseTokenClassify},
		DefaultUsecases:  []string{UsecaseTokenClassify},
		Description:      "privacy-filter.cpp — standalone GGML backend for openai-privacy-filter PII/NER token classification",
	},
	"vllm": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodPredictStream, MethodEmbedding},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseEmbeddings, UsecaseVision},
		DefaultUsecases:  []string{UsecaseChat},
		AcceptsImages:    true,
		AcceptsVideos:    true,
		Description:      "vLLM engine — high-throughput LLM serving with optional multimodal",
	},
	"sglang": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodPredictStream, MethodTokenizeString},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseTokenize, UsecaseVision},
		DefaultUsecases:  []string{UsecaseChat},
		AcceptsImages:    true,
		Description:      "SGLang — fast LLM inference with structured generation and optional vision",
	},
	"vllm-omni": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodPredictStream, MethodGenerateImage, MethodGenerateVideo, MethodTTS},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseImage, UsecaseVideo, UsecaseTTS, UsecaseVision},
		DefaultUsecases:  []string{UsecaseChat},
		AcceptsImages:    true,
		AcceptsVideos:    true,
		AcceptsAudios:    true,
		VoiceCloning:     referenceVoiceCloning(),
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
	"longcat-video": {
		GRPCMethods:      []GRPCMethod{MethodGenerateVideo},
		PossibleUsecases: []string{UsecaseVideo},
		DefaultUsecases:  []string{UsecaseVideo},
		AcceptsImages:    true,
		AcceptsAudios:    true,
		Description:      "LongCat-Video — text, image, and audio-conditioned avatar video generation on NVIDIA CUDA",
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
	"parakeet-cpp": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription},
		PossibleUsecases: []string{UsecaseTranscript},
		DefaultUsecases:  []string{UsecaseTranscript},
		Description:      "NVIDIA NeMo Parakeet ASR (parakeet.cpp)",
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
	"vibevoice-cpp": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription, MethodTTS, MethodTTSStream},
		PossibleUsecases: []string{UsecaseTranscript, UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTranscript, UsecaseTTS},
		VoiceCloning:     referenceVoiceCloning(),
		Description:      "VibeVoice C++ — bidirectional speech, C++ backend with streaming TTS",
	},
	"sherpa-onnx": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription, MethodTTS, MethodTTSStream, MethodVAD},
		PossibleUsecases: []string{UsecaseTranscript, UsecaseTTS, UsecaseVAD},
		DefaultUsecases:  []string{UsecaseTranscript},
		Description:      "Sherpa-ONNX — multi-model speech toolkit (ASR, TTS, VAD)",
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
		VoiceCloning:     referenceVoiceCloning(),
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
		VoiceCloning:     referenceVoiceCloning(),
		Description:      "Pocket TTS — lightweight text-to-speech",
	},
	"qwen-tts": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		VoiceCloning:     referenceVoiceCloning(),
		Description:      "Qwen TTS",
	},
	"qwen3-tts-cpp": {
		GRPCMethods:      []GRPCMethod{MethodTTS, MethodTTSStream},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		VoiceCloning:     referenceVoiceCloning(),
		Description:      "Qwen3 TTS C++ - text-to-speech with streaming, named speakers, voice design and cloning (qwentts.cpp / GGML)",
	},
	"faster-qwen3-tts": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		VoiceCloning:     referenceVoiceCloning(),
		Description:      "Faster Qwen3 TTS — accelerated Qwen TTS",
	},
	"fish-speech": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		VoiceCloning:     referenceVoiceCloning(),
		Description:      "Fish Speech TTS",
	},
	"neutts": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		VoiceCloning:     referenceVoiceCloning(),
		Description:      "NeuTTS — neural text-to-speech",
	},
	"chatterbox": {
		GRPCMethods:      []GRPCMethod{MethodTTS},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		VoiceCloning:     referenceVoiceCloning(),
		Description:      "Chatterbox TTS",
	},
	"voxcpm": {
		GRPCMethods:      []GRPCMethod{MethodTTS, MethodTTSStream},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		VoiceCloning:     referenceVoiceCloning(),
		Description:      "VoxCPM TTS with streaming support",
	},
	"omnivoice-cpp": {
		GRPCMethods:      []GRPCMethod{MethodTTS, MethodTTSStream},
		PossibleUsecases: []string{UsecaseTTS},
		DefaultUsecases:  []string{UsecaseTTS},
		VoiceCloning:     referenceVoiceCloning(),
		Description:      "OmniVoice C++ — multilingual TTS with streaming voice cloning and voice design",
	},
	"crispasr": {
		GRPCMethods:      []GRPCMethod{MethodAudioTranscription, MethodTTS, MethodTTSStream, MethodVAD},
		PossibleUsecases: []string{UsecaseTranscript, UsecaseTTS, UsecaseVAD},
		DefaultUsecases:  []string{UsecaseTranscript},
		VoiceCloning:     referenceVoiceCloning(),
		Description:      "CrispASR GGUF runtime — speech recognition, VAD, and model-dependent TTS",
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

	// --- Any-to-any audio backends ---
	"liquid-audio": {
		GRPCMethods:      []GRPCMethod{MethodPredict, MethodPredictStream, MethodAudioTranscription, MethodTTS, MethodAudioToAudioStream, MethodVAD},
		PossibleUsecases: []string{UsecaseChat, UsecaseCompletion, UsecaseTranscript, UsecaseTTS, UsecaseRealtimeAudio, UsecaseVAD},
		DefaultUsecases:  []string{UsecaseRealtimeAudio, UsecaseChat, UsecaseTranscript, UsecaseTTS, UsecaseVAD},
		AcceptsAudios:    true,
		Description:      "LFM2 / LFM2.5-Audio — self-contained any-to-any audio model for the Realtime API; also exposes chat, transcription, TTS and a stub energy-based VAD endpoint",
	},

	// --- Audio transform backends ---
	"localvqe": {
		GRPCMethods:      []GRPCMethod{MethodAudioTransform},
		PossibleUsecases: []string{UsecaseAudioTransform},
		DefaultUsecases:  []string{UsecaseAudioTransform},
		Description:      "LocalVQE — joint AEC, noise suppression, and dereverberation for 16 kHz mono speech",
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
	"rfdetr-cpp": {
		GRPCMethods:      []GRPCMethod{MethodDetect},
		PossibleUsecases: []string{UsecaseDetection},
		DefaultUsecases:  []string{UsecaseDetection},
		Description:      "RF-DETR C++ object detection",
	},
	"depth-anything": {
		GRPCMethods:      []GRPCMethod{MethodDepth, MethodPredict, MethodGenerateImage},
		PossibleUsecases: []string{UsecaseDepth},
		DefaultUsecases:  []string{UsecaseDepth},
		AcceptsImages:    true,
		Description:      "Depth Anything 3 C++ — per-pixel metric depth, camera pose and 3D point cloud",
	},

	// --- Face and speaker recognition backends ---
	"insightface": {
		GRPCMethods:      []GRPCMethod{MethodEmbedding, MethodDetect, MethodFaceVerify, MethodFaceAnalyze},
		PossibleUsecases: []string{UsecaseEmbeddings, UsecaseDetection, UsecaseFaceRecognition},
		DefaultUsecases:  []string{UsecaseFaceRecognition},
		AcceptsImages:    true,
		Description:      "InsightFace — face detection, embedding, verification and attribute analysis",
	},
	"speaker-recognition": {
		GRPCMethods:      []GRPCMethod{MethodVoiceVerify, MethodVoiceEmbed, MethodVoiceAnalyze},
		PossibleUsecases: []string{UsecaseSpeakerRecognition},
		DefaultUsecases:  []string{UsecaseSpeakerRecognition},
		Description:      "Speaker recognition — voice identity verification and analysis",
	},
	"voice-detect": {
		GRPCMethods:      []GRPCMethod{MethodVoiceVerify, MethodVoiceEmbed, MethodVoiceAnalyze},
		PossibleUsecases: []string{UsecaseSpeakerRecognition},
		DefaultUsecases:  []string{UsecaseSpeakerRecognition},
		Description:      "voice-detect.cpp: C++/ggml speaker embedding, verification and voice analysis (age/gender/emotion)",
	},
	"face-detect": {
		GRPCMethods:      []GRPCMethod{MethodEmbedding, MethodDetect, MethodFaceVerify, MethodFaceAnalyze},
		PossibleUsecases: []string{UsecaseEmbeddings, UsecaseDetection, UsecaseFaceRecognition},
		DefaultUsecases:  []string{UsecaseFaceRecognition},
		AcceptsImages:    true,
		Description:      "face-detect.cpp: C++/ggml face detection, embedding, verification and attribute analysis",
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

// llamaCppChannelSuffixes are the release-channel suffixes appended to a
// llama.cpp backend name in the gallery ("llama-cpp" vs
// "llama-cpp-development"). They carry no engine information, so they are
// stripped before the family check below.
var llamaCppChannelSuffixes = []string{"-development", "-quantization"}

// IsLlamaCppBackend reports whether a backend name refers to a build of the
// llama.cpp gRPC server. The gallery ships one concrete backend per hardware
// capability ("vulkan-llama-cpp", "cuda12-llama-cpp", "metal-llama-cpp", ...)
// behind the "llama-cpp" meta name, and an operator may pin any of them in a
// model config. They all run the same server, so anything gated on "is this
// llama.cpp" must accept the whole family: an exact match against "llama-cpp"
// silently skips every pinned variant (see #10945, where skipping the media
// marker probe broke all vision requests).
//
// The empty name matches too: it is the GGUF auto-detect path, which resolves
// to llama.cpp.
//
// ik-llama.cpp is deliberately excluded. It is a separate engine with its own
// gRPC server that happens to share the "-llama-cpp" suffix.
func IsLlamaCppBackend(backend string) bool {
	name := NormalizeBackendName(backend)
	if name == "" {
		return true
	}
	for _, suffix := range llamaCppChannelSuffixes {
		name = strings.TrimSuffix(name, suffix)
	}
	if strings.HasSuffix(name, "ik-llama-cpp") {
		return false
	}
	return name == "llama-cpp" || strings.HasSuffix(name, "-llama-cpp")
}

// nonLlamaSamplerBackends lists backends whose native sampler defaults differ
// from llama.cpp's, so LocalAI must NOT inject llama.cpp's top_k=40 default for
// them (issue #6632). mlx_lm's intended default is top_k=0 (disabled) and mlx
// does not remap 0->40, so shipping 40 silently changes sampling for clients
// that omit top_k. Leaving TopK nil lets the wire value default to 0.
//
// This is intentionally a small allow-list of KNOWN non-llama backends: empty
// and unknown backends fall through to the llama.cpp default to preserve the
// GGUF auto-detect path's behavior.
var nonLlamaSamplerBackends = map[string]struct{}{
	"mlx":             {},
	"mlx-vlm":         {},
	"mlx-distributed": {},
}

// UsesLlamaSamplerDefaults reports whether a backend should receive llama.cpp's
// sampler defaults (e.g. top_k=40). Empty/unknown backends return true so the
// GGUF auto-detect path (which resolves to llama.cpp) keeps today's behavior;
// only the known non-llama backends in nonLlamaSamplerBackends return false.
func UsesLlamaSamplerDefaults(backend string) bool {
	if backend == "" {
		return true
	}
	_, isNonLlama := nonLlamaSamplerBackends[NormalizeBackendName(backend)]
	return !isNonLlama
}

// UsesLlamaCppServingOptions reports whether a backend understands llama.cpp's
// serving-tuning model options - the free-form option strings cache_reuse /
// n_cache_reuse (cross-request KV-prefix reuse) and parallel / n_parallel
// (concurrent slots). These are llama.cpp server flags; LocalAI injects them as
// defaults, but a backend that strictly validates its options (e.g.
// longcat-video) rejects an unknown one with "unknown model option(s)" at
// LoadModel. Only the llama.cpp backend - and the empty/auto-detect case, which
// resolves to llama.cpp from a GGUF file, mirroring how llamaCppDefaults is
// registered - should receive them.
//
// This is an allow-list on purpose (unlike UsesLlamaSamplerDefaults's
// deny-list): these options are meaningful to no other backend, so a new
// backend defaults to NOT getting them rather than breaking the same way.
func UsesLlamaCppServingOptions(backend string) bool {
	switch NormalizeBackendName(backend) {
	case "", "llama-cpp":
		return true
	}
	return false
}

// GetBackendCapability returns the capability info for a backend, or nil if unknown.
// Handles backend name normalization.
func GetBackendCapability(backend string) *BackendCapability {
	if cap, ok := BackendCapabilities[NormalizeBackendName(backend)]; ok {
		return &cap
	}
	return nil
}

// VoiceCloningForModel returns the reference-audio contract only when the
// installed model variant can honor it. Several backends serve both Base
// (voice cloning) and CustomVoice/VoiceDesign models, so backend name alone is
// deliberately insufficient. Operators with custom filenames can opt in or
// out explicitly with tts.voice_cloning; the model option spelling remains a
// compatibility fallback for configurations created before the typed field.
func VoiceCloningForModel(cfg *ModelConfig) *VoiceCloningCapability {
	if cfg == nil {
		return nil
	}
	backend := NormalizeBackendName(cfg.Backend)
	capability := GetBackendCapability(backend)
	if capability == nil || capability.VoiceCloning == nil {
		return nil
	}
	if cfg.VoiceCloning != nil {
		if !*cfg.VoiceCloning {
			return nil
		}
		return cloneVoiceCloningCapability(capability.VoiceCloning)
	}

	if enabled, explicit := voiceCloningOverride(cfg.Options); explicit {
		if !enabled {
			return nil
		}
		return cloneVoiceCloningCapability(capability.VoiceCloning)
	}

	identity := strings.ToLower(strings.Join([]string{cfg.Name, cfg.Model, strings.Join(cfg.Options, " ")}, " "))
	supported := false
	switch backend {
	case "qwen3-tts-cpp", "qwen-tts", "vllm-omni":
		supported = strings.Contains(identity, "base") || strings.Contains(identity, "voiceclone") || strings.Contains(identity, "voice_clone")
	case "vibevoice-cpp":
		// Realtime 0.5B consumes a precomputed .gguf voice prompt; the 1.5B
		// path consumes raw WAV references per request.
		supported = strings.Contains(identity, "1.5b")
	case "coqui":
		supported = strings.Contains(identity, "xtts") || strings.Contains(identity, "your_tts")
	case "crispasr":
		supported = strings.Contains(identity, "f5-tts") || strings.Contains(identity, "f5_tts")
	default:
		supported = true
	}
	if !supported {
		return nil
	}
	return cloneVoiceCloningCapability(capability.VoiceCloning)
}

func voiceCloningOverride(options []string) (enabled, explicit bool) {
	for _, option := range options {
		parts := strings.FieldsFunc(option, func(r rune) bool { return r == ':' || r == '=' })
		if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "voice_cloning") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(parts[1])) {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off":
			return false, true
		}
	}
	return false, false
}

func cloneVoiceCloningCapability(capability *VoiceCloningCapability) *VoiceCloningCapability {
	if capability == nil {
		return nil
	}
	clone := *capability
	clone.AcceptedAudioFormats = slices.Clone(capability.AcceptedAudioFormats)
	return &clone
}

// PossibleUsecasesForBackend returns all usecases a backend can support.
// Returns nil if the backend is unknown.
func PossibleUsecasesForBackend(backend string) []string {
	if cap := GetBackendCapability(backend); cap != nil {
		return cap.PossibleUsecases
	}
	return nil
}

// DefaultUsecasesForBackendCap returns the conservative default usecases.
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
