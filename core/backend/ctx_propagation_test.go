package backend_test

// Regression spec for X-LocalAI-Node coverage on audio/image/TTS/rerank/VAD.
//
// The X-LocalAI-Node middleware (core/http/middleware.ExposeNodeHeader)
// works end-to-end only if the per-request holder attached to the HTTP
// request context reaches the SmartRouter via ml.Load(opts...). The chain
// is:
//
//   handler -> backend.Foo(ctx, ...) -> ModelOptions(cfg, app, WithContext(ctx))
//     -> ml.Load(opts...) -> grpcModel(..., o.context) -> modelRouter(ctx, ...)
//     -> SmartRouter -> distributedhdr.Stamp(ctx, nodeID)
//
// If any backend helper drops `ctx` and lets ModelOptions fall back to the
// app context, the router never sees the per-request holder and the
// header silently stays empty for that endpoint. These specs pin the
// request-context-reaches-router contract for the five backend helpers
// that were previously dropping ctx between the handler and Load.

import (
	"context"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	pbproto "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/distributedhdr"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newCapturingLoader returns a ModelLoader wired with a stub model router
// that captures the context it receives and then short-circuits with a
// sentinel error. The router callback is the exact seam where the
// SmartRouter would call distributedhdr.Stamp in production, so observing
// the holder here is equivalent to observing it at the real router.
func newCapturingLoader() (*model.ModelLoader, *atomic.Value, func() context.Context) {
	loader := model.NewModelLoader(&system.SystemState{})
	var captured atomic.Value
	loader.SetModelRouter(func(ctx context.Context, _ string, _, _, _ string, _ *pbproto.ModelOptions, _ bool) (*model.Model, error) {
		captured.Store(ctx)
		// Return an error so the backend short-circuits before trying to
		// dial gRPC. We only care about the context-arrival contract.
		return nil, errRouterShortCircuit
	})
	get := func() context.Context {
		v, _ := captured.Load().(context.Context)
		return v
	}
	return loader, &captured, get
}

var errRouterShortCircuit = sentinelErr("router short-circuit (test)")

type sentinelErr string

func (s sentinelErr) Error() string { return string(s) }

func newAppCfg() *config.ApplicationConfig {
	return config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
}

func newModelCfg() config.ModelConfig {
	threads := 1
	cfg := config.ModelConfig{
		Name:    "test-model",
		Backend: "stub-backend",
		Threads: &threads,
	}
	cfg.Model = "test.bin"
	return cfg
}

var _ = Describe("X-LocalAI-Node ctx propagation contract", func() {
	const fakeNodeID = "node-ctx-propagation-7"

	var (
		appCfg      *config.ApplicationConfig
		modelCfg    config.ModelConfig
		loader      *model.ModelLoader
		routerCtxOf func() context.Context
		holder      *atomic.Value
		reqCtx      context.Context
	)

	BeforeEach(func() {
		appCfg = newAppCfg()
		modelCfg = newModelCfg()
		loader, _, routerCtxOf = newCapturingLoader()
		holder = distributedhdr.NewHolder()
		reqCtx = distributedhdr.WithHolder(context.Background(), holder)
	})

	// stampViaRouterCtx asserts the captured router context carries the
	// SAME holder that was attached to the request. We verify by stamping
	// through the router-side ctx and observing the value via the
	// request-side holder; if the holders were different objects the load
	// would return "".
	stampViaRouterCtx := func() {
		routerCtx := routerCtxOf()
		Expect(routerCtx).ToNot(BeNil(), "router callback must have been invoked")
		distributedhdr.Stamp(routerCtx, fakeNodeID)
		Expect(distributedhdr.Load(holder)).To(Equal(fakeNodeID),
			"stamp via router-side ctx must be observable via the request-side holder")
	}

	It("Rerank forwards the request context to the SmartRouter", func() {
		_, err := backend.Rerank(reqCtx, &pbproto.RerankRequest{Query: "q"}, loader, appCfg, modelCfg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("router short-circuit (test)"))
		stampViaRouterCtx()
	})

	It("VAD forwards the request context to the SmartRouter", func() {
		_, err := backend.VAD(&schema.VADRequest{}, reqCtx, loader, appCfg, modelCfg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("router short-circuit (test)"))
		stampViaRouterCtx()
	})

	It("ModelTTS forwards the request context to the SmartRouter", func() {
		_, _, err := backend.ModelTTS(reqCtx, "hello", "", "", "", nil, loader, appCfg, modelCfg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("router short-circuit (test)"))
		stampViaRouterCtx()
	})

	It("ModelTTSStream forwards the request context to the SmartRouter", func() {
		err := backend.ModelTTSStream(reqCtx, "hello", "", "", "", nil, loader, appCfg, modelCfg, func([]byte) error { return nil })
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("router short-circuit (test)"))
		stampViaRouterCtx()
	})

	It("ModelTranscriptionWithOptions forwards the request context to the SmartRouter", func() {
		_, err := backend.ModelTranscriptionWithOptions(reqCtx, backend.TranscriptionRequest{Audio: "x.wav"}, loader, modelCfg, appCfg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("router short-circuit (test)"))
		stampViaRouterCtx()
	})

	It("ModelTranscriptionStream forwards the request context to the SmartRouter", func() {
		err := backend.ModelTranscriptionStream(reqCtx, backend.TranscriptionRequest{Audio: "x.wav"}, loader, modelCfg, appCfg, func(backend.TranscriptionStreamChunk) {})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("router short-circuit (test)"))
		stampViaRouterCtx()
	})

	It("ImageGeneration forwards the request context to the SmartRouter", func() {
		_, err := backend.ImageGeneration(reqCtx, 64, 64, 1, 0, "p", "", "", "/tmp/out.png", loader, modelCfg, appCfg, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("router short-circuit (test)"))
		stampViaRouterCtx()
	})

	// Regression for #10636: a canceled request context must NOT cancel the
	// model LOAD. The heavy image/audio backends bind the load to the request
	// context so the routing holder reaches the SmartRouter; but a large
	// diffusers/LLM model on a slow (e.g. shared-memory iGPU) host can take
	// far longer to load than the client stays connected. If the request's
	// cancellation propagates to the load, the LoadModel RPC is aborted, the
	// backend process is torn down, and every retry restarts from scratch and
	// never converges. The load must instead run to completion and cache while
	// still carrying the request's routing holder value.
	It("ImageGeneration does not propagate request cancellation to the model load", func() {
		canceledCtx, cancel := context.WithCancel(reqCtx)
		cancel() // client disconnected while the (slow) load was still running

		_, err := backend.ImageGeneration(canceledCtx, 64, 64, 1, 0, "p", "", "", "/tmp/out.png", loader, modelCfg, appCfg, nil)
		// The load reached the router (short-circuit sentinel), i.e. it was
		// NOT aborted early by the already-canceled request context.
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("router short-circuit (test)"))

		routerCtx := routerCtxOf()
		Expect(routerCtx).ToNot(BeNil(), "router callback must have been invoked")
		Expect(routerCtx.Err()).To(BeNil(),
			"a canceled request must not cancel the model load")
		// The routing holder value still propagates despite the decoupling.
		stampViaRouterCtx()
	})

	It("does NOT leak the holder when the app context is used instead", func() {
		// Sanity: the bug being fixed manifests as the router getting
		// appCfg.Context (no holder) instead of reqCtx (holder). A direct
		// call with context.Background() must not see the holder via the
		// app context surface.
		appCtxOnly := appCfg.Context
		Expect(distributedhdr.Holder(appCtxOnly)).To(BeNil(),
			"the app context must not be the carrier of per-request holders")
	})
})
