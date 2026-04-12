package e2ebackends_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/phayes/freeport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Environment variables consumed by the suite.
//
// Required (one of):
//
//	BACKEND_IMAGE            Docker image tag to test (e.g. local-ai-backend:llama-cpp).
//
// Required model source (one of):
//
//	BACKEND_TEST_MODEL_URL   HTTP(S) URL of a model file to download before the test.
//	BACKEND_TEST_MODEL_FILE  Path to an already-available model file (skips download).
//
// Optional:
//
//	BACKEND_TEST_CAPS        Comma-separated list of capabilities to exercise.
//	                         Supported values: health, load, predict, stream, embeddings.
//	                         Defaults to "health,load,predict,stream".
//	                         A backend that only does embeddings would set this to
//	                         "health,load,embeddings"; an image/TTS backend that cannot
//	                         be driven by a text prompt can set it to "health,load".
//	BACKEND_TEST_PROMPT      Override the prompt used by predict/stream specs.
//	BACKEND_TEST_CTX_SIZE    Override the context size passed to LoadModel (default 512).
//	BACKEND_TEST_THREADS     Override Threads passed to LoadModel (default 4).
//
// The suite is intentionally model-format-agnostic: it only ever passes the
// file path to LoadModel, so GGUF, ONNX, safetensors, .bin etc. all work so
// long as the backend under test accepts that format.
const (
	capHealth     = "health"
	capLoad       = "load"
	capPredict    = "predict"
	capStream     = "stream"
	capEmbeddings = "embeddings"

	defaultPrompt = "The capital of France is"
	streamPrompt  = "Once upon a time"
)

func defaultCaps() map[string]bool {
	return map[string]bool{
		capHealth:  true,
		capLoad:    true,
		capPredict: true,
		capStream:  true,
	}
}

// parseCaps reads BACKEND_TEST_CAPS and returns the enabled capability set.
// An empty/unset value falls back to defaultCaps().
func parseCaps() map[string]bool {
	raw := strings.TrimSpace(os.Getenv("BACKEND_TEST_CAPS"))
	if raw == "" {
		return defaultCaps()
	}
	caps := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part != "" {
			caps[part] = true
		}
	}
	return caps
}

var _ = Describe("Backend container", Ordered, func() {
	var (
		caps      map[string]bool
		workDir   string
		binaryDir string
		modelFile string
		addr      string
		serverCmd *exec.Cmd
		conn      *grpc.ClientConn
		client    pb.BackendClient
		prompt    string
	)

	BeforeAll(func() {
		image := os.Getenv("BACKEND_IMAGE")
		Expect(image).NotTo(BeEmpty(), "BACKEND_IMAGE env var must be set (e.g. local-ai-backend:llama-cpp)")

		modelURL := os.Getenv("BACKEND_TEST_MODEL_URL")
		modelFile = os.Getenv("BACKEND_TEST_MODEL_FILE")
		Expect(modelURL != "" || modelFile != "").To(BeTrue(),
			"one of BACKEND_TEST_MODEL_URL or BACKEND_TEST_MODEL_FILE must be set")

		caps = parseCaps()
		GinkgoWriter.Printf("Testing image=%q with capabilities=%v\n", image, keys(caps))

		prompt = os.Getenv("BACKEND_TEST_PROMPT")
		if prompt == "" {
			prompt = defaultPrompt
		}

		var err error
		workDir, err = os.MkdirTemp("", "backend-e2e-*")
		Expect(err).NotTo(HaveOccurred())

		// Extract the image filesystem so we can run run.sh directly.
		binaryDir = filepath.Join(workDir, "rootfs")
		Expect(os.MkdirAll(binaryDir, 0o755)).To(Succeed())
		extractImage(image, binaryDir)
		Expect(filepath.Join(binaryDir, "run.sh")).To(BeAnExistingFile())

		// Download the model once if not provided.
		if modelFile == "" {
			modelFile = filepath.Join(workDir, "model.bin")
			downloadFile(modelURL, modelFile)
		}

		// Pick a free port and launch the backend.
		port, err := freeport.GetFreePort()
		Expect(err).NotTo(HaveOccurred())
		addr = fmt.Sprintf("127.0.0.1:%d", port)

		Expect(os.Chmod(filepath.Join(binaryDir, "run.sh"), 0o755)).To(Succeed())
		// Mark any other top-level files executable (extraction may strip perms).
		entries, _ := os.ReadDir(binaryDir)
		for _, e := range entries {
			if !e.IsDir() && !strings.HasSuffix(e.Name(), ".sh") {
				_ = os.Chmod(filepath.Join(binaryDir, e.Name()), 0o755)
			}
		}

		serverCmd = exec.Command(filepath.Join(binaryDir, "run.sh"), "--addr="+addr)
		serverCmd.Stdout = GinkgoWriter
		serverCmd.Stderr = GinkgoWriter
		Expect(serverCmd.Start()).To(Succeed())

		// Wait for the gRPC port to accept connections.
		Eventually(func() error {
			c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
			if err != nil {
				return err
			}
			_ = c.Close()
			return nil
		}, 30*time.Second, 200*time.Millisecond).Should(Succeed(), "backend did not start")

		conn, err = grpc.Dial(addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(50*1024*1024)),
		)
		Expect(err).NotTo(HaveOccurred())
		client = pb.NewBackendClient(conn)
	})

	AfterAll(func() {
		if conn != nil {
			_ = conn.Close()
		}
		if serverCmd != nil && serverCmd.Process != nil {
			_ = serverCmd.Process.Kill()
			_, _ = serverCmd.Process.Wait()
		}
		if workDir != "" {
			_ = os.RemoveAll(workDir)
		}
	})

	It("responds to Health", func() {
		if !caps[capHealth] {
			Skip("health capability not enabled")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, err := client.Health(ctx, &pb.HealthMessage{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.GetMessage()).NotTo(BeEmpty())
	})

	It("loads the model", func() {
		if !caps[capLoad] {
			Skip("load capability not enabled")
		}
		ctxSize := envInt32("BACKEND_TEST_CTX_SIZE", 512)
		threads := envInt32("BACKEND_TEST_THREADS", 4)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := client.LoadModel(ctx, &pb.ModelOptions{
			Model:       modelFile,
			ModelFile:   modelFile,
			ContextSize: ctxSize,
			Threads:     threads,
			NGPULayers:  0,
			MMap:        true,
			NBatch:      128,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.GetSuccess()).To(BeTrue(), "LoadModel failed: %s", res.GetMessage())
	})

	It("generates output via Predict", func() {
		if !caps[capPredict] {
			Skip("predict capability not enabled")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		res, err := client.Predict(ctx, &pb.PredictOptions{
			Prompt:      prompt,
			Tokens:      20,
			Temperature: 0.1,
			TopK:        40,
			TopP:        0.9,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.GetMessage()).NotTo(BeEmpty(), "Predict produced empty output")
		GinkgoWriter.Printf("Predict: %q (tokens=%d, prompt_tokens=%d)\n",
			res.GetMessage(), res.GetTokens(), res.GetPromptTokens())
	})

	It("streams output via PredictStream", func() {
		if !caps[capStream] {
			Skip("stream capability not enabled")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		stream, err := client.PredictStream(ctx, &pb.PredictOptions{
			Prompt:      streamPrompt,
			Tokens:      20,
			Temperature: 0.1,
			TopK:        40,
			TopP:        0.9,
		})
		Expect(err).NotTo(HaveOccurred())

		var chunks int
		var combined string
		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				break
			}
			Expect(err).NotTo(HaveOccurred())
			if len(msg.GetMessage()) > 0 {
				chunks++
				combined += string(msg.GetMessage())
			}
		}
		Expect(chunks).To(BeNumerically(">", 0), "no stream chunks received")
		GinkgoWriter.Printf("Stream: %d chunks, combined=%q\n", chunks, combined)
	})

	It("computes embeddings via Embedding", func() {
		if !caps[capEmbeddings] {
			Skip("embeddings capability not enabled")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := client.Embedding(ctx, &pb.PredictOptions{
			Embeddings: prompt,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.GetEmbeddings()).NotTo(BeEmpty(), "Embedding returned empty vector")
		GinkgoWriter.Printf("Embedding: %d dims\n", len(res.GetEmbeddings()))
	})
})

// extractImage runs `docker create` + `docker export` to materialise the image
// rootfs into dest. Using export (not save) avoids dealing with layer tarballs.
func extractImage(image, dest string) {
	GinkgoHelper()
	// The backend images have no default ENTRYPOINT/CMD, so docker create fails
	// unless we override one; run.sh is harmless and guaranteed to exist.
	create := exec.Command("docker", "create", "--entrypoint=/run.sh", image)
	out, err := create.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "docker create failed: %s", string(out))
	cid := strings.TrimSpace(string(out))
	DeferCleanup(func() {
		_ = exec.Command("docker", "rm", "-f", cid).Run()
	})

	// Pipe `docker export <cid>` into `tar -xf - -C dest`.
	exp := exec.Command("docker", "export", cid)
	expOut, err := exp.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())
	exp.Stderr = GinkgoWriter
	Expect(exp.Start()).To(Succeed())

	tar := exec.Command("tar", "-xf", "-", "-C", dest)
	tar.Stdin = expOut
	tar.Stderr = GinkgoWriter
	Expect(tar.Run()).To(Succeed())
	Expect(exp.Wait()).To(Succeed())
}

// downloadFile fetches url into dest using curl -L. Used for CI convenience;
// local runs can use BACKEND_TEST_MODEL_FILE to skip downloading.
func downloadFile(url, dest string) {
	GinkgoHelper()
	cmd := exec.Command("curl", "-sSfL", "-o", dest, url)
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter
	Expect(cmd.Run()).To(Succeed(), "failed to download %s", url)
	fi, err := os.Stat(dest)
	Expect(err).NotTo(HaveOccurred())
	Expect(fi.Size()).To(BeNumerically(">", 1024), "downloaded file is suspiciously small")
}

func envInt32(name string, def int32) int32 {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	var v int32
	_, err := fmt.Sscanf(raw, "%d", &v)
	if err != nil {
		return def
	}
	return v
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	return out
}
