package model_test

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeRemoteUnloader records the models it was asked to unload so the specs can
// assert the remote path was actually taken (not merely that no error
// surfaced). It mirrors the real adapter's contract: unloading is idempotent
// and reports nil even when nothing was loaded, so presence is a separate
// question.
type fakeRemoteUnloader struct {
	called    []string
	unloadErr error

	present     bool
	presenceErr error
	asked       []string
}

func (f *fakeRemoteUnloader) UnloadRemoteModel(modelName string) error {
	f.called = append(f.called, modelName)
	return f.unloadErr
}

func (f *fakeRemoteUnloader) HasRemoteModel(_ context.Context, modelName string) (bool, error) {
	f.asked = append(f.asked, modelName)
	return f.present, f.presenceErr
}

// unloaderWithoutPresence is a RemoteModelUnloader that does NOT implement
// RemoteModelPresenceChecker, pinning the compatibility path for third-party
// implementations of the older interface.
type unloaderWithoutPresence struct{ called []string }

func (u *unloaderWithoutPresence) UnloadRemoteModel(modelName string) error {
	u.called = append(u.called, modelName)
	return nil
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
		unloader.present = true
		modelLoader.SetRemoteUnloader(unloader)

		err := modelLoader.ShutdownModel("longcat-video-avatar-1.5")

		Expect(unloader.called).To(ConsistOf("longcat-video-avatar-1.5"),
			"a model absent locally may still be loaded on a worker; the remote unloader must be consulted")
		Expect(err).ToNot(HaveOccurred(),
			"stopping a model that is running on a worker must succeed, not report 'model not found'")
	})

	It("reports not-found only after the registry confirms no node has it", func() {
		unloader.present = false
		modelLoader.SetRemoteUnloader(unloader)

		err := modelLoader.ShutdownModel("never-loaded")

		Expect(unloader.asked).To(ConsistOf("never-loaded"),
			"the registry must be consulted before declaring a model not found")
		Expect(err).To(MatchError(model.ErrModelNotFound),
			"absent locally AND cluster-wide is the only case that may report not-found")
		Expect(unloader.called).To(BeEmpty(),
			"nothing to unload — no point publishing a stop for a model no node holds")
	})

	It("does not claim not-found when the registry lookup fails", func() {
		// An unreachable registry is not evidence of absence. Reporting 404
		// here would tell an operator the model is gone on the strength of a
		// failed lookup.
		unloader.presenceErr = errors.New("registry unavailable")
		modelLoader.SetRemoteUnloader(unloader)

		err := modelLoader.ShutdownModel("maybe-loaded")

		Expect(err).To(HaveOccurred())
		Expect(err).ToNot(MatchError(model.ErrModelNotFound))
	})

	It("still unloads via an unloader that cannot answer presence", func() {
		// Older RemoteModelUnloader implementations have no presence check.
		// They must keep working: attempt the unload rather than refusing it.
		legacy := &unloaderWithoutPresence{}
		modelLoader.SetRemoteUnloader(legacy)

		err := modelLoader.ShutdownModel("some-model")

		Expect(legacy.called).To(ConsistOf("some-model"))
		Expect(err).ToNot(HaveOccurred())
	})

	It("still reports not-found when no remote unloader is configured", func() {
		// Single-node behavior must be unchanged.
		err := modelLoader.ShutdownModel("never-loaded")
		Expect(err).To(MatchError(model.ErrModelNotFound))
	})
})
