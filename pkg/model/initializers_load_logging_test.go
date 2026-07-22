package model

import (
	"bytes"
	"log/slog"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// backendLoader is not a synonym for "a model is being loaded". In distributed
// mode Load() deliberately bypasses the local cache and calls backendLoader on
// every inference request so SmartRouter can re-pick a replica per request, so
// the function runs at request rate against an already-resident model. Emitting
// the load banner unconditionally made ordinary embedding traffic (~5 req/s)
// look like a retry storm in the INFO log and sent an engineer chasing a
// non-existent hot loop during an unrelated production investigation. INFO must
// mark a genuine cold load only; the per-call trace belongs at DEBUG.
var _ = Describe("backendLoader load logging", func() {
	var (
		ml       *ModelLoader
		captured *bytes.Buffer
	)

	BeforeEach(func() {
		systemState, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).ToNot(HaveOccurred())
		ml = NewModelLoader(systemState)

		// Capture at info level so a debug-level emission is filtered out: the
		// assertions then fail on severity, not merely on wording.
		captured = &bytes.Buffer{}
		handler := slog.NewTextHandler(captured, &slog.HandlerOptions{Level: slog.LevelInfo})
		xlog.SetLogger(xlog.NewLoggerWithHandler(handler, xlog.LogLevelInfo))
	})

	AfterEach(func() {
		// xlog exposes no getter for the package logger, so restore the same
		// default the suite entrypoint installs rather than the prior value.
		xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text"))
	})

	Context("when the model is already resident", func() {
		BeforeEach(func() {
			resident := NewModel("resident-model", "127.0.0.1:65535", nil)
			// Skip the gRPC health probe so the resident model survives the
			// lookup without a live backend behind the address.
			resident.MarkHealthy()
			ml.mu.Lock()
			ml.store.Set("resident-model", resident)
			ml.mu.Unlock()
		})

		It("does not announce a load at info level", func() {
			_, err := ml.backendLoader(
				WithModelID("resident-model"),
				WithModel("resident-model"),
				WithBackendString("llama-cpp"),
				WithLoadGRPCLoadModelOpts(&pb.ModelOptions{ContextSize: 4096}),
			)
			Expect(err).ToNot(HaveOccurred())

			Expect(captured.String()).ToNot(ContainSubstring("BackendLoader starting"),
				"a request served by an already-resident model must not look like a cold load")
		})

		It("does not repeat the effective runtime tuning banner at info level", func() {
			_, err := ml.backendLoader(
				WithModelID("resident-model"),
				WithModel("resident-model"),
				WithBackendString("llama-cpp"),
				WithLoadGRPCLoadModelOpts(&pb.ModelOptions{ContextSize: 4096}),
			)
			Expect(err).ToNot(HaveOccurred())

			Expect(captured.String()).ToNot(ContainSubstring("effective runtime tuning"),
				"the tuning banner documents what a load will run with, so it belongs to a load")
		})
	})

	Context("when the model is not resident", func() {
		It("still announces the cold load at info level", func() {
			// The load itself fails (no such backend is installed); what is
			// pinned here is that the banner is emitted before that, so
			// suppressing the warm case does not silence real loads.
			_, err := ml.backendLoader(
				WithModelID("cold-model"),
				WithModel("cold-model"),
				WithBackendString("definitely-not-an-installed-backend"),
				WithLoadGRPCLoadModelOpts(&pb.ModelOptions{ContextSize: 4096}),
			)
			Expect(err).To(HaveOccurred())

			Expect(captured.String()).To(ContainSubstring("BackendLoader starting"))
			Expect(captured.String()).To(ContainSubstring("effective runtime tuning"))
		})
	})
})
