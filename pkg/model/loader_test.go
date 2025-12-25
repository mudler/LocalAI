package model_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

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
		modelLoader = model.NewModelLoader(systemState)
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

	Context("Concurrent Loading", func() {
		It("should handle concurrent requests for the same model", func() {
			var loadCount int32
			mockLoader := func(modelID, modelName, modelFile string) (*model.Model, error) {
				atomic.AddInt32(&loadCount, 1)
				time.Sleep(100 * time.Millisecond) // Simulate loading time
				return model.NewModel(modelID, modelName, nil), nil
			}

			var wg sync.WaitGroup
			results := make([]*model.Model, 5)
			errs := make([]error, 5)

			// Start 5 concurrent requests for the same model
			for i := 0; i < 5; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					results[idx], errs[idx] = modelLoader.LoadModel("concurrent-model", "test.model", mockLoader)
				}(i)
			}

			wg.Wait()

			// All requests should succeed
			for i := 0; i < 5; i++ {
				Expect(errs[i]).To(BeNil())
				Expect(results[i]).ToNot(BeNil())
			}

			// The loader should only have been called once
			Expect(atomic.LoadInt32(&loadCount)).To(Equal(int32(1)))

			// All results should be the same model instance
			for i := 1; i < 5; i++ {
				Expect(results[i]).To(Equal(results[0]))
			}
		})

		It("should handle concurrent requests for different models", func() {
			var loadCount int32
			mockLoader := func(modelID, modelName, modelFile string) (*model.Model, error) {
				atomic.AddInt32(&loadCount, 1)
				time.Sleep(50 * time.Millisecond) // Simulate loading time
				return model.NewModel(modelID, modelName, nil), nil
			}

			var wg sync.WaitGroup
			modelCount := 3

			// Start concurrent requests for different models
			for i := 0; i < modelCount; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					modelID := "model-" + string(rune('A'+idx))
					_, err := modelLoader.LoadModel(modelID, "test.model", mockLoader)
					Expect(err).To(BeNil())
				}(i)
			}

			wg.Wait()

			// Each model should be loaded exactly once
			Expect(atomic.LoadInt32(&loadCount)).To(Equal(int32(modelCount)))

			// All models should be loaded
			Expect(modelLoader.CheckIsLoaded("model-A")).ToNot(BeNil())
			Expect(modelLoader.CheckIsLoaded("model-B")).ToNot(BeNil())
			Expect(modelLoader.CheckIsLoaded("model-C")).ToNot(BeNil())
		})

		It("should track loading count correctly", func() {
			loadStarted := make(chan struct{})
			loadComplete := make(chan struct{})

			mockLoader := func(modelID, modelName, modelFile string) (*model.Model, error) {
				close(loadStarted)
				<-loadComplete // Wait until we're told to complete
				return model.NewModel(modelID, modelName, nil), nil
			}

			// Start loading in background
			go func() {
				modelLoader.LoadModel("slow-model", "test.model", mockLoader)
			}()

			// Wait for loading to start
			<-loadStarted

			// Loading count should be 1
			Expect(modelLoader.GetLoadingCount()).To(Equal(1))

			// Complete the loading
			close(loadComplete)

			// Wait a bit for cleanup
			time.Sleep(50 * time.Millisecond)

			// Loading count should be back to 0
			Expect(modelLoader.GetLoadingCount()).To(Equal(0))
		})

		It("should retry loading if first attempt fails", func() {
			var attemptCount int32
			mockLoader := func(modelID, modelName, modelFile string) (*model.Model, error) {
				count := atomic.AddInt32(&attemptCount, 1)
				if count == 1 {
					return nil, errors.New("first attempt fails")
				}
				return model.NewModel(modelID, modelName, nil), nil
			}

			// First goroutine will fail
			var wg sync.WaitGroup
			wg.Add(2)

			var err1, err2 error
			var m1, m2 *model.Model

			go func() {
				defer wg.Done()
				m1, err1 = modelLoader.LoadModel("retry-model", "test.model", mockLoader)
			}()

			// Give first goroutine a head start
			time.Sleep(10 * time.Millisecond)

			go func() {
				defer wg.Done()
				m2, err2 = modelLoader.LoadModel("retry-model", "test.model", mockLoader)
			}()

			wg.Wait()

			// At least one should succeed (the second attempt after retry)
			successCount := 0
			if err1 == nil && m1 != nil {
				successCount++
			}
			if err2 == nil && m2 != nil {
				successCount++
			}
			Expect(successCount).To(BeNumerically(">=", 1))
		})
	})

	Context("GetLoadingCount", func() {
		It("should return 0 when nothing is loading", func() {
			Expect(modelLoader.GetLoadingCount()).To(Equal(0))
		})
	})

	Context("LRU Eviction Retry Settings", func() {
		It("should allow updating retry settings", func() {
			modelLoader.SetLRUEvictionRetrySettings(50, 2*time.Second)
			// Settings are updated - we can verify through behavior if needed
			// For now, just verify the call doesn't panic
			Expect(modelLoader).ToNot(BeNil())
		})
	})
})
