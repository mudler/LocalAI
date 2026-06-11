package e2ebackends_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/phayes/freeport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ─── dllm templated chat-completion e2e (opt-in, BACKEND_BINARY mode) ────────
//
// The generic "Backend container" suite already exercises dllm's
// health/load/predict/stream surface in BACKEND_BINARY mode (ds4 precedent:
// hardware-gated backends skip the Docker image and point BACKEND_BINARY at a
// packaged run.sh). What it does NOT cover is the templated chat path: dllm
// owns prompt rendering AND output parsing natively (the gemma4
// renderer/parser, not llama.cpp's jinja autoparser), and that path only
// triggers when PredictOptions carries Messages + UseTokenizerTemplate.
// These specs drive exactly that round trip over the real gRPC server
// binary, non-streaming and streaming.
//
// Tiny-model spec (cheap, runs anywhere a libdllm.so build exists):
//
//	BACKEND_TEST_DLLM=1       enables the spec (skipped by default, CI-safe)
//	BACKEND_BINARY            packaged dllm run.sh (backend/go/dllm/run.sh with
//	                          dllm-grpc + libdllm.so next to it, or
//	                          package/run.sh from 'make -C backend/go/dllm package')
//	BACKEND_TEST_MODEL_FILE   dllm.cpp's tests/fixtures/tiny_with_vocab.gguf
//	                          (random weights + handcrafted 43-token gemma4 vocab)
//
// Real-model spec (the 26B BF16 GGUF, ~50 GB; CUDA-13-class hardware):
//
//	BACKEND_TEST_DLLM_REAL_MODEL_FILE  path to diffusiongemma-26B-A4B-it-BF16.gguf;
//	                                   setting it enables the spec (skipped by
//	                                   default; BACKEND_BINARY still required)
//	BACKEND_TEST_DLLM_REAL_GPU_LAYERS  NGPULayers for the real model
//	                                   (default -1 = full offload)
//
// Tool-call e2e is deliberately absent: the tiny fixture has RANDOM weights,
// so it cannot be prompted into emitting gemma4 <|tool_call> markup and a
// live tool-call assertion would be flaky-by-construction. Tool-call
// rendering and parsing are pinned by unit tables in backend/go/dllm
// (gemma4_renderer_test.go / gemma4_parser_test.go) instead; the real-model
// spec can grow a tools cap once a quantized checkpoint is cheap enough to
// gate on.

// startDllmBackend boots the packaged dllm backend via BACKEND_BINARY's
// run.sh, waits for the gRPC port, loads modelFile, and returns a connected
// client. Fails the spec on any error. Teardown is registered with
// DeferCleanup the moment each resource exists, so a failure anywhere in
// setup (port-wait timeout, dial error, LoadModel failure) still reaps the
// spawned server - critical for the real-model spec, where a failed load
// would otherwise leak a ~50GB process. options are extra ModelOptions
// "key:value" entries (eb_* sampler knobs etc.).
func startDllmBackend(modelFile string, gpuLayers int32, options ...string) pb.BackendClient {
	GinkgoHelper()

	binary := os.Getenv("BACKEND_BINARY")
	Expect(binary).NotTo(BeEmpty(),
		"dllm chat spec requires BACKEND_BINARY pointing at the packaged dllm run.sh")
	Expect(filepath.Base(binary)).To(Equal("run.sh"),
		"BACKEND_BINARY must point at a run.sh (see backend/go/dllm/package.sh)")
	binaryDir := filepath.Dir(binary)
	Expect(filepath.Join(binaryDir, "run.sh")).To(BeAnExistingFile())
	Expect(modelFile).To(BeAnExistingFile())

	port, err := freeport.GetFreePort()
	Expect(err).NotTo(HaveOccurred())
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	Expect(os.Chmod(filepath.Join(binaryDir, "run.sh"), 0o755)).To(Succeed())
	cmd := exec.Command(filepath.Join(binaryDir, "run.sh"), "--addr="+addr)
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter
	Expect(cmd.Start()).To(Succeed())
	DeferCleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	Eventually(func() error {
		c, derr := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if derr != nil {
			return derr
		}
		_ = c.Close()
		return nil
	}, 30*time.Second, 200*time.Millisecond).Should(Succeed(), "dllm backend did not start")

	conn, err := grpc.Dial(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(50*1024*1024)),
	)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		_ = conn.Close()
	})
	client := pb.NewBackendClient(conn)

	// 15 min: reading the 26B BF16 from a cold disk dominates real-model load.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	res, err := client.LoadModel(ctx, &pb.ModelOptions{
		Model:       modelFile,
		ModelFile:   modelFile,
		ContextSize: envInt32("BACKEND_TEST_CTX_SIZE", 512),
		Threads:     envInt32("BACKEND_TEST_THREADS", 4),
		NGPULayers:  gpuLayers,
		Options:     options,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(res.GetSuccess()).To(BeTrue(), "dllm LoadModel failed: %s", res.GetMessage())

	return client
}

// dllmChatRequest builds the templated chat request shared by both specs.
// The user content is fixed to "hello": the tiny fixture's handcrafted
// 43-token vocab is guaranteed to cover it (and the gemma4 template markup),
// while arbitrary English text is not tokenizable by that vocab.
func dllmChatRequest() *pb.PredictOptions {
	return &pb.PredictOptions{
		Messages:             []*pb.Message{{Role: "user", Content: "hello"}},
		UseTokenizerTemplate: true,
		// Rounds up to one whole 256-token canvas (dllm commits whole
		// canvases); keeps the tiny run fast and the real run bounded.
		Tokens:      16,
		Temperature: 0.1,
		Seed:        7,
	}
}

// assertDllmChat does the non-streaming templated round trip: no error,
// non-empty content, parsed ChatDeltas present.
func assertDllmChat(client pb.BackendClient) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	res, err := client.Predict(ctx, dllmChatRequest())
	Expect(err).NotTo(HaveOccurred())
	Expect(string(res.GetMessage())).NotTo(BeEmpty(), "templated chat completion produced empty content")
	Expect(res.GetChatDeltas()).NotTo(BeEmpty(), "templated chat completion produced no ChatDeltas")
	GinkgoWriter.Printf("dllm chat: %q (deltas=%d)\n", string(res.GetMessage()), len(res.GetChatDeltas()))
}

// assertDllmChatStream does the streaming variant: >=1 chunk, non-empty
// combined content.
func assertDllmChatStream(client pb.BackendClient) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	stream, err := client.PredictStream(ctx, dllmChatRequest())
	Expect(err).NotTo(HaveOccurred())

	var chunks int
	var combined string
	for {
		msg, rerr := stream.Recv()
		if rerr == io.EOF {
			break
		}
		Expect(rerr).NotTo(HaveOccurred())
		if len(msg.GetMessage()) > 0 {
			chunks++
			combined += string(msg.GetMessage())
		}
	}
	Expect(chunks).To(BeNumerically(">=", 1), "no stream chunks received")
	Expect(combined).NotTo(BeEmpty(), "streamed chat completion produced empty content")
	GinkgoWriter.Printf("dllm chat stream: %d chunks, combined=%q\n", chunks, combined)
}

var _ = Describe("dllm templated chat-completion (tiny model)", Ordered, func() {
	var client pb.BackendClient

	BeforeAll(func() {
		if os.Getenv("BACKEND_TEST_DLLM") != "1" {
			Skip("dllm chat spec is opt-in; set BACKEND_TEST_DLLM=1 (plus BACKEND_BINARY and BACKEND_TEST_MODEL_FILE) to run it")
		}
		modelFile := os.Getenv("BACKEND_TEST_MODEL_FILE")
		Expect(modelFile).NotTo(BeEmpty(),
			"dllm chat spec requires BACKEND_TEST_MODEL_FILE (dllm.cpp's tests/fixtures/tiny_with_vocab.gguf)")
		client = startDllmBackend(modelFile, 0)
	})

	It("answers a templated chat completion", func() {
		assertDllmChat(client)
	})

	It("streams a templated chat completion", func() {
		assertDllmChatStream(client)
	})
})

var _ = Describe("dllm request cancellation (tiny model)", Ordered, func() {
	var client pb.BackendClient

	BeforeAll(func() {
		if os.Getenv("BACKEND_TEST_DLLM") != "1" {
			Skip("dllm cancellation spec is opt-in; set BACKEND_TEST_DLLM=1 (plus BACKEND_BINARY and BACKEND_TEST_MODEL_FILE) to run it")
		}
		modelFile := os.Getenv("BACKEND_TEST_MODEL_FILE")
		Expect(modelFile).NotTo(BeEmpty(),
			"dllm cancellation spec requires BACKEND_TEST_MODEL_FILE (dllm.cpp's tests/fixtures/tiny_with_vocab.gguf)")
		// eb_max_steps inflates the per-block denoise loop: a 256-token run
		// takes ~10s on the tiny fixture (vs ~40ms at engine defaults), so a
		// cancelled request is clearly distinguishable from one that simply
		// finished. A dedicated backend process keeps the chat specs fast.
		client = startDllmBackend(modelFile, 0, "eb_max_steps:256")
	})

	// This is the end-to-end proof of the Cancellable plumbing
	// (pkg/grpc/server.go arming backend.Cancel via context.AfterFunc on
	// the stream context): a client disconnect mid-stream must abort the
	// server-side generation, not just orphan it.
	It("aborts the in-flight generation when the client context is cancelled mid-stream", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Raw-prompt mode, not the templated chat request: the templated
		// render can hit an end-of-turn token after the first block and
		// finish before the cancel lands, which would silently turn this
		// into a no-op spec. The raw "hello" run is probed deterministic
		// with seed 7: 16 blocks, the eb_max_steps cap hit on every one,
		// ~10s total if left to finish.
		req := &pb.PredictOptions{Prompt: "hello", Tokens: 256, Seed: 7}

		stream, err := client.PredictStream(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		// First chunk received = the generate is provably in flight (the C
		// side resets the cancel flag on generate entry, so cancelling
		// before it starts would be swallowed).
		_, err = stream.Recv()
		Expect(err).NotTo(HaveOccurred())
		cancel()

		// Client side: the stream must end promptly, not after the
		// remaining ~9s of generation (the first chunk arrives after one
		// ~0.7s block, so plenty of generation is provably outstanding).
		recvDone := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			for {
				if _, rerr := stream.Recv(); rerr != nil {
					recvDone <- rerr
					return
				}
			}
		}()
		var rerr error
		Eventually(recvDone, "5s").Should(Receive(&rerr))
		Expect(rerr).NotTo(Equal(io.EOF), "stream completed normally despite the cancelled context")

		// Server side: prove the generation actually aborted. dllm
		// serializes every C call through one worker goroutine, so if the
		// orphaned generation were still grinding, this follow-up would
		// queue behind its remaining ~9s instead of completing in ~1s
		// (16 tokens = one block at eb_max_steps:256).
		followCtx, followCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer followCancel()
		start := time.Now()
		res, err := client.Predict(followCtx, dllmChatRequest())
		elapsed := time.Since(start)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(res.GetMessage())).NotTo(BeEmpty())
		Expect(elapsed).To(BeNumerically("<", 5*time.Second),
			"follow-up request queued behind the cancelled generation - server-side Cancel did not reach the backend")
		GinkgoWriter.Printf("dllm cancel e2e: follow-up completed in %v after mid-stream cancellation\n", elapsed)
	})
})

var _ = Describe("dllm templated chat-completion (real model)", Ordered, func() {
	var client pb.BackendClient

	BeforeAll(func() {
		modelFile := os.Getenv("BACKEND_TEST_DLLM_REAL_MODEL_FILE")
		if modelFile == "" {
			Skip("real-model dllm spec is opt-in; set BACKEND_TEST_DLLM_REAL_MODEL_FILE (the 26B BF16 GGUF) to run it")
		}
		client = startDllmBackend(modelFile,
			envInt32("BACKEND_TEST_DLLM_REAL_GPU_LAYERS", -1))
	})

	It("answers a templated chat completion", func() {
		assertDllmChat(client)
	})

	It("streams a templated chat completion", func() {
		assertDllmChatStream(client)
	})
})
