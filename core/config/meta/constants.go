package meta

// Dynamic autocomplete provider constants (runtime lookup required).
const (
	ProviderBackends         = "backends"
	ProviderModels           = "models"
	ProviderModelsChat       = "models:chat"
	ProviderModelsTTS        = "models:tts"
	ProviderModelsTranscript = "models:transcript"
	ProviderModelsVAD        = "models:vad"
	ProviderModelsScore      = "models:score"
)

// Static option lists embedded directly in field metadata.

var QuantizationOptions = []FieldOption{
	{Value: "q4_0", Label: "Q4_0"},
	{Value: "q4_1", Label: "Q4_1"},
	{Value: "q5_0", Label: "Q5_0"},
	{Value: "q5_1", Label: "Q5_1"},
	{Value: "q8_0", Label: "Q8_0"},
	{Value: "q2_K", Label: "Q2_K"},
	{Value: "q3_K_S", Label: "Q3_K_S"},
	{Value: "q3_K_M", Label: "Q3_K_M"},
	{Value: "q3_K_L", Label: "Q3_K_L"},
	{Value: "q4_K_S", Label: "Q4_K_S"},
	{Value: "q4_K_M", Label: "Q4_K_M"},
	{Value: "q5_K_S", Label: "Q5_K_S"},
	{Value: "q5_K_M", Label: "Q5_K_M"},
	{Value: "q6_K", Label: "Q6_K"},
}

var CacheTypeOptions = []FieldOption{
	{Value: "f16", Label: "F16"},
	{Value: "f32", Label: "F32"},
	{Value: "q8_0", Label: "Q8_0"},
	{Value: "q4_0", Label: "Q4_0"},
	{Value: "q4_1", Label: "Q4_1"},
	{Value: "q5_0", Label: "Q5_0"},
	{Value: "q5_1", Label: "Q5_1"},
}

var DiffusersPipelineOptions = []FieldOption{
	{Value: "StableDiffusionPipeline", Label: "StableDiffusionPipeline"},
	{Value: "StableDiffusionImg2ImgPipeline", Label: "StableDiffusionImg2ImgPipeline"},
	{Value: "StableDiffusionXLPipeline", Label: "StableDiffusionXLPipeline"},
	{Value: "StableDiffusionXLImg2ImgPipeline", Label: "StableDiffusionXLImg2ImgPipeline"},
	{Value: "StableDiffusionDepth2ImgPipeline", Label: "StableDiffusionDepth2ImgPipeline"},
	{Value: "DiffusionPipeline", Label: "DiffusionPipeline"},
	{Value: "StableVideoDiffusionPipeline", Label: "StableVideoDiffusionPipeline"},
}

// UsecaseOptions must stay in sync with GetAllModelConfigUsecases in
// core/config/model_config.go — a value missing here is silently
// inaccessible from the model editor, which is how `score` (the router
// classifier usecase) hid for an entire release.
var UsecaseOptions = []FieldOption{
	{Value: "chat", Label: "Chat"},
	{Value: "completion", Label: "Completion"},
	{Value: "edit", Label: "Edit"},
	{Value: "embeddings", Label: "Embeddings"},
	{Value: "rerank", Label: "Rerank"},
	{Value: "score", Label: "Score (Router Classifier)"},
	{Value: "image", Label: "Image"},
	{Value: "vision", Label: "Vision"},
	{Value: "detection", Label: "Detection"},
	{Value: "depth", Label: "Depth"},
	{Value: "face_recognition", Label: "Face Recognition"},
	{Value: "transcript", Label: "Transcript"},
	{Value: "diarization", Label: "Diarization"},
	{Value: "sound_classification", Label: "Sound Classification"},
	{Value: "speaker_recognition", Label: "Speaker Recognition"},
	{Value: "tts", Label: "TTS"},
	{Value: "sound_generation", Label: "Sound Generation"},
	{Value: "audio_transform", Label: "Audio Transform"},
	{Value: "realtime_audio", Label: "Realtime Audio"},
	{Value: "tokenize", Label: "Tokenize"},
	{Value: "vad", Label: "VAD"},
	{Value: "video", Label: "Video"},
}

var DiffusersSchedulerOptions = []FieldOption{
	{Value: "ddim", Label: "DDIM"},
	{Value: "ddpm", Label: "DDPM"},
	{Value: "pndm", Label: "PNDM"},
	{Value: "lms", Label: "LMS"},
	{Value: "euler", Label: "Euler"},
	{Value: "euler_a", Label: "Euler A"},
	{Value: "dpm_multistep", Label: "DPM Multistep"},
	{Value: "dpm_singlestep", Label: "DPM Singlestep"},
	{Value: "heun", Label: "Heun"},
	{Value: "unipc", Label: "UniPC"},
}
