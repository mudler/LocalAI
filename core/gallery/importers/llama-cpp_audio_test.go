package importers

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ggufMetadata builds a GGUF v3 header with no tensors and the given
// key/value metadata, so the importer's probe can read the mmproj flags over
// a local HTTP server without a real model download.
func ggufMetadata(kvs map[string]any) []byte {
	var buf bytes.Buffer
	writeString := func(s string) {
		_ = binary.Write(&buf, binary.LittleEndian, uint64(len(s)))
		buf.WriteString(s)
	}
	buf.WriteString("GGUF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(3))
	_ = binary.Write(&buf, binary.LittleEndian, uint64(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint64(len(kvs)))
	for key, value := range kvs {
		writeString(key)
		switch v := value.(type) {
		case string:
			_ = binary.Write(&buf, binary.LittleEndian, uint32(8))
			writeString(v)
		case bool:
			_ = binary.Write(&buf, binary.LittleEndian, uint32(7))
			var b byte
			if v {
				b = 1
			}
			buf.WriteByte(b)
		}
	}
	return buf.Bytes()
}

var _ = Describe("maybeApplyTTSUsecase on an mmproj probe", func() {
	// serveMMProj serves the synthetic header and returns the URL the import
	// would fetch its mmproj from.
	serveMMProj := func(kvs map[string]any) string {
		// The parser refuses a projector that declares no type, as llama.cpp
		// does, so every fixture carries one.
		kvs["clip.projector_type"] = "mlp"
		payload := ggufMetadata(kvs)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.ServeContent(w, r, "mmproj.gguf", time.Time{}, bytes.NewReader(payload))
		}))
		DeferCleanup(server.Close)
		return server.URL + "/mmproj.gguf"
	}

	// importedConfig is the chat config the llama-cpp importer starts from,
	// with the mmproj the import selected.
	importedConfig := func(mmprojURL string) (*config.ModelConfig, *gallery.ModelConfig) {
		modelConfig := &config.ModelConfig{
			Name:                "lfm3-audio-2b",
			KnownUsecaseStrings: []string{config.UsecaseChat},
			Options:             []string{"use_jinja:true"},
			LLMConfig:           config.LLMConfig{MMProj: "mmproj-LFM3-Audio-2B-Q8_0.gguf"},
		}
		cfg := &gallery.ModelConfig{
			Files: []gallery.File{{Filename: "mmproj-LFM3-Audio-2B-Q8_0.gguf", URI: mmprojURL}},
		}
		return modelConfig, cfg
	}

	It("imports an LFM3 speech-to-speech model with audio encoder and gen-audio decoder", func() {
		url := serveMMProj(map[string]any{
			"general.architecture":       "clip",
			"clip.has_audio_encoder":     true,
			"clip.has_gen_audio_encoder": true,
			"clip.audio.projector_type":  "lfm3a",
		})
		modelConfig, cfg := importedConfig(url)

		maybeApplyTTSUsecase(modelConfig, cfg)

		Expect(modelConfig.KnownUsecaseStrings).To(Equal([]string{
			config.UsecaseRealtimeAudio, config.UsecaseChat, config.UsecaseTranscript, config.UsecaseTTS,
		}))
		Expect(modelConfig.Pipeline.AnyToAny).NotTo(BeNil())
		Expect(*modelConfig.Pipeline.AnyToAny).To(BeTrue())
		Expect(modelConfig.Options).To(ContainElements("use_jinja:true", "parallel:2"))
		Expect(modelConfig.TemplateConfig.UseTokenizerTemplate).To(BeTrue())
	})

	It("keeps a Qwen3-TTS projector, which also sets both encoder flags, as TTS", func() {
		url := serveMMProj(map[string]any{
			"general.architecture":       "clip",
			"clip.has_audio_encoder":     true,
			"clip.has_gen_audio_encoder": true,
			"clip.audio.projector_type":  "qwen3tts_spkenc",
		})
		modelConfig, cfg := importedConfig(url)

		maybeApplyTTSUsecase(modelConfig, cfg)

		Expect(modelConfig.KnownUsecaseStrings).To(Equal([]string{config.UsecaseTTS}))
		Expect(modelConfig.Pipeline.AnyToAny).To(BeNil())
	})

	It("keeps the TTS-only result for a gen-audio projector without an audio encoder", func() {
		url := serveMMProj(map[string]any{
			"general.architecture":       "clip",
			"clip.has_gen_audio_encoder": true,
		})
		modelConfig, cfg := importedConfig(url)

		maybeApplyTTSUsecase(modelConfig, cfg)

		Expect(modelConfig.KnownUsecaseStrings).To(Equal([]string{config.UsecaseTTS}))
		Expect(modelConfig.Pipeline.AnyToAny).To(BeNil())
		Expect(modelConfig.Options).To(Equal([]string{"use_jinja:true"}))
	})

	It("leaves a vision model with an audio encoder as a chat model", func() {
		url := serveMMProj(map[string]any{
			"general.architecture":   "clip",
			"clip.has_audio_encoder": true,
		})
		modelConfig, cfg := importedConfig(url)

		maybeApplyTTSUsecase(modelConfig, cfg)

		Expect(modelConfig.KnownUsecaseStrings).To(Equal([]string{config.UsecaseChat}))
		Expect(modelConfig.Pipeline.AnyToAny).To(BeNil())
	})
})
