package meta

// Dynamic autocomplete provider constants (runtime lookup required).
const (
	ProviderBackends         = "backends"
	ProviderModels           = "models"
	ProviderModelsChat       = "models:chat"
	ProviderModelsTTS        = "models:tts"
	ProviderModelsTranscript = "models:transcript"
	ProviderModelsVAD        = "models:vad"
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
