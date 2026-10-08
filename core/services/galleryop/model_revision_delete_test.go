package galleryop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type deleteRevisionLifecycle struct {
	applied  bool
	revision string
	err      error
}

func (l *deleteRevisionLifecycle) ApplyConfigRevisions(_ context.Context, transitions []config.ModelConfigRevisionTransition) (int, error) {
	Expect(transitions).To(HaveLen(1))
	Expect(transitions[0].ModelName).To(Equal("doomed"))
	Expect(transitions[0].Disabled).To(BeTrue())
	l.applied = true
	l.revision = transitions[0].ConfigRevision
	return 1, l.err
}

type orderedDeleteManager struct {
	called bool
	path   string
}

type realDeletingManager struct {
	state       *system.SystemState
	afterDelete func() error
}

type countingMessagingClient struct{ subjects []string }

func (c *countingMessagingClient) Publish(subject string, _ any) error {
	c.subjects = append(c.subjects, subject)
	return nil
}
func (c *countingMessagingClient) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return nil, nil
}
func (c *countingMessagingClient) QueueSubscribe(string, string, func([]byte)) (messaging.Subscription, error) {
	return nil, nil
}
func (c *countingMessagingClient) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return nil, nil
}
func (c *countingMessagingClient) SubscribeReply(string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return nil, nil
}
func (c *countingMessagingClient) Request(string, []byte, time.Duration) ([]byte, error) {
	return nil, nil
}
func (c *countingMessagingClient) IsConnected() bool { return true }
func (c *countingMessagingClient) Close()            {}

func (m *realDeletingManager) DeleteModel(name string) error {
	if err := gallery.DeleteModelFromSystem(m.state, name); err != nil {
		return err
	}
	if m.afterDelete != nil {
		return m.afterDelete()
	}
	return nil
}

func (m *realDeletingManager) InstallModel(context.Context, *ManagementOp[gallery.GalleryModel, gallery.ModelConfig], ProgressCallback) error {
	return nil
}

func (m *orderedDeleteManager) DeleteModel(name string) error {
	m.called = true
	Expect(name).To(Equal("doomed"))
	if m.path != "" {
		return os.Remove(m.path)
	}
	return nil
}

type rejectingDeleteMaterializer struct{ calls int }

func (m *rejectingDeleteMaterializer) Ensure(context.Context, string, modelartifacts.Spec) (modelartifacts.Result, error) {
	m.calls++
	return modelartifacts.Result{}, errors.New("deleted config was preloaded")
}

func (m *orderedDeleteManager) InstallModel(context.Context, *ManagementOp[gallery.GalleryModel, gallery.ModelConfig], ProgressCallback) error {
	return nil
}

var _ = Describe("model deletion revision lifecycle", func() {
	It("deletes the real config, publishes its tombstone, and replaces the loader from disk", func() {
		dir := GinkgoT().TempDir()
		appConfig := &config.ApplicationConfig{SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}
		loader := config.NewModelConfigLoader(dir)
		path := filepath.Join(dir, "doomed.yaml")
		Expect(os.WriteFile(path, []byte("name: doomed\nbackend: llama-cpp\n"), 0644)).To(Succeed())
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		lifecycle := &deleteRevisionLifecycle{}
		service := NewGalleryService(appConfig, nil)
		service.SetModelRevisionLifecycle(lifecycle)
		service.SetModelManager(&orderedDeleteManager{path: path})
		op := &ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID: "delete-operation", GalleryElementName: "doomed", Delete: true, Context: context.Background(),
		}

		Expect(service.modelHandler(op, loader, appConfig.SystemState)).To(Succeed())
		Expect(lifecycle.revision).To(HaveLen(64))
		Expect(path).NotTo(BeAnExistingFile())
		_, ok := loader.GetModelConfig("doomed")
		Expect(ok).To(BeFalse())
		restarted := config.NewModelConfigLoader(dir)
		Expect(restarted.LoadModelConfigsFromPath(dir)).To(Succeed())
		_, ok = restarted.GetModelConfig("doomed")
		Expect(ok).To(BeFalse())
	})

	It("keeps standalone deletion authoritative and never preloads the deleted config", func() {
		dir := GinkgoT().TempDir()
		appConfig := &config.ApplicationConfig{SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}
		materializer := &rejectingDeleteMaterializer{}
		loader := config.NewModelConfigLoader(dir, config.WithArtifactMaterializer(materializer))
		path := filepath.Join(dir, "doomed.yaml")
		Expect(os.WriteFile(path, []byte("name: doomed\nbackend: llama-cpp\nartifacts:\n  - name: model\n    target: model\n    source: {type: huggingface, repo: owner/repo}\n"), 0644)).To(Succeed())
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		service := NewGalleryService(appConfig, nil)
		service.SetModelManager(&orderedDeleteManager{path: path})
		op := &ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID: "delete-operation", GalleryElementName: "doomed", Delete: true, Context: context.Background(),
		}

		Expect(service.modelHandler(op, loader, appConfig.SystemState)).To(Succeed())
		Expect(materializer.calls).To(Equal(0))
		_, ok := loader.GetModelConfig("doomed")
		Expect(ok).To(BeFalse())
	})

	It("does not replace the authoritative loader when tombstone publication fails", func() {
		dir := GinkgoT().TempDir()
		appConfig := &config.ApplicationConfig{SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}
		loader := config.NewModelConfigLoader(dir)
		path := filepath.Join(dir, "doomed.yaml")
		Expect(os.WriteFile(path, []byte("name: doomed\nbackend: llama-cpp\n"), 0644)).To(Succeed())
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		lifecycle := &deleteRevisionLifecycle{err: errors.New("registry unavailable")}
		manager := &realDeletingManager{state: appConfig.SystemState}
		service := NewGalleryService(appConfig, nil)
		service.SetModelRevisionLifecycle(lifecycle)
		service.SetModelManager(manager)
		op := &ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID: "delete-operation", GalleryElementName: "doomed", Delete: true, Context: context.Background(),
		}

		Expect(service.modelHandler(op, loader, appConfig.SystemState)).To(MatchError(ContainSubstring("registry unavailable")))
		Expect(path).To(BeAnExistingFile())
		_, ok := loader.GetModelConfig("doomed")
		Expect(ok).To(BeTrue())
		restarted := config.NewModelConfigLoader(dir)
		Expect(restarted.LoadModelConfigsFromPath(dir)).To(Succeed())
		_, ok = restarted.GetModelConfig("doomed")
		Expect(ok).To(BeTrue())
	})

	DescribeTable("rolls back real deletion before the commit boundary",
		func(failurePoint string) {
			dir := GinkgoT().TempDir()
			materializer := &rejectingDeleteMaterializer{}
			appConfig := &config.ApplicationConfig{
				SystemState:               &system.SystemState{Model: system.Model{ModelsPath: dir}},
				ModelArtifactMaterializer: materializer,
			}
			loader := config.NewModelConfigLoader(dir, config.WithArtifactMaterializer(materializer))
			configPath := filepath.Join(dir, "doomed.yaml")
			metadataPath := filepath.Join(dir, gallery.GalleryFileName("doomed"))
			configData := []byte("name: doomed\nbackend: llama-cpp\n")
			metadataData := []byte("files: []\n")
			Expect(os.WriteFile(configPath, configData, 0640)).To(Succeed())
			Expect(os.WriteFile(metadataPath, metadataData, 0600)).To(Succeed())

			manager := &realDeletingManager{state: appConfig.SystemState}
			lifecycle := &deleteRevisionLifecycle{}
			switch failurePoint {
			case "parse":
				manager.afterDelete = func() error {
					return os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("name: ["), 0644)
				}
			case "lifecycle":
				lifecycle.err = errors.New("injected lifecycle failure")
			}
			Expect(loader.LoadModelConfigsFromPath(dir, appConfig.ToConfigLoaderOptions()...)).To(Succeed())

			service := NewGalleryService(appConfig, nil)
			bus := &countingMessagingClient{}
			service.SetNATSClient(bus)
			service.SetModelManager(manager)
			service.SetModelRevisionLifecycle(lifecycle)
			op := &ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
				ID: "delete-operation", GalleryElementName: "doomed", Delete: true, Context: context.Background(),
			}

			Expect(service.modelHandler(op, loader, appConfig.SystemState)).ToNot(Succeed())
			Expect(bus.subjects).NotTo(ContainElement(messaging.SubjectCacheInvalidateModels))
			Expect(os.ReadFile(configPath)).To(Equal(configData))
			Expect(os.ReadFile(metadataPath)).To(Equal(metadataData))
			Expect(filepath.Join(dir, "broken.yaml")).NotTo(BeAnExistingFile())
			loaded, ok := loader.GetModelConfig("doomed")
			Expect(ok).To(BeTrue())
			Expect(loaded.Name).To(Equal("doomed"))
			fresh := config.NewModelConfigLoader(dir)
			Expect(fresh.LoadModelConfigsFromPath(dir)).To(Succeed())
			_, ok = fresh.GetModelConfig("doomed")
			Expect(ok).To(BeTrue())
		},
		Entry("when authoritative parsing fails", "parse"),
		Entry("when lifecycle publication fails", "lifecycle"),
	)

	// The preload walks every remaining model, not the deleted one, so its
	// failure says nothing about the deletion. Once the deletion is announced
	// to peers it must stay applied here too, or this replica would list a
	// model that every peer has already dropped.
	It("keeps and announces a committed deletion when the preload fails", func() {
		dir := GinkgoT().TempDir()
		materializer := &rejectingDeleteMaterializer{}
		appConfig := &config.ApplicationConfig{
			SystemState:               &system.SystemState{Model: system.Model{ModelsPath: dir}},
			ModelArtifactMaterializer: materializer,
		}
		loader := config.NewModelConfigLoader(dir, config.WithArtifactMaterializer(materializer))
		configPath := filepath.Join(dir, "doomed.yaml")
		Expect(os.WriteFile(configPath, []byte("name: doomed\nbackend: llama-cpp\n"), 0640)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, gallery.GalleryFileName("doomed")), []byte("files: []\n"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "survivor.yaml"), []byte("name: survivor\nbackend: transformers\nartifacts:\n  - name: model\n    target: model\n    source: {type: huggingface, repo: owner/repo}\n"), 0644)).To(Succeed())
		Expect(loader.LoadModelConfigsFromPath(dir, appConfig.ToConfigLoaderOptions()...)).To(Succeed())

		service := NewGalleryService(appConfig, nil)
		bus := &countingMessagingClient{}
		service.SetNATSClient(bus)
		service.SetModelManager(&realDeletingManager{state: appConfig.SystemState})
		lifecycle := &deleteRevisionLifecycle{}
		service.SetModelRevisionLifecycle(lifecycle)
		op := &ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID: "delete-operation", GalleryElementName: "doomed", Delete: true, Context: context.Background(),
		}

		err := service.modelHandler(op, loader, appConfig.SystemState)
		Expect(err).To(MatchError(ContainSubstring("preloading model files failed")))
		Expect(materializer.calls).To(BeNumerically(">", 0), "precondition: the preload ran and failed")
		Expect(lifecycle.applied).To(BeTrue())
		Expect(bus.subjects).To(ContainElement(messaging.SubjectCacheInvalidateModels))
		Expect(configPath).NotTo(BeAnExistingFile())
		_, ok := loader.GetModelConfig("doomed")
		Expect(ok).To(BeFalse())
		_, ok = loader.GetModelConfig("survivor")
		Expect(ok).To(BeTrue())
	})
})
