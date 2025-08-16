package model_test

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ModelLoader", func() {
	var (
		modelLoader *model.ModelLoader
		modelPath   string
		mockModel   *model.Model
	)

	BeforeEach(func() {
		// Setup the model loader with a test directory
		modelPath = "/tmp/test_model_path"
		os.Mkdir(modelPath, 0755)

		systemState, err := system.GetSystemState(
			system.WithModelPath(modelPath),
		)
		Expect(err).ToNot(HaveOccurred())
		modelLoader = model.NewModelLoader(systemState, false)
	})

	AfterEach(func() {
		// Cleanup test directory
		os.RemoveAll(modelPath)
	})

	Context("NewModelLoader", func() {
		It("should create a new ModelLoader with an empty model map", func() {
			Expect(modelLoader).ToNot(BeNil())
			Expect(modelLoader.ModelPath).To(Equal(modelPath))
			Expect(modelLoader.ListLoadedModels()).To(BeEmpty())
		})
	})

	Context("ExistsInModelPath", func() {
		It("should return true if a file exists in the model path", func() {
			testFile := filepath.Join(modelPath, "test.model")
			os.Create(testFile)
			Expect(modelLoader.ExistsInModelPath("test.model")).To(BeTrue())
		})

		It("should return false if a file does not exist in the model path", func() {
			Expect(modelLoader.ExistsInModelPath("nonexistent.model")).To(BeFalse())
		})
	})

	Context("ListFilesInModelPath", func() {
		It("should list all valid model files in the model path", func() {
			os.Create(filepath.Join(modelPath, "test.model"))
			os.Create(filepath.Join(modelPath, "README.md"))

			files, err := modelLoader.ListFilesInModelPath()
			Expect(err).To(BeNil())
			Expect(files).To(ContainElement("test.model"))
			Expect(files).ToNot(ContainElement("README.md"))
		})
	})

	Context("LoadModel", func() {
		It("should load a model and keep it in memory", func() {
			mockModel = model.NewModel("foo", "test.model", nil)

			mockLoader := func(modelID, modelName, modelFile string) (*model.Model, error) {
				return mockModel, nil
			}

			model, err := modelLoader.LoadModel("foo", "test.model", mockLoader)
			Expect(err).To(BeNil())
			Expect(model).To(Equal(mockModel))
			Expect(modelLoader.CheckIsLoaded("foo")).To(Equal(mockModel))
		})

		It("should return an error if loading the model fails", func() {
			mockLoader := func(modelID, modelName, modelFile string) (*model.Model, error) {
				return nil, errors.New("failed to load model")
			}

			model, err := modelLoader.LoadModel("foo", "test.model", mockLoader)
			Expect(err).To(HaveOccurred())
			Expect(model).To(BeNil())
		})
	})

	Context("ShutdownModel", func() {
		It("should shutdown a loaded model", func() {
			mockLoader := func(modelID, modelName, modelFile string) (*model.Model, error) {
				return model.NewModel("foo", "test.model", nil), nil
			}

			_, err := modelLoader.LoadModel("foo", "test.model", mockLoader)
			Expect(err).To(BeNil())

			err = modelLoader.ShutdownModel("foo")
			Expect(err).To(BeNil())
			Expect(modelLoader.CheckIsLoaded("foo")).To(BeNil())
		})
	})
})
