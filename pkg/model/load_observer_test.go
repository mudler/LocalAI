package model_test

import (
	"os"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The load observer is the hook the core wires to backend tracing: it must
// fire once per actual load attempt with the alias-resolved backend and the
// resolved runtime URI, because that pair is what tells an operator WHICH
// installed backend build served a model (a stale install is invisible in
// the model config but shows up here).
var _ = Describe("ModelLoader load observer", func() {
	var (
		modelLoader *model.ModelLoader
		modelPath   string
		events      []model.BackendLoadEvent
	)

	BeforeEach(func() {
		var err error
		modelPath, err = os.MkdirTemp("", "load_observer")
		Expect(err).ToNot(HaveOccurred())

		systemState, err := system.GetSystemState(
			system.WithModelPath(modelPath),
		)
		Expect(err).ToNot(HaveOccurred())
		modelLoader = model.NewModelLoader(systemState)

		events = nil
		modelLoader.SetLoadObserver(func(ev model.BackendLoadEvent) {
			events = append(events, ev)
		})
	})

	AfterEach(func() {
		Expect(os.RemoveAll(modelPath)).To(Succeed())
	})

	It("fires with the resolved runtime URI when a load attempt fails", func() {
		// A non-file external backend URI is treated as a remote gRPC
		// address; nothing listens on port 1, so the health check fails
		// after the single configured attempt — a real, fast load attempt.
		modelLoader.SetExternalBackend("fakebackend", "127.0.0.1:1")

		_, err := modelLoader.Load(
			model.WithModelID("m"),
			model.WithModel("m.bin"),
			model.WithBackendString("fakebackend"),
			model.WithGRPCAttempts(1),
			model.WithGRPCAttemptsDelay(0),
		)
		Expect(err).To(HaveOccurred())

		Expect(events).To(HaveLen(1))
		ev := events[0]
		Expect(ev.ModelID).To(Equal("m"))
		Expect(ev.ModelName).To(Equal("m.bin"))
		Expect(ev.Backend).To(Equal("fakebackend"))
		Expect(ev.BackendURI).To(Equal("127.0.0.1:1"))
		Expect(ev.Err).To(HaveOccurred())
	})

	It("fires for unknown backends with an empty runtime URI", func() {
		_, err := modelLoader.Load(
			model.WithModelID("m"),
			model.WithModel("m.bin"),
			model.WithBackendString("no-such-backend"),
		)
		Expect(err).To(HaveOccurred())

		Expect(events).To(HaveLen(1))
		Expect(events[0].Backend).To(Equal("no-such-backend"))
		Expect(events[0].BackendURI).To(BeEmpty())
		Expect(events[0].Err).To(MatchError(ContainSubstring("backend not found")))
	})

	It("is optional: loads proceed when no observer is registered", func() {
		modelLoader.SetLoadObserver(nil)

		_, err := modelLoader.Load(
			model.WithModelID("m"),
			model.WithModel("m.bin"),
			model.WithBackendString("no-such-backend"),
		)
		Expect(err).To(HaveOccurred())
		Expect(events).To(BeEmpty())
	})
})
