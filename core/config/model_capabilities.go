package config

import "slices"

// This file is the single source of truth for deriving a model's user-facing
// capabilities and input/output modalities from its ModelConfig. Both the
// OpenAI-compatible /v1/models/capabilities endpoint and the Ollama-compatible
// /api/tags|/api/show surface consume these, so the vocabulary stays consistent
// across clients. Keep the detection heuristics here rather than duplicating
// them per endpoint.

// Canonical model modality values used by config declarations and discovery APIs.
const (
	ModalityText  = "text"
	ModalityImage = "image"
	ModalityAudio = "audio"
	ModalityVideo = "video"
	Modality3D    = "3d"
)

var modalityOrder = []string{ModalityText, ModalityImage, ModalityAudio, ModalityVideo, Modality3D}

func declaredModalities(modalities []string) map[string]bool {
	declared := make(map[string]bool, len(modalities))
	for _, modality := range modalities {
		if slices.Contains(modalityOrder, modality) {
			declared[modality] = true
		}
	}
	return declared
}

func orderedModalities(modalities map[string]bool) []string {
	result := make([]string, 0, len(modalityOrder))
	for _, modality := range modalityOrder {
		if modalities[modality] {
			result = append(result, modality)
		}
	}
	return result
}

// VisionSupported reports whether the model can accept image inputs.
//
// We deliberately avoid HasUsecases(FLAG_VISION): GuessUsecases has no
// FLAG_VISION branch and reports true for any chat model, so it would paint
// vision onto text-only models. Instead we look for explicit signals: the
// declared input modality or KnownUsecases bit, a multimodal projector, or a
// template/backend multimodal marker.
func (c *ModelConfig) VisionSupported() bool {
	if slices.Contains(c.KnownInputModalities, ModalityImage) {
		return true
	}
	if c.KnownUsecases != nil && (*c.KnownUsecases&FLAG_VISION) == FLAG_VISION {
		return true
	}
	if c.MMProj != "" {
		return true
	}
	if c.TemplateConfig.Multimodal != "" {
		return true
	}
	if c.MediaMarker != "" {
		return true
	}
	return false
}

// ToolSupported reports whether the model is wired up for tool / function
// calling. We look for any of the explicit knobs LocalAI uses to drive
// function-call extraction (regex match, response regex, grammar triggers, XML
// format) or the auto-detected tool-format markers the llama.cpp backend
// populates during model load.
func (c *ModelConfig) ToolSupported() bool {
	fc := c.FunctionsConfig
	if fc.ToolFormatMarkers != nil && fc.ToolFormatMarkers.FormatType != "" {
		return true
	}
	if len(fc.JSONRegexMatch) > 0 || len(fc.ResponseRegex) > 0 {
		return true
	}
	if fc.XMLFormatPreset != "" || fc.XMLFormat != nil {
		return true
	}
	if len(fc.GrammarConfig.GrammarTriggers) > 0 || fc.GrammarConfig.SchemaType != "" {
		return true
	}
	return false
}

// ThinkingSupported reports whether the model has reasoning / thinking enabled.
// LocalAI sets DisableReasoning=false (or leaves thinking markers configured)
// when the backend probe reports that the model supports thinking.
func (c *ModelConfig) ThinkingSupported() bool {
	rc := c.ReasoningConfig
	if rc.DisableReasoning != nil && !*rc.DisableReasoning {
		return true
	}
	if len(rc.ThinkingStartTokens) > 0 || len(rc.TagPairs) > 0 {
		// Explicit thinking markers imply support unless explicitly disabled.
		return rc.DisableReasoning == nil || !*rc.DisableReasoning
	}
	return false
}

// AudioInputSupported reports whether a chat/generation model accepts audio as
// input. Model configs can declare this directly; vLLM-family configs can also
// signal it through the per-prompt audio limit. Transcription models are
// handled separately in InputModalities via FLAG_TRANSCRIPT.
func (c *ModelConfig) AudioInputSupported() bool {
	return slices.Contains(c.KnownInputModalities, ModalityAudio) ||
		c.LimitMMPerPrompt.LimitAudioPerPrompt > 0
}

// VideoInputSupported reports whether a chat/generation model accepts video as
// input. Model configs can declare this directly; vLLM-family configs can also
// signal it through the per-prompt video limit. This is distinct from
// FLAG_VIDEO, which denotes video generation — an output modality.
func (c *ModelConfig) VideoInputSupported() bool {
	return slices.Contains(c.KnownInputModalities, ModalityVideo) ||
		c.LimitMMPerPrompt.LimitVideoPerPrompt > 0
}

// Capabilities returns the ordered list of capability strings the model
// supports, using the canonical usecase vocabulary (chat, vision, transcript,
// tts, embeddings, image, video, ...) plus the modifier capabilities "tools"
// and "thinking". Vision is resolved via VisionSupported (not HasUsecases) to
// avoid the guess-heuristic false positive.
func (c *ModelConfig) Capabilities() []string {
	chat := c.HasUsecases(FLAG_CHAT)
	completion := c.HasUsecases(FLAG_COMPLETION)

	var caps []string
	add := func(cond bool, name string) {
		if cond {
			caps = append(caps, name)
		}
	}

	add(chat, UsecaseChat)
	add(completion, UsecaseCompletion)
	add(c.HasUsecases(FLAG_EDIT), UsecaseEdit)
	add(c.HasUsecases(FLAG_EMBEDDINGS), UsecaseEmbeddings)
	add(c.HasUsecases(FLAG_RERANK), UsecaseRerank)
	// Vision is only meaningful as an image-understanding modifier on a chat/
	// completion model. Gating on (chat||completion) matches the Ollama surface
	// and avoids a false positive when config defaults hydrate a MediaMarker on
	// a non-chat model (e.g. a pure ASR/TTS backend).
	add((chat || completion) && c.VisionSupported(), UsecaseVision)
	// tools/thinking are modifiers on the chat/completion surface.
	add((chat || completion) && c.ToolSupported(), "tools")
	add((chat || completion) && c.ThinkingSupported(), "thinking")
	add(c.HasUsecases(FLAG_TRANSCRIPT), UsecaseTranscript)
	add(c.HasUsecases(FLAG_TTS), UsecaseTTS)
	add(c.HasUsecases(FLAG_SOUND_GENERATION), UsecaseSoundGeneration)
	add(c.HasUsecases(FLAG_IMAGE), UsecaseImage)
	add(c.HasUsecases(FLAG_VIDEO), UsecaseVideo)
	add(c.HasUsecases(FLAG_3D), Usecase3D)
	add(c.HasUsecases(FLAG_VAD), UsecaseVAD)
	add(c.HasUsecases(FLAG_DETECTION), UsecaseDetection)
	add(c.HasUsecases(FLAG_DEPTH), UsecaseDepth)
	add(c.HasUsecases(FLAG_AUDIO_TRANSFORM), UsecaseAudioTransform)
	add(c.HasUsecases(FLAG_DIARIZATION), UsecaseDiarization)
	add(c.HasUsecases(FLAG_SOUND_CLASSIFICATION), UsecaseSoundClassification)
	add(c.HasUsecases(FLAG_REALTIME_AUDIO), UsecaseRealtimeAudio)
	add(c.HasUsecases(FLAG_FACE_RECOGNITION), UsecaseFaceRecognition)
	add(c.HasUsecases(FLAG_SPEAKER_RECOGNITION), UsecaseSpeakerRecognition)
	return caps
}

// InputModalities returns the set of modalities (text, image, audio, video) the
// model accepts as input, ordered text→image→audio→video. This is what an
// attachment router consults to decide whether an image/audio/video file can be
// handed to the active model directly.
func (c *ModelConfig) InputModalities() []string {
	modalities := declaredModalities(c.KnownInputModalities)
	imageGen := c.HasUsecases(FLAG_IMAGE)
	videoGen := c.HasUsecases(FLAG_VIDEO)
	chatish := c.HasUsecases(FLAG_CHAT) || c.HasUsecases(FLAG_COMPLETION)

	textIn := chatish || c.HasUsecases(FLAG_EDIT) ||
		c.HasUsecases(FLAG_EMBEDDINGS) || c.HasUsecases(FLAG_RERANK) || c.HasUsecases(FLAG_TOKENIZE) ||
		c.HasUsecases(FLAG_TTS) || c.HasUsecases(FLAG_SOUND_GENERATION) || imageGen || videoGen

	// Image input via a chat model requires vision (gated on chat, like the
	// Ollama surface); detection/depth/face/3D models consume images directly.
	imageIn := (chatish && c.VisionSupported()) || c.LimitMMPerPrompt.LimitImagePerPrompt > 0 ||
		c.HasUsecases(FLAG_DETECTION) || c.HasUsecases(FLAG_DEPTH) || c.HasUsecases(FLAG_FACE_RECOGNITION) ||
		c.HasUsecases(FLAG_3D)

	audioIn := c.AudioInputSupported() || c.HasUsecases(FLAG_TRANSCRIPT) || c.HasUsecases(FLAG_AUDIO_TRANSFORM) ||
		c.HasUsecases(FLAG_REALTIME_AUDIO) || c.HasUsecases(FLAG_VAD) || c.HasUsecases(FLAG_DIARIZATION) ||
		c.HasUsecases(FLAG_SOUND_CLASSIFICATION) || c.HasUsecases(FLAG_SPEAKER_RECOGNITION)

	videoIn := c.VideoInputSupported()

	modalities[ModalityText] = modalities[ModalityText] || textIn
	modalities[ModalityImage] = modalities[ModalityImage] || imageIn
	modalities[ModalityAudio] = modalities[ModalityAudio] || audioIn
	modalities[ModalityVideo] = modalities[ModalityVideo] || videoIn
	return orderedModalities(modalities)
}

// OutputModalities returns the set of modalities (text, image, audio, video)
// the model produces, ordered text→image→audio→video.
func (c *ModelConfig) OutputModalities() []string {
	modalities := declaredModalities(c.KnownOutputModalities)
	textOut := c.HasUsecases(FLAG_CHAT) || c.HasUsecases(FLAG_COMPLETION) || c.HasUsecases(FLAG_EDIT) ||
		c.HasUsecases(FLAG_TRANSCRIPT)
	imageOut := c.HasUsecases(FLAG_IMAGE)
	audioOut := c.HasUsecases(FLAG_TTS) || c.HasUsecases(FLAG_SOUND_GENERATION) ||
		c.HasUsecases(FLAG_AUDIO_TRANSFORM) || c.HasUsecases(FLAG_REALTIME_AUDIO)
	videoOut := c.HasUsecases(FLAG_VIDEO)
	threeDOut := c.HasUsecases(FLAG_3D)

	modalities[ModalityText] = modalities[ModalityText] || textOut
	modalities[ModalityImage] = modalities[ModalityImage] || imageOut
	modalities[ModalityAudio] = modalities[ModalityAudio] || audioOut
	modalities[ModalityVideo] = modalities[ModalityVideo] || videoOut
	modalities[Modality3D] = modalities[Modality3D] || threeDOut
	return orderedModalities(modalities)
}
