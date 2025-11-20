package gallery_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

const bertEmbeddingsURL = `https://gist.githubusercontent.com/mudler/0a080b166b87640e8644b09c2aee6e3b/raw/f0e8c26bb72edc16d9fbafbfd6638072126ff225/bert-embeddings-gallery.yaml`

var _ = Describe("Model test", func() {

	BeforeEach(func() {
		if os.Getenv("FIXTURES") == "" {
			Skip("FIXTURES env var not set, skipping model tests")
		}
	})

	Context("Downloading", func() {
		It("applies model correctly", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile[ModelConfig](filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())
			systemState, err := system.GetSystemState(
				system.WithModelPath(tempdir),
			)
			Expect(err).ToNot(HaveOccurred())
			_, err = InstallModel(context.TODO(), systemState, "", c, map[string]interface{}{}, func(string, string, string, float64) {}, true)
			Expect(err).ToNot(HaveOccurred())

			for _, f := range []string{"cerebras", "cerebras-completion.tmpl", "cerebras-chat.tmpl", "cerebras.yaml"} {
				_, err = os.Stat(filepath.Join(tempdir, f))
				Expect(err).ToNot(HaveOccurred())
			}

			content := map[string]interface{}{}

			dat, err := os.ReadFile(filepath.Join(tempdir, "cerebras.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = yaml.Unmarshal(dat, content)
			Expect(err).ToNot(HaveOccurred())

			Expect(content["context_size"]).To(Equal(1024))
		})

		It("applies model from gallery correctly", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)

			gallery := []GalleryModel{{
				Metadata: Metadata{
					Name: "bert",
					URL:  bertEmbeddingsURL,
				},
			}}
			out, err := yaml.Marshal(gallery)
			Expect(err).ToNot(HaveOccurred())
			galleryFilePath := filepath.Join(tempdir, "gallery_simple.yaml")
			err = os.WriteFile(galleryFilePath, out, 0600)
			Expect(err).ToNot(HaveOccurred())
			Expect(filepath.IsAbs(galleryFilePath)).To(BeTrue(), galleryFilePath)
			galleries := []config.Gallery{
				{
					Name: "test",
					URL:  "file://" + galleryFilePath,
				},
			}
			systemState, err := system.GetSystemState(
				system.WithModelPath(tempdir),
			)
			Expect(err).ToNot(HaveOccurred())

			models, err := AvailableGalleryModels(galleries, systemState)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models)).To(Equal(1))
			Expect(models[0].Name).To(Equal("bert"))
			Expect(models[0].URL).To(Equal(bertEmbeddingsURL))
			Expect(models[0].Installed).To(BeFalse())

			err = InstallModelFromGallery(context.TODO(), galleries, []config.Gallery{}, systemState, nil, "test@bert", GalleryModel{}, func(s1, s2, s3 string, f float64) {}, true, true)
			Expect(err).ToNot(HaveOccurred())

			dat, err := os.ReadFile(filepath.Join(tempdir, "bert.yaml"))
			Expect(err).ToNot(HaveOccurred())

			content := map[string]interface{}{}
			err = yaml.Unmarshal(dat, &content)
			Expect(err).ToNot(HaveOccurred())
			Expect(content["usage"]).To(ContainSubstring("You can test this model with curl like this"))

			models, err = AvailableGalleryModels(galleries, systemState)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models)).To(Equal(1))
			Expect(models[0].Installed).To(BeTrue())

			// delete
			err = DeleteModelFromSystem(systemState, "bert")
			Expect(err).ToNot(HaveOccurred())

			models, err = AvailableGalleryModels(galleries, systemState)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models)).To(Equal(1))
			Expect(models[0].Installed).To(BeFalse())

			_, err = os.Stat(filepath.Join(tempdir, "bert.yaml"))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
		})

		It("renames model correctly", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile[ModelConfig](filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			systemState, err := system.GetSystemState(
				system.WithModelPath(tempdir),
			)
			Expect(err).ToNot(HaveOccurred())
			_, err = InstallModel(context.TODO(), systemState, "foo", c, map[string]interface{}{}, func(string, string, string, float64) {}, true)
			Expect(err).ToNot(HaveOccurred())

			for _, f := range []string{"cerebras", "cerebras-completion.tmpl", "cerebras-chat.tmpl", "foo.yaml"} {
				_, err = os.Stat(filepath.Join(tempdir, f))
				Expect(err).ToNot(HaveOccurred())
			}
		})

		It("overrides parameters", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile[ModelConfig](filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			systemState, err := system.GetSystemState(
				system.WithModelPath(tempdir),
			)
			Expect(err).ToNot(HaveOccurred())
			_, err = InstallModel(context.TODO(), systemState, "foo", c, map[string]interface{}{"backend": "foo"}, func(string, string, string, float64) {}, true)
			Expect(err).ToNot(HaveOccurred())

			for _, f := range []string{"cerebras", "cerebras-completion.tmpl", "cerebras-chat.tmpl", "foo.yaml"} {
				_, err = os.Stat(filepath.Join(tempdir, f))
				Expect(err).ToNot(HaveOccurred())
			}

			content := map[string]interface{}{}

			dat, err := os.ReadFile(filepath.Join(tempdir, "foo.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = yaml.Unmarshal(dat, content)
			Expect(err).ToNot(HaveOccurred())

			Expect(content["backend"]).To(Equal("foo"))
		})

		It("catches path traversals", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile[ModelConfig](filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			systemState, err := system.GetSystemState(
				system.WithModelPath(tempdir),
			)
			Expect(err).ToNot(HaveOccurred())
			_, err = InstallModel(context.TODO(), systemState, "../../../foo", c, map[string]interface{}{}, func(string, string, string, float64) {}, true)
			Expect(err).To(HaveOccurred())
		})

		It("does not delete shared model files when one config is deleted", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)

			systemState, err := system.GetSystemState(
				system.WithModelPath(tempdir),
			)
			Expect(err).ToNot(HaveOccurred())

			// Create a shared model file
			sharedModelFile := filepath.Join(tempdir, "shared_model.bin")
			err = os.WriteFile(sharedModelFile, []byte("fake model content"), 0600)
			Expect(err).ToNot(HaveOccurred())

			// Create first model configuration
			config1 := `name: model1
model: shared_model.bin`
			err = os.WriteFile(filepath.Join(tempdir, "model1.yaml"), []byte(config1), 0600)
			Expect(err).ToNot(HaveOccurred())

			// Create first model's gallery file
			galleryConfig1 := ModelConfig{
				Name: "model1",
				Files: []File{
					{Filename: "shared_model.bin"},
				},
			}
			galleryData1, err := yaml.Marshal(galleryConfig1)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(filepath.Join(tempdir, "._gallery_model1.yaml"), galleryData1, 0600)
			Expect(err).ToNot(HaveOccurred())

			// Create second model configuration sharing the same model file
			config2 := `name: model2
model: shared_model.bin`
			err = os.WriteFile(filepath.Join(tempdir, "model2.yaml"), []byte(config2), 0600)
			Expect(err).ToNot(HaveOccurred())

			// Create second model's gallery file
			galleryConfig2 := ModelConfig{
				Name: "model2",
				Files: []File{
					{Filename: "shared_model.bin"},
				},
			}
			galleryData2, err := yaml.Marshal(galleryConfig2)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(filepath.Join(tempdir, "._gallery_model2.yaml"), galleryData2, 0600)
			Expect(err).ToNot(HaveOccurred())

			// Verify both configurations exist
			_, err = os.Stat(filepath.Join(tempdir, "model1.yaml"))
			Expect(err).ToNot(HaveOccurred())
			_, err = os.Stat(filepath.Join(tempdir, "model2.yaml"))
			Expect(err).ToNot(HaveOccurred())

			// Verify the shared model file exists
			_, err = os.Stat(sharedModelFile)
			Expect(err).ToNot(HaveOccurred())

			// Delete the first model
			err = DeleteModelFromSystem(systemState, "model1")
			Expect(err).ToNot(HaveOccurred())

			// Verify the first configuration is deleted
			_, err = os.Stat(filepath.Join(tempdir, "model1.yaml"))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())

			// Verify the shared model file still exists (not deleted because model2 still uses it)
			_, err = os.Stat(sharedModelFile)
			Expect(err).ToNot(HaveOccurred(), "shared model file should not be deleted when used by other configs")

			// Verify the second configuration still exists
			_, err = os.Stat(filepath.Join(tempdir, "model2.yaml"))
			Expect(err).ToNot(HaveOccurred())

			// Now delete the second model
			err = DeleteModelFromSystem(systemState, "model2")
			Expect(err).ToNot(HaveOccurred())

			// Verify the second configuration is deleted
			_, err = os.Stat(filepath.Join(tempdir, "model2.yaml"))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())

			// Verify the shared model file is now deleted (no more references)
			_, err = os.Stat(sharedModelFile)
			Expect(err).To(HaveOccurred(), "shared model file should be deleted when no configs reference it")
			Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
		})
	})
})
