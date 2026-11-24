package gallery_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// --disable-backend-install: nothing may land in BackendsPath from outside,
// backends already present (managed or system path) keep working, and a model
// install skips the backend step instead of failing on it.
var _ = Describe("Disabled backend installation", func() {
	var tempDir string
	var state *system.SystemState
	var ml *model.ModelLoader
	galleryRevision := 0

	// A runnable backend directory, as ListSystemBackends recognizes one.
	mkBackend := func(root, name string) {
		dir := filepath.Join(root, name)
		Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
		meta, _ := json.Marshal(&gallery.BackendMetadata{Name: name})
		Expect(os.WriteFile(filepath.Join(dir, "metadata.json"), meta, 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o755)).To(Succeed())
	}

	// A file:// model gallery, inside the models path (the only root a file
	// URL is trusted from); the listing is cached on the name and URL pair,
	// so every spec gets a gallery of its own.
	newGallery := func(entries ...gallery.GalleryModel) []config.Gallery {
		out, err := yaml.Marshal(entries)
		Expect(err).ToNot(HaveOccurred())
		name := fmt.Sprintf("install-disabled-%d", galleryRevision)
		galleryRevision++
		path := filepath.Join(state.Model.ModelsPath, name+".yaml")
		Expect(os.WriteFile(path, out, 0o600)).To(Succeed())
		return []config.Gallery{{Name: name, URL: "file://" + path}}
	}

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "backend-install-disabled-*")
		Expect(err).NotTo(HaveOccurred())
		state, err = system.GetSystemState(
			system.WithBackendPath(filepath.Join(tempDir, "backends")),
			system.WithBackendSystemPath(filepath.Join(tempDir, "system-backends")),
			system.WithModelPath(filepath.Join(tempDir, "models")),
			system.WithDisableBackendInstall(true),
		)
		Expect(err).NotTo(HaveOccurred())
		for _, dir := range []string{state.Model.ModelsPath, state.Backend.BackendsPath, state.Backend.BackendsSystemPath} {
			Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
		}
		ml = model.NewModelLoader(state)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	It("InstallBackend refuses with the typed error and writes nothing", func() {
		src, err := os.MkdirTemp("", "backend-src-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(src)
		Expect(os.WriteFile(filepath.Join(src, "run.sh"), []byte("#!/bin/sh\n"), 0o755)).To(Succeed())

		backend := &gallery.GalleryBackend{}
		backend.Name = "test-backend"
		backend.URI = src

		err = gallery.InstallBackend(context.Background(), state, ml, backend, nil, false)
		Expect(errors.Is(err, gallery.ErrBackendInstallDisabled)).To(BeTrue(), err.Error())
		_, statErr := os.Stat(filepath.Join(state.Backend.BackendsPath, "test-backend"))
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})

	It("InstallBackendFromGallery refuses before consulting any gallery", func() {
		err := gallery.InstallBackendFromGallery(context.Background(), nil, state, ml, "vllm", nil, false, false)
		Expect(errors.Is(err, gallery.ErrBackendInstallDisabled)).To(BeTrue(), err.Error())
	})

	It("InstallBackendFromGallery still accepts a backend that is already installed", func() {
		mkBackend(state.Backend.BackendsSystemPath, "llama-cpp")
		Expect(gallery.InstallBackendFromGallery(context.Background(), nil, state, ml, "llama-cpp", nil, false, false)).To(Succeed())
	})

	It("UpgradeBackend refuses even for an installed backend", func() {
		mkBackend(state.Backend.BackendsPath, "llama-cpp")
		err := gallery.UpgradeBackend(context.Background(), state, ml, nil, "llama-cpp", nil, false)
		Expect(errors.Is(err, gallery.ErrBackendInstallDisabled)).To(BeTrue(), err.Error())
		Expect(filepath.Join(state.Backend.BackendsPath, "llama-cpp", "run.sh")).To(BeAnExistingFile())
	})

	It("RequiredBackendInstalled reports the entry's backend and its presence", func() {
		mkBackend(state.Backend.BackendsSystemPath, "llama-cpp")
		galleries := newGallery(
			gallery.GalleryModel{Metadata: gallery.Metadata{Name: "needs-vllm", Backend: "vllm"}},
			gallery.GalleryModel{Metadata: gallery.Metadata{Name: "needs-llama", Backend: "llama-cpp"}},
			gallery.GalleryModel{Metadata: gallery.Metadata{Name: "no-backend"}},
		)

		backend, installed, err := gallery.RequiredBackendInstalled(context.Background(), galleries, state, "needs-vllm", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend).To(Equal("vllm"))
		Expect(installed).To(BeFalse())

		backend, installed, err = gallery.RequiredBackendInstalled(context.Background(), galleries, state, "needs-llama", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend).To(Equal("llama-cpp"))
		Expect(installed).To(BeTrue())

		backend, installed, err = gallery.RequiredBackendInstalled(context.Background(), galleries, state, "no-backend", "", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend).To(BeEmpty())
		Expect(installed).To(BeTrue())

		// A distributed frontend has no local backends; the execution
		// target's answer decides.
		onCluster := func(string) (bool, error) { return true, nil }
		backend, installed, err = gallery.RequiredBackendInstalled(context.Background(), galleries, state, "needs-vllm", "", onCluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend).To(Equal("vllm"))
		Expect(installed).To(BeTrue())
	})

	It("AvailableOnCluster reads worker presence, not local files", func() {
		cluster := gallery.SystemBackends{
			"vllm":   {Name: "vllm", Nodes: []gallery.NodeBackendRef{{NodeID: "w1", NodeName: "worker-1", NodeStatus: "healthy"}}},
			"orphan": {Name: "orphan"},
			// Listed because the node still answers, but the router will not
			// place a load there until its heartbeat returns.
			"ailing": {Name: "ailing", Nodes: []gallery.NodeBackendRef{{NodeID: "w3", NodeName: "worker-3", NodeStatus: "unhealthy"}}},
			// Workers send the alias on the concrete's row, never as a row of its own.
			"llama-cpp-dev": {Name: "llama-cpp-dev", Metadata: &gallery.BackendMetadata{Name: "llama-cpp-dev", Alias: "llama-cpp"},
				Nodes: []gallery.NodeBackendRef{{NodeID: "w2", NodeName: "worker-2", NodeStatus: "healthy"}}},
			"stale": {Name: "stale", Metadata: &gallery.BackendMetadata{Name: "stale", Alias: "gone"}},
		}
		Expect(gallery.AvailableOnCluster(cluster, "vllm")).To(BeTrue())
		Expect(gallery.AvailableOnCluster(cluster, "orphan")).To(BeFalse())
		Expect(gallery.AvailableOnCluster(cluster, "missing")).To(BeFalse())
		Expect(gallery.AvailableOnCluster(cluster, "ailing")).To(BeFalse())
		Expect(gallery.AvailableOnCluster(cluster, "llama-cpp")).To(BeTrue())
		Expect(gallery.AvailableOnCluster(cluster, "gone")).To(BeFalse())
	})

	It("a model install trusts the execution target's backend availability", func() {
		// Empty local backend directories, as on a distributed frontend.
		Expect(os.WriteFile(filepath.Join(state.Model.ModelsPath, "weights.gguf"), []byte("weights"), 0o600)).To(Succeed())
		galleries := newGallery(gallery.GalleryModel{
			Metadata:   gallery.Metadata{Name: "on-worker", Backend: "vllm"},
			ConfigFile: map[string]any{"backend": "vllm", "parameters": map[string]any{"model": "weights.gguf"}},
		})
		onCluster := func(string) (bool, error) { return true, nil }

		err := gallery.InstallModelFromGallery(context.Background(), galleries, nil, state, ml,
			"on-worker", gallery.GalleryModel{}, func(string, string, string, float64) {}, false, false, false,
			gallery.WithBackendAvailability(onCluster))
		Expect(err).NotTo(HaveOccurred())
		Expect(filepath.Join(state.Model.ModelsPath, "on-worker.yaml")).To(BeAnExistingFile())
	})

	It("a model install is refused before anything is downloaded when its backend is missing", func() {
		galleries := newGallery(gallery.GalleryModel{
			Metadata: gallery.Metadata{
				Name:    "needs-vllm",
				Backend: "vllm",
				// A download that cannot succeed: reaching it would fail with a
				// different error than the refusal asserted below.
				AdditionalFiles: []gallery.File{{Filename: "weights.gguf", URI: "http://127.0.0.1:9/weights.gguf"}},
			},
			ConfigFile: map[string]any{"backend": "vllm", "parameters": map[string]any{"model": "weights.gguf"}},
		})

		err := gallery.InstallModelFromGallery(context.Background(), galleries, nil, state, ml,
			"needs-vllm", gallery.GalleryModel{}, func(string, string, string, float64) {}, false, true, false)
		Expect(errors.Is(err, gallery.ErrBackendInstallDisabled)).To(BeTrue(), err.Error())
		Expect(err.Error()).To(ContainSubstring(`"vllm"`))
		Expect(filepath.Join(state.Model.ModelsPath, "needs-vllm.yaml")).NotTo(BeAnExistingFile())
		Expect(filepath.Join(state.Model.ModelsPath, "weights.gguf")).NotTo(BeAnExistingFile())
	})

	It("a model install proceeds when its backend is already present", func() {
		mkBackend(state.Backend.BackendsSystemPath, "llama-cpp")
		// Seeded weights keep the install off the network: an already-present
		// destination with no declared sha256 counts as fetched.
		Expect(os.WriteFile(filepath.Join(state.Model.ModelsPath, "weights.gguf"), []byte("weights"), 0o600)).To(Succeed())
		galleries := newGallery(gallery.GalleryModel{
			Metadata:   gallery.Metadata{Name: "has-llama", Backend: "llama-cpp"},
			ConfigFile: map[string]any{"backend": "llama-cpp", "parameters": map[string]any{"model": "weights.gguf"}},
		})

		err := gallery.InstallModelFromGallery(context.Background(), galleries, nil, state, ml,
			"has-llama", gallery.GalleryModel{}, func(string, string, string, float64) {}, false, true, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(filepath.Join(state.Model.ModelsPath, "has-llama.yaml")).To(BeAnExistingFile())
	})
})
