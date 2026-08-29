package localai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type endpointFailingMaterializer struct{}

func (*endpointFailingMaterializer) Ensure(context.Context, string, modelartifacts.Spec) (modelartifacts.Result, error) {
	return modelartifacts.Result{}, errors.New("artifact unavailable")
}

type endpointRecordingClient struct {
	published []messaging.CacheInvalidateEvent
}
type endpointSubscription struct{}

func (*endpointSubscription) Unsubscribe() error { return nil }
func (c *endpointRecordingClient) Publish(subject string, data any) error {
	if subject == messaging.SubjectCacheInvalidateModels {
		c.published = append(c.published, data.(messaging.CacheInvalidateEvent))
	}
	return nil
}
func (*endpointRecordingClient) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return &endpointSubscription{}, nil
}
func (*endpointRecordingClient) QueueSubscribe(string, string, func([]byte)) (messaging.Subscription, error) {
	return &endpointSubscription{}, nil
}
func (*endpointRecordingClient) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &endpointSubscription{}, nil
}
func (*endpointRecordingClient) SubscribeReply(string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &endpointSubscription{}, nil
}
func (*endpointRecordingClient) Request(string, []byte, time.Duration) ([]byte, error) {
	return nil, nil
}
func (*endpointRecordingClient) IsConnected() bool { return true }
func (*endpointRecordingClient) Close()            {}

// testRenderer is a simple renderer for tests that returns JSON
type testRenderer struct{}

func (t *testRenderer) Render(w io.Writer, name string, data any, c echo.Context) error {
	// For tests, just return the data as JSON
	return json.NewEncoder(w).Encode(data)
}

var _ = Describe("Edit Model test", func() {

	var tempDir string
	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "localai-test")
		Expect(err).ToNot(HaveOccurred())
	})
	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Context("Edit Model endpoint", func() {
		DescribeTable("reports the saved revision and pending cleanup count",
			func(pendingCleanup int) {
				systemState, err := system.GetSystemState(system.WithModelPath(tempDir))
				Expect(err).ToNot(HaveOccurred())
				applicationConfig := config.NewApplicationConfig(config.WithSystemState(systemState))
				loader := config.NewModelConfigLoader(tempDir)
				Expect(os.WriteFile(
					filepath.Join(tempDir, "model.yaml"),
					[]byte("name: model\nbackend: llama-cpp\ncontext_size: 4096\n"),
					0o644,
				)).To(Succeed())
				Expect(loader.LoadModelConfigsFromPath(tempDir)).To(Succeed())
				lifecycle := &endpointLifecycleRecorder{pendingCleanup: pendingCleanup}
				app := echo.New()
				app.POST("/models/edit/:name", EditModelEndpoint(loader, nil, applicationConfig, lifecycle))

				req := httptest.NewRequest(
					http.MethodPost,
					"/models/edit/model",
					bytes.NewBufferString("name: model\nbackend: llama-cpp\ncontext_size: 8192\n"),
				)
				rec := httptest.NewRecorder()
				app.ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
				var response map[string]any
				Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
				Expect(response).To(HaveKeyWithValue("config_revision", Not(BeEmpty())))
				Expect(response).To(HaveKeyWithValue("pending_cleanup", BeNumerically("==", pendingCleanup)))
			},
			Entry("when no replicas need cleanup", 0),
			Entry("when stale replicas remain queued for cleanup", 2),
		)

		It("does not broadcast an in-place edit that rolls back during preload", func() {
			systemState, err := system.GetSystemState(system.WithModelPath(tempDir))
			Expect(err).ToNot(HaveOccurred())
			applicationConfig := config.NewApplicationConfig(config.WithSystemState(systemState))
			loader := config.NewModelConfigLoader(tempDir, config.WithArtifactMaterializer(&endpointFailingMaterializer{}))
			path := filepath.Join(tempDir, "model.yaml")
			Expect(os.WriteFile(path, []byte("name: model\nbackend: llama-cpp\ncontext_size: 4096\n"), 0644)).To(Succeed())
			Expect(loader.LoadModelConfigsFromPath(tempDir)).To(Succeed())
			galleryService := galleryop.NewGalleryService(applicationConfig, nil)
			client := &endpointRecordingClient{}
			galleryService.SetNATSClient(client)

			app := echo.New()
			app.POST("/models/edit/:name", EditModelEndpoint(loader, galleryService, applicationConfig))
			body := "name: model\nbackend: llama-cpp\ncontext_size: 8192\nartifacts:\n  - name: model\n    target: model\n    source: {type: huggingface, repo: owner/repo}\n"
			req := httptest.NewRequest("POST", "/models/edit/model", bytes.NewBufferString(body))
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			Expect(client.published).To(BeEmpty())
			bodyOnDisk, err := os.ReadFile(path)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(bodyOnDisk)).To(ContainSubstring("context_size: 4096"))
		})

		It("does not broadcast an edit that rolls back during preload", func() {
			systemState, err := system.GetSystemState(system.WithModelPath(tempDir))
			Expect(err).ToNot(HaveOccurred())
			applicationConfig := config.NewApplicationConfig(config.WithSystemState(systemState))
			loader := config.NewModelConfigLoader(tempDir, config.WithArtifactMaterializer(&endpointFailingMaterializer{}))
			path := filepath.Join(tempDir, "old.yaml")
			Expect(os.WriteFile(path, []byte("name: old\nbackend: llama-cpp\ncontext_size: 4096\n"), 0644)).To(Succeed())
			Expect(loader.LoadModelConfigsFromPath(tempDir)).To(Succeed())
			galleryService := galleryop.NewGalleryService(applicationConfig, nil)
			client := &endpointRecordingClient{}
			galleryService.SetNATSClient(client)

			app := echo.New()
			app.POST("/models/edit/:name", EditModelEndpoint(loader, galleryService, applicationConfig))
			body := "name: new\nbackend: llama-cpp\ncontext_size: 8192\nartifacts:\n  - name: model\n    target: model\n    source: {type: huggingface, repo: owner/repo}\n"
			req := httptest.NewRequest("POST", "/models/edit/old", bytes.NewBufferString(body))
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			Expect(client.published).To(BeEmpty())
			Expect(path).To(BeAnExistingFile())
			Expect(filepath.Join(tempDir, "new.yaml")).NotTo(BeAnExistingFile())
		})

		It("should edit a model", func() {
			systemState, err := system.GetSystemState(
				system.WithModelPath(filepath.Join(tempDir)),
			)
			Expect(err).ToNot(HaveOccurred())

			applicationConfig := config.NewApplicationConfig(
				config.WithSystemState(systemState),
			)
			//modelLoader := model.NewModelLoader(systemState, true)
			modelConfigLoader := config.NewModelConfigLoader(systemState.Model.ModelsPath)

			// Define Echo app and register all routes upfront
			app := echo.New()
			// Set up a simple renderer for the test
			app.Renderer = &testRenderer{}
			app.POST("/import-model", ImportModelEndpoint(modelConfigLoader, nil, applicationConfig))
			app.GET("/edit-model/:name", GetEditModelPage(modelConfigLoader, applicationConfig))

			requestBody := bytes.NewBufferString(`{"name": "foo", "backend": "foo", "model": "foo"}`)

			req := httptest.NewRequest("POST", "/import-model", requestBody)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			body, err := io.ReadAll(rec.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("Model configuration created successfully"))
			Expect(rec.Code).To(Equal(http.StatusOK))

			req = httptest.NewRequest("GET", "/edit-model/foo", nil)
			rec = httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			body, err = io.ReadAll(rec.Body)
			Expect(err).ToNot(HaveOccurred())
			// The response contains the model configuration with backend field
			Expect(string(body)).To(ContainSubstring(`"backend":"foo"`))
			Expect(string(body)).To(ContainSubstring(`"name":"foo"`))
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("renames the config file on disk when the YAML name changes", func() {
			systemState, err := system.GetSystemState(
				system.WithModelPath(tempDir),
			)
			Expect(err).ToNot(HaveOccurred())
			applicationConfig := config.NewApplicationConfig(
				config.WithSystemState(systemState),
			)
			modelConfigLoader := config.NewModelConfigLoader(systemState.Model.ModelsPath)

			oldYAML := "name: oldname\nbackend: llama\nmodel: foo\n"
			oldPath := filepath.Join(tempDir, "oldname.yaml")
			Expect(os.WriteFile(oldPath, []byte(oldYAML), 0644)).To(Succeed())
			// Drop a gallery metadata file so we can check it is renamed too.
			galleryOldPath := filepath.Join(tempDir, gallery.GalleryFileName("oldname"))
			Expect(os.WriteFile(galleryOldPath, []byte("name: oldname\n"), 0644)).To(Succeed())

			Expect(modelConfigLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())
			_, exists := modelConfigLoader.GetModelConfig("oldname")
			Expect(exists).To(BeTrue())

			app := echo.New()
			app.POST("/models/edit/:name", EditModelEndpoint(modelConfigLoader, nil, applicationConfig))

			newYAML := "name: newname\nbackend: llama\nmodel: foo\n"
			req := httptest.NewRequest("POST", "/models/edit/oldname", bytes.NewBufferString(newYAML))
			req.Header.Set("Content-Type", "application/x-yaml")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			body, err := io.ReadAll(rec.Body)
			Expect(err).ToNot(HaveOccurred(), string(body))
			Expect(rec.Code).To(Equal(http.StatusOK), string(body))

			// Old file is gone, new file exists.
			_, err = os.Stat(oldPath)
			Expect(os.IsNotExist(err)).To(BeTrue(), "old config file should be removed")
			newPath := filepath.Join(tempDir, "newname.yaml")
			_, err = os.Stat(newPath)
			Expect(err).ToNot(HaveOccurred(), "new config file should exist")

			// Gallery metadata followed the rename.
			_, err = os.Stat(galleryOldPath)
			Expect(os.IsNotExist(err)).To(BeTrue(), "old gallery metadata should be removed")
			_, err = os.Stat(filepath.Join(tempDir, gallery.GalleryFileName("newname")))
			Expect(err).ToNot(HaveOccurred(), "new gallery metadata should exist")

			// In-memory config loader holds exactly one entry, keyed by the new name.
			_, exists = modelConfigLoader.GetModelConfig("oldname")
			Expect(exists).To(BeFalse(), "old name must not remain in config loader")
			_, exists = modelConfigLoader.GetModelConfig("newname")
			Expect(exists).To(BeTrue(), "new name must be present in config loader")
			Expect(modelConfigLoader.GetAllModelsConfigs()).To(HaveLen(1))
		})

		It("broadcasts rename tombstone and install events with their own revisions", func() {
			systemState, err := system.GetSystemState(system.WithModelPath(tempDir))
			Expect(err).ToNot(HaveOccurred())
			applicationConfig := config.NewApplicationConfig(config.WithSystemState(systemState))
			loader := config.NewModelConfigLoader(tempDir)
			Expect(os.WriteFile(filepath.Join(tempDir, "old.yaml"), []byte("name: old\nbackend: llama-cpp\ncontext_size: 4096\n"), 0644)).To(Succeed())
			Expect(loader.LoadModelConfigsFromPath(tempDir)).To(Succeed())
			peerLoader := config.NewModelConfigLoader(tempDir)
			Expect(peerLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())
			galleryService := galleryop.NewGalleryService(applicationConfig, nil)
			client := &endpointRecordingClient{}
			galleryService.SetNATSClient(client)
			lifecycle := &endpointLifecycleRecorder{pendingCleanup: 2}
			app := echo.New()
			app.POST("/models/edit/:name", EditModelEndpoint(loader, galleryService, applicationConfig, lifecycle))

			req := httptest.NewRequest(http.MethodPost, "/models/edit/old", bytes.NewBufferString("name: new\nbackend: llama-cpp\ncontext_size: 8192\n"))
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			var response map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
			Expect(response).To(HaveKeyWithValue("config_revision", Not(BeEmpty())))
			Expect(response).To(HaveKeyWithValue("pending_cleanup", BeNumerically("==", 2)))
			Expect(client.published).To(HaveLen(2))
			Expect(client.published[0]).To(Equal(messaging.CacheInvalidateEvent{
				Element: "old", Op: "delete", ConfigRevision: modeladmin.DeletedModelConfigRevision("old"),
			}))
			_, ok := loader.GetModelConfig("new")
			Expect(ok).To(BeTrue())
			newRevision, err := loader.RevisionForPath("new", tempDir)
			Expect(err).ToNot(HaveOccurred())
			Expect(client.published[1]).To(Equal(messaging.CacheInvalidateEvent{
				Element: "new", Op: "install", ConfigRevision: newRevision,
			}))
			Expect(lifecycle.batches).To(HaveLen(1))
			Expect(lifecycle.batches[0]).To(Equal([]modeladmin.ModelRevisionTransition{
				{ModelName: "old", ConfigRevision: modeladmin.DeletedModelConfigRevision("old"), Disabled: true},
				{ModelName: "new", ConfigRevision: newRevision},
			}))

			peerLifecycle := &endpointLifecycleRecorder{}
			for _, event := range client.published {
				Expect(modeladmin.ApplyRemoteChange(context.Background(), peerLoader, tempDir, event, peerLifecycle, applicationConfig.ToConfigLoaderOptions()...)).To(Succeed())
			}
			_, oldOnPeer := peerLoader.GetModelConfig("old")
			Expect(oldOnPeer).To(BeFalse())
			_, newOnPeer := peerLoader.GetModelConfig("new")
			Expect(newOnPeer).To(BeTrue())
			peerRevision, err := peerLoader.RevisionForPath("new", tempDir)
			Expect(err).ToNot(HaveOccurred())
			Expect(peerRevision).To(Equal(newRevision))
			Expect(peerLifecycle.batches).To(Equal([][]modeladmin.ModelRevisionTransition{
				{
					{ModelName: "new", ConfigRevision: newRevision},
					{ModelName: "old", ConfigRevision: modeladmin.DeletedModelConfigRevision("old"), Disabled: true},
				},
				{{ModelName: "new", ConfigRevision: newRevision}},
			}))
		})

		It("rejects a rename when the new name already exists", func() {
			systemState, err := system.GetSystemState(
				system.WithModelPath(tempDir),
			)
			Expect(err).ToNot(HaveOccurred())
			applicationConfig := config.NewApplicationConfig(
				config.WithSystemState(systemState),
			)
			modelConfigLoader := config.NewModelConfigLoader(systemState.Model.ModelsPath)

			Expect(os.WriteFile(
				filepath.Join(tempDir, "oldname.yaml"),
				[]byte("name: oldname\nbackend: llama\nmodel: foo\n"),
				0644,
			)).To(Succeed())
			Expect(os.WriteFile(
				filepath.Join(tempDir, "newname.yaml"),
				[]byte("name: newname\nbackend: llama\nmodel: bar\n"),
				0644,
			)).To(Succeed())
			Expect(modelConfigLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

			app := echo.New()
			app.POST("/models/edit/:name", EditModelEndpoint(modelConfigLoader, nil, applicationConfig))

			req := httptest.NewRequest(
				"POST",
				"/models/edit/oldname",
				bytes.NewBufferString("name: newname\nbackend: llama\nmodel: foo\n"),
			)
			req.Header.Set("Content-Type", "application/x-yaml")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusConflict))

			// Neither file should have been rewritten.
			oldBody, err := os.ReadFile(filepath.Join(tempDir, "oldname.yaml"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(oldBody)).To(ContainSubstring("name: oldname"))
			newBody, err := os.ReadFile(filepath.Join(tempDir, "newname.yaml"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(newBody)).To(ContainSubstring("model: bar"))
		})

		It("rejects a rename whose new name contains a path separator", func() {
			systemState, err := system.GetSystemState(
				system.WithModelPath(tempDir),
			)
			Expect(err).ToNot(HaveOccurred())
			applicationConfig := config.NewApplicationConfig(
				config.WithSystemState(systemState),
			)
			modelConfigLoader := config.NewModelConfigLoader(systemState.Model.ModelsPath)

			Expect(os.WriteFile(
				filepath.Join(tempDir, "oldname.yaml"),
				[]byte("name: oldname\nbackend: llama\nmodel: foo\n"),
				0644,
			)).To(Succeed())
			Expect(modelConfigLoader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

			app := echo.New()
			app.POST("/models/edit/:name", EditModelEndpoint(modelConfigLoader, nil, applicationConfig))

			req := httptest.NewRequest(
				"POST",
				"/models/edit/oldname",
				bytes.NewBufferString("name: evil/name\nbackend: llama\nmodel: foo\n"),
			)
			req.Header.Set("Content-Type", "application/x-yaml")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			_, err = os.Stat(filepath.Join(tempDir, "oldname.yaml"))
			Expect(err).ToNot(HaveOccurred(), "original file must not be removed")
		})
	})
})
