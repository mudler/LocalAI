package config

import (
	"io"
	"net/http"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Test cases for config related functions", func() {
	Context("Test Read configuration functions", func() {
		It("Test Validate", func() {
			tmp, err := os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = tmp.WriteString(
				`backend: "../foo-bar"
name: "foo"
parameters:
  model: "foo-bar"
known_usecases:
- chat
- COMPLETION
`)
			Expect(err).ToNot(HaveOccurred())
			config, err := readBackendConfigFromFile(tmp.Name())
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			Expect(config.Validate()).To(BeFalse())
			Expect(config.KnownUsecases).ToNot(BeNil())
		})
		It("Test Validate", func() {
			tmp, err := os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = tmp.WriteString(
				`name: bar-baz
backend: "foo-bar"
parameters:
  model: "foo-bar"`)
			Expect(err).ToNot(HaveOccurred())
			config, err := readBackendConfigFromFile(tmp.Name())
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config.Name).To(Equal("bar-baz"))
			Expect(config.Validate()).To(BeTrue())

			// download https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/models/hermes-2-pro-mistral.yaml
			httpClient := http.Client{}
			resp, err := httpClient.Get("https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/models/hermes-2-pro-mistral.yaml")
			Expect(err).To(BeNil())
			defer resp.Body.Close()
			tmp, err = os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = io.Copy(tmp, resp.Body)
			Expect(err).To(BeNil())
			config, err = readBackendConfigFromFile(tmp.Name())
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config.Name).To(Equal("hermes-2-pro-mistral"))
			Expect(config.Validate()).To(BeTrue())
		})
	})
	It("Properly handles backend usecase matching", func() {

		a := BackendConfig{
			Name: "a",
		}
		Expect(a.HasUsecases(FLAG_ANY)).To(BeTrue()) // FLAG_ANY just means the config _exists_ essentially.

		b := BackendConfig{
			Name:    "b",
			Backend: "stablediffusion",
		}
		Expect(b.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(b.HasUsecases(FLAG_IMAGE)).To(BeTrue())
		Expect(b.HasUsecases(FLAG_CHAT)).To(BeFalse())

		c := BackendConfig{
			Name:    "c",
			Backend: "llama-cpp",
			TemplateConfig: TemplateConfig{
				Chat: "chat",
			},
		}
		Expect(c.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(c.HasUsecases(FLAG_IMAGE)).To(BeFalse())
		Expect(c.HasUsecases(FLAG_COMPLETION)).To(BeFalse())
		Expect(c.HasUsecases(FLAG_CHAT)).To(BeTrue())

		d := BackendConfig{
			Name:    "d",
			Backend: "llama-cpp",
			TemplateConfig: TemplateConfig{
				Chat:       "chat",
				Completion: "completion",
			},
		}
		Expect(d.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(d.HasUsecases(FLAG_IMAGE)).To(BeFalse())
		Expect(d.HasUsecases(FLAG_COMPLETION)).To(BeTrue())
		Expect(d.HasUsecases(FLAG_CHAT)).To(BeTrue())

		trueValue := true
		e := BackendConfig{
			Name:    "e",
			Backend: "llama-cpp",
			TemplateConfig: TemplateConfig{
				Completion: "completion",
			},
			Embeddings: &trueValue,
		}

		Expect(e.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(e.HasUsecases(FLAG_IMAGE)).To(BeFalse())
		Expect(e.HasUsecases(FLAG_COMPLETION)).To(BeTrue())
		Expect(e.HasUsecases(FLAG_CHAT)).To(BeFalse())
		Expect(e.HasUsecases(FLAG_EMBEDDINGS)).To(BeTrue())

		f := BackendConfig{
			Name:    "f",
			Backend: "piper",
		}
		Expect(f.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(f.HasUsecases(FLAG_TTS)).To(BeTrue())
		Expect(f.HasUsecases(FLAG_CHAT)).To(BeFalse())

		g := BackendConfig{
			Name:    "g",
			Backend: "whisper",
		}
		Expect(g.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(g.HasUsecases(FLAG_TRANSCRIPT)).To(BeTrue())
		Expect(g.HasUsecases(FLAG_TTS)).To(BeFalse())

		h := BackendConfig{
			Name:    "h",
			Backend: "transformers-musicgen",
		}
		Expect(h.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(h.HasUsecases(FLAG_TRANSCRIPT)).To(BeFalse())
		Expect(h.HasUsecases(FLAG_TTS)).To(BeTrue())
		Expect(h.HasUsecases(FLAG_SOUND_GENERATION)).To(BeTrue())

		knownUsecases := FLAG_CHAT | FLAG_COMPLETION
		i := BackendConfig{
			Name:    "i",
			Backend: "whisper",
			// Earlier test checks parsing, this just needs to set final values
			KnownUsecases: &knownUsecases,
		}
		Expect(i.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(i.HasUsecases(FLAG_TRANSCRIPT)).To(BeTrue())
		Expect(i.HasUsecases(FLAG_TTS)).To(BeFalse())
		Expect(i.HasUsecases(FLAG_COMPLETION)).To(BeTrue())
		Expect(i.HasUsecases(FLAG_CHAT)).To(BeTrue())

	})
})
