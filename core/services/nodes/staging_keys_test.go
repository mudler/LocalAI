package nodes_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/nodes"
)

var _ = Describe("StagingKeyMapper", func() {
	Describe("Key", func() {
		It("namespaces under tracking key with subdirectory structure", func() {
			m := nodes.StagingKeyMapper{
				TrackingKey:       "my-model",
				FrontendModelsDir: "/frontend/models",
			}
			key := m.Key("/frontend/models/sd-cpp/models/flux.gguf")
			Expect(key).To(Equal("models/my-model/sd-cpp/models/flux.gguf"))
		})

		It("preserves different subdirectories for multi-file models", func() {
			m := nodes.StagingKeyMapper{
				TrackingKey:       "my-model",
				FrontendModelsDir: "/frontend/models",
			}
			modelKey := m.Key("/frontend/models/sd-cpp/models/flux.gguf")
			clipKey := m.Key("/frontend/models/sd-cpp/clip/clip.safetensors")
			vaeKey := m.Key("/frontend/models/sd-cpp/data/vae.safetensors")

			Expect(modelKey).To(Equal("models/my-model/sd-cpp/models/flux.gguf"))
			Expect(clipKey).To(Equal("models/my-model/sd-cpp/clip/clip.safetensors"))
			Expect(vaeKey).To(Equal("models/my-model/sd-cpp/data/vae.safetensors"))
		})

		It("falls back to basename for files outside models dir", func() {
			m := nodes.StagingKeyMapper{
				TrackingKey:       "my-model",
				FrontendModelsDir: "/frontend/models",
			}
			key := m.Key("/tmp/external.bin")
			Expect(key).To(Equal("models/my-model/external.bin"))
		})

		It("falls back to basename when FrontendModelsDir is empty", func() {
			m := nodes.StagingKeyMapper{
				TrackingKey:       "my-model",
				FrontendModelsDir: "",
			}
			key := m.Key("/any/path/model.gguf")
			Expect(key).To(Equal("models/my-model/model.gguf"))
		})

		It("handles single-file model at root of models dir", func() {
			m := nodes.StagingKeyMapper{
				TrackingKey:       "llama-3",
				FrontendModelsDir: "/models",
			}
			key := m.Key("/models/llama-3-8b.gguf")
			Expect(key).To(Equal("models/llama-3/llama-3-8b.gguf"))
		})

		It("does not allow path traversal via ..", func() {
			m := nodes.StagingKeyMapper{
				TrackingKey:       "my-model",
				FrontendModelsDir: "/frontend/models",
			}
			// Path that tries to escape — should fall back to basename
			key := m.Key("/frontend/models/../../../etc/passwd")
			// filepath.Rel resolves ".." → result starts with ".." → rejected → fallback to basename
			Expect(key).To(Equal("models/my-model/passwd"))
		})
	})

	Describe("DeriveRemoteModelPath", func() {
		It("derives ModelPath from remote path and model", func() {
			result := nodes.DeriveRemoteModelPath(
				"/worker/models/my-model/sd-cpp/models/flux.gguf",
				"sd-cpp/models/flux.gguf",
			)
			Expect(result).To(Equal("/worker/models/my-model"))
		})

		It("works with simple model name", func() {
			result := nodes.DeriveRemoteModelPath(
				"/worker/models/llama-3/model.gguf",
				"model.gguf",
			)
			Expect(result).To(Equal("/worker/models/llama-3"))
		})
	})

	Describe("Key + DeriveRemoteModelPath round-trip", func() {
		It("ModelPath + Model resolves back to the staged path", func() {
			m := nodes.StagingKeyMapper{
				TrackingKey:       "my-model",
				FrontendModelsDir: "/frontend/models",
			}

			model := "sd-cpp/models/flux.gguf"
			key := m.Key("/frontend/models/" + model)
			// Simulate what the worker does: strip "models/" prefix, prepend ModelsPath
			workerModelsPath := "/worker/models"
			relPath := key[len("models/"):] // strip ModelKeyPrefix
			remotePath := filepath.Join(workerModelsPath, relPath)

			derivedModelPath := nodes.DeriveRemoteModelPath(remotePath, model)

			// ModelPath + Model should equal the remote path
			Expect(filepath.Join(derivedModelPath, model)).To(Equal(remotePath))
		})

		It("generic option files resolve correctly via ModelPath", func() {
			m := nodes.StagingKeyMapper{
				TrackingKey:       "my-model",
				FrontendModelsDir: "/frontend/models",
			}

			// Stage main model file
			model := "sd-cpp/models/flux.gguf"
			modelKey := m.Key("/frontend/models/" + model)

			// Stage a generic option file (vae_path: sd-cpp/data/vae.safetensors)
			optionRelPath := "sd-cpp/data/vae.safetensors"
			optionKey := m.Key("/frontend/models/" + optionRelPath)

			workerModelsPath := "/worker/models"
			remoteModelPath := filepath.Join(workerModelsPath, modelKey[len("models/"):])
			remoteOptionPath := filepath.Join(workerModelsPath, optionKey[len("models/"):])

			derivedModelPath := nodes.DeriveRemoteModelPath(remoteModelPath, model)

			// The backend resolves option paths as ModelPath + optionRelPath
			resolvedOptionPath := filepath.Join(derivedModelPath, optionRelPath)
			Expect(resolvedOptionPath).To(Equal(remoteOptionPath))
		})
	})
})
