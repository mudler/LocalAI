package importers_test

import (
	"errors"
	"testing"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type metadataFixtures map[string]*hfapi.ModelDetails

func (f metadataFixtures) GetModelDetails(repo string) (*hfapi.ModelDetails, error) {
	details, ok := f[repo]
	if !ok {
		return nil, errors.New("metadata fixture not declared: " + repo)
	}
	return details, nil
}

func file(repo, path, sha string) hfapi.ModelFile {
	return hfapi.ModelFile{Path: path, SHA256: sha, URL: "https://huggingface.co/" + repo + "/resolve/main/" + path} // test-network: fixture
}

var fixtures = metadataFixtures{
	"mudler/vibevoice.cpp-models": {
		ModelID: "mudler/vibevoice.cpp-models", Author: "mudler",
		Files: []hfapi.ModelFile{
			file("mudler/vibevoice.cpp-models", "vibevoice-realtime-Q4_K_M.gguf", "01"),
			file("mudler/vibevoice.cpp-models", "vibevoice-asr-Q4_K_M.gguf", "02"),
			file("mudler/vibevoice.cpp-models", "tokenizer.gguf", "03"),
			file("mudler/vibevoice.cpp-models", "voice-Alice.gguf", "04"),
		},
	},
	"UsefulSensors/moonshine-tiny":                            {ModelID: "UsefulSensors/moonshine-tiny", Author: "UsefulSensors", PipelineTag: "automatic-speech-recognition", Files: []hfapi.ModelFile{file("UsefulSensors/moonshine-tiny", "model.onnx", "05")}},
	"nvidia/parakeet-tdt-0.6b-v3":                             {ModelID: "nvidia/parakeet-tdt-0.6b-v3", Author: "nvidia", PipelineTag: "automatic-speech-recognition", Files: []hfapi.ModelFile{file("nvidia/parakeet-tdt-0.6b-v3", "parakeet.nemo", "06")}},
	"LiquidAI/LFM2.5-Audio-1.5B":                              {ModelID: "LiquidAI/LFM2.5-Audio-1.5B", Author: "LiquidAI"},
	"LiquidAI/LFM2-Audio-1.5B":                                {ModelID: "LiquidAI/LFM2-Audio-1.5B", Author: "LiquidAI"},
	"LiquidAI/LFM2.5-Audio-1.5B-GGUF":                         {ModelID: "LiquidAI/LFM2.5-Audio-1.5B-GGUF", Author: "LiquidAI", Files: []hfapi.ModelFile{file("LiquidAI/LFM2.5-Audio-1.5B-GGUF", "LFM2.5-Audio-Q4_K_M.gguf", "07")}},
	"hexgrad/Kokoro-82M":                                      {ModelID: "hexgrad/Kokoro-82M", Author: "hexgrad", PipelineTag: "text-to-speech", Files: []hfapi.ModelFile{file("hexgrad/Kokoro-82M", "kokoro-v1_0.pth", "08")}},
	"Qwen/Qwen3-ASR-1.7B":                                     {ModelID: "Qwen/Qwen3-ASR-1.7B", Author: "Qwen", PipelineTag: "automatic-speech-recognition"},
	"HirCoir/piper-voice-es-mx-lucas-melor":                   {ModelID: "HirCoir/piper-voice-es-mx-lucas-melor", Author: "HirCoir", PipelineTag: "text-to-speech", Files: []hfapi.ModelFile{file("HirCoir/piper-voice-es-mx-lucas-melor", "es_MX-lucas-medium.onnx", "09"), file("HirCoir/piper-voice-es-mx-lucas-melor", "es_MX-lucas-medium.onnx.json", "10")}},
	"h94/IP-Adapter-FaceID":                                   {ModelID: "h94/IP-Adapter-FaceID", Author: "h94", PipelineTag: "text-to-image"},
	"LocalAI-io/whisper-large-v3-it-yodas-only-ggml":          {ModelID: "LocalAI-io/whisper-large-v3-it-yodas-only-ggml", Author: "LocalAI-io", PipelineTag: "automatic-speech-recognition", Files: []hfapi.ModelFile{file("LocalAI-io/whisper-large-v3-it-yodas-only-ggml", "ggml-model-q4_0.bin", "11"), file("LocalAI-io/whisper-large-v3-it-yodas-only-ggml", "ggml-model-q5_0.bin", "12"), file("LocalAI-io/whisper-large-v3-it-yodas-only-ggml", "ggml-model-q8_0.bin", "13")}},
	"Systran/faster-whisper-large-v3":                         {ModelID: "Systran/faster-whisper-large-v3", Author: "Systran", PipelineTag: "automatic-speech-recognition", Files: []hfapi.ModelFile{file("Systran/faster-whisper-large-v3", "model.bin", "14"), file("Systran/faster-whisper-large-v3", "config.json", "15")}},
	"nari-labs/Dia-1.6B":                                      {ModelID: "nari-labs/Dia-1.6B", Author: "nari-labs", PipelineTag: "text-to-speech"},
	"mudler/rfdetr-cpp-nano":                                  {ModelID: "mudler/rfdetr-cpp-nano", Author: "mudler", PipelineTag: "object-detection", Files: []hfapi.ModelFile{file("mudler/rfdetr-cpp-nano", "rfdetr-nano-Q4_K_M.gguf", "16")}},
	"Qdrant/bm25":                                             {ModelID: "Qdrant/bm25", Author: "Qdrant", PipelineTag: "sentence-similarity"},
	"pyannote/voice-activity-detection":                       {ModelID: "pyannote/voice-activity-detection", Author: "pyannote", PipelineTag: "automatic-speech-recognition"},
	"mudler/LocalAI-functioncall-qwen2.5-7b-v0.5-Q4_K_M-GGUF": {ModelID: "mudler/LocalAI-functioncall-qwen2.5-7b-v0.5-Q4_K_M-GGUF", Author: "mudler", Files: []hfapi.ModelFile{file("mudler/LocalAI-functioncall-qwen2.5-7b-v0.5-Q4_K_M-GGUF", "localai-functioncall-qwen2.5-7b-v0.5-q4_k_m.gguf", "4e7b7fe1d54b881f1ef90799219dc6cc285d29db24f559c8998d1addb35713d4")}},
	"Qwen/Qwen3-VL-2B-Instruct-GGUF":                          {ModelID: "Qwen/Qwen3-VL-2B-Instruct-GGUF", Author: "Qwen", Files: []hfapi.ModelFile{file("Qwen/Qwen3-VL-2B-Instruct-GGUF", "Qwen3VL-2B-Instruct-Q4_K_M.gguf", "17"), file("Qwen/Qwen3-VL-2B-Instruct-GGUF", "Qwen3VL-2B-Instruct-Q8_0.gguf", "18"), file("Qwen/Qwen3-VL-2B-Instruct-GGUF", "mmproj-Qwen3VL-2B-Instruct-F16.gguf", "20"), file("Qwen/Qwen3-VL-2B-Instruct-GGUF", "mmproj-Qwen3VL-2B-Instruct-Q8_0.gguf", "19")}},
}

func TestImporters(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Importers test suite")
}

var _ = BeforeSuite(func() {
	restore := importers.SetHuggingFaceMetadataFactoryForTest(func() importers.HuggingFaceMetadata { return fixtures })
	DeferCleanup(restore)
})
