package main

// main_test.go - end-to-end smoke test for the depth-anything-cpp gRPC backend.
//
// Spawns the compiled depth-anything-cpp binary on a free local port, dials it
// via gRPC, and exercises LoadModel + Predict against the test fixtures
// downloaded by test.sh: the small (vits) f32 GGUF of Depth Anything 3 and a
// real photo. Asserts that Predict returns a JSON payload with a positive
// depth-map width/height.
//
// The spec Skip()s cleanly if its fixtures (the model, the test image, the
// built binary, or the fallback .so) are missing, so the test target stays
// usable on a fresh checkout / on CI runners where the model hasn't been
// downloaded.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestDepth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "depth-anything-cpp backend smoke suite")
}

// freePort grabs an ephemeral TCP port and immediately releases it so the
// spawned backend can bind to it. There is a tiny TOCTOU window here but in
// practice it's adequate for a smoke test on a quiet runner.
func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred(), "freePort listen")
	port := l.Addr().(*net.TCPAddr).Port
	Expect(l.Close()).To(Succeed())
	return port
}

// startBackend spawns the depth-anything-cpp binary on the given port and waits
// until it accepts TCP connections (up to 10s). It mirrors how main.go resolves
// the purego library: the DEPTHANYTHING_LIBRARY env var points the dlopen at the
// freshly built fallback .so. The returned cleanup func kills the process.
func startBackend(port int) func() {
	binary, err := filepath.Abs("./depth-anything-cpp")
	Expect(err).ToNot(HaveOccurred())
	if _, err := os.Stat(binary); err != nil {
		Skip(fmt.Sprintf("backend binary not built: %s (run `make depth-anything-cpp` first)", binary))
	}

	libPath, err := filepath.Abs("./libdepthanythingcpp-fallback.so")
	Expect(err).ToNot(HaveOccurred())
	if _, err := os.Stat(libPath); err != nil {
		Skip(fmt.Sprintf("fallback library not built: %s (run `make libdepthanythingcpp-fallback.so` first)", libPath))
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd := exec.Command(binary, "--addr", addr)
	cmd.Env = append(os.Environ(), "DEPTHANYTHING_LIBRARY="+libPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	Expect(cmd.Start()).To(Succeed())

	cleanup := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return cleanup
		}
		time.Sleep(200 * time.Millisecond)
	}

	cleanup()
	Fail(fmt.Sprintf("backend did not become ready on %s within 10s", addr))
	return func() {}
}

// loadTestImage reads the test image downloaded by test.sh and returns its
// base64-encoded content (one of the wire formats accepted by Predict).
func loadTestImage() string {
	imgPath, err := filepath.Abs("test-data/test.jpg")
	Expect(err).ToNot(HaveOccurred())
	imgBytes, err := os.ReadFile(imgPath)
	if err != nil {
		Skip(fmt.Sprintf("test image not present: %s (run test.sh first)", imgPath))
	}
	return base64.StdEncoding.EncodeToString(imgBytes)
}

// dialBackend opens a gRPC client connection to the spawned backend.
func dialBackend(port int) (pb.BackendClient, func()) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	Expect(err).ToNot(HaveOccurred())
	return pb.NewBackendClient(conn), func() { _ = conn.Close() }
}

// modelPathOrSkip resolves the model file under ./test-models/ and Skip()s the
// current spec if it's missing (not present on a fresh checkout / on CI runners
// without the download).
func modelPathOrSkip(name string) string {
	modelDir, err := filepath.Abs("test-models")
	Expect(err).ToNot(HaveOccurred())
	modelPath := filepath.Join(modelDir, name)
	if _, err := os.Stat(modelPath); err != nil {
		Skip(fmt.Sprintf("model not present: %s (run test.sh first)", modelPath))
	}
	return modelPath
}

var _ = Describe("depth-anything-cpp backend", func() {
	It("runs depth+pose against a known-good image", func() {
		modelPath := modelPathOrSkip("depth-anything-small-f32.gguf")
		imgB64 := loadTestImage()

		port := freePort()
		cleanup := startBackend(port)
		defer cleanup()

		client, closeConn := dialBackend(port)
		defer closeConn()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()

		loadResp, err := client.LoadModel(ctx, &pb.ModelOptions{
			Model:     "depth-anything-small-f32.gguf",
			ModelFile: modelPath,
			Threads:   4,
		})
		Expect(err).ToNot(HaveOccurred(), "LoadModel")
		Expect(loadResp.GetSuccess()).To(BeTrue(), "LoadModel reported failure: %s", loadResp.GetMessage())

		// Predict runs depth+pose and returns the JSON depthResult in Reply.Message.
		reply, err := client.Predict(ctx, &pb.PredictOptions{
			Images: []string{imgB64},
		})
		Expect(err).ToNot(HaveOccurred(), "Predict")

		var res depthResult
		Expect(json.Unmarshal(reply.GetMessage(), &res)).To(Succeed(), "Predict returned non-JSON: %q", string(reply.GetMessage()))
		Expect(res.DepthW).To(BeNumerically(">", 0), "depth width should be positive")
		Expect(res.DepthH).To(BeNumerically(">", 0), "depth height should be positive")

		_, _ = fmt.Fprintf(GinkgoWriter, "depth OK: %dx%d min=%.3f max=%.3f\n",
			res.DepthW, res.DepthH, res.DepthMin, res.DepthMax)
	})
})
