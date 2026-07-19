package model_test

import (
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeRemoteUnloader records the models it was asked to unload so the specs
// can assert the remote path was actually taken (not merely that no error
// surfaced).
type fakeRemoteUnloader struct {
	called []string
	err    error
}

func (f *fakeRemoteUnloader) UnloadRemoteModel(modelName string) error {
	f.called = append(f.called, modelName)
	return f.err
}

// In distributed mode the authoritative record of "is this model loaded" is
// the shared node registry, not this replica's in-memory store. A frontend
// replica that never served the model itself (load balancer picked another
// replica, or this one restarted) has no local entry, so ShutdownModel
// short-circuited on a local-store miss and reported "model not found" for a
// model that was demonstrably running on a worker — while the remote unload
// path it documents was never reached.
var _ = Describe("ShutdownModel in distributed mode", func() {
	var (
		modelLoader *model.ModelLoader
		unloader    *fakeRemoteUnloader
	)

	BeforeEach(func() {
		systemState, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).ToNot(HaveOccurred())
		modelLoader = model.NewModelLoader(systemState)
		unloader = &fakeRemoteUnloader{}
	})

	It("delegates to the remote unloader when the model is not in the local store", func() {
		modelLoader.SetRemoteUnloader(unloader)

		err := modelLoader.ShutdownModel("longcat-video-avatar-1.5")

		Expect(unloader.called).To(ConsistOf("longcat-video-avatar-1.5"),
			"a model absent locally may still be loaded on a worker; the remote unloader must be consulted")
		Expect(err).ToNot(HaveOccurred(),
			"stopping a model that is running on a worker must succeed, not report 'model not found'")
	})

	It("reports not-found only after the remote unloader confirms no node has it", func() {
		unloader.err = model.ErrRemoteModelNotLoaded
		modelLoader.SetRemoteUnloader(unloader)

		err := modelLoader.ShutdownModel("never-loaded")

		Expect(unloader.called).To(ConsistOf("never-loaded"),
			"the registry must be consulted before declaring a model not found")
		Expect(err).To(HaveOccurred(),
			"a genuinely absent model must still error — silently succeeding would hide a failed stop")
	})

	It("still reports not-found when no remote unloader is configured", func() {
		// Single-node behavior must be unchanged.
		err := modelLoader.ShutdownModel("never-loaded")
		Expect(err).To(HaveOccurred())
	})
})
