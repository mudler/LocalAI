package main

// main_test.go - end-to-end smoke test for the locate-anything-cpp gRPC backend.
//
// Spawns the compiled locate-anything-cpp binary on a free local port, dials it
// via gRPC, and exercises LoadModel + Detect against the test fixtures
// downloaded by test.sh: the q8_0 GGUF of nvidia/LocateAnything-3B and a real
// COCO image with people + cars. Asserts that open-vocabulary detection driven
// by a text prompt returns at least one detection, each carrying a non-empty
// class name and a bounding box of non-zero size.
//
// The spec Skip()s cleanly if its fixtures (the ~6.3 GB model, the test image,
// the built binary, or the fallback .so) are missing, so the test target stays
// usable on a fresh checkout / on CI runners where the large model hasn't been
// downloaded.

import (
	"context"
	"encoding/base64"
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

func TestDetect(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "locate-anything-cpp backend smoke suite")
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

// startBackend spawns the locate-anything-cpp binary on the given port and
// waits until it accepts TCP connections (up to 10s). It mirrors how main.go
// resolves the purego library: the LOCATEANYTHING_LIBRARY env var points the
// dlopen at the freshly built fallback .so, and the la_capi_* symbols are
// registered there. The returned cleanup func kills the process and reaps it.
func startBackend(port int) func() {
	binary, err := filepath.Abs("./locate-anything-cpp")
	Expect(err).ToNot(HaveOccurred())
	if _, err := os.Stat(binary); err != nil {
		Skip(fmt.Sprintf("backend binary not built: %s (run `make locate-anything-cpp` first)", binary))
	}

	libPath, err := filepath.Abs("./liblocateanythingcpp-fallback.so")
	Expect(err).ToNot(HaveOccurred())
	if _, err := os.Stat(libPath); err != nil {
		Skip(fmt.Sprintf("fallback library not built: %s (run `make liblocateanythingcpp-fallback.so` first)", libPath))
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd := exec.Command(binary, "--addr", addr)
	cmd.Env = append(os.Environ(), "LOCATEANYTHING_LIBRARY="+libPath)
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

// loadTestImage reads the COCO test image downloaded by test.sh and returns its
// base64-encoded content (the wire format accepted by the Detect RPC).
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
// current spec if it's missing (the ~6.3 GB GGUF is not present on a fresh
// checkout / on CI runners without the download).
func modelPathOrSkip(name string) string {
	modelDir, err := filepath.Abs("test-models")
	Expect(err).ToNot(HaveOccurred())
	modelPath := filepath.Join(modelDir, name)
	if _, err := os.Stat(modelPath); err != nil {
		Skip(fmt.Sprintf("model not present: %s (run test.sh first)", modelPath))
	}
	return modelPath
}

var _ = Describe("locate-anything-cpp backend", func() {
	It("runs open-vocabulary detection against a known-good COCO image", func() {
		modelPath := modelPathOrSkip("locate-anything-q8_0.gguf")
		imgB64 := loadTestImage()

		port := freePort()
		cleanup := startBackend(port)
		defer cleanup()

		client, closeConn := dialBackend(port)
		defer closeConn()

		// The q8_0 model is ~6.3 GB and hybrid Parallel Box Decoding on CPU is
		// not cheap, so give LoadModel + Detect a generous deadline.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()

		loadResp, err := client.LoadModel(ctx, &pb.ModelOptions{
			Model:     "locate-anything-q8_0.gguf",
			ModelFile: modelPath,
			Threads:   4,
		})
		Expect(err).ToNot(HaveOccurred(), "LoadModel")
		Expect(loadResp.GetSuccess()).To(BeTrue(), "LoadModel reported failure: %s", loadResp.GetMessage())

		// Open-vocabulary detection is prompt-driven; the prompt names the
		// classes to locate (people + cars), separated by the </c> control token.
		detResp, err := client.Detect(ctx, &pb.DetectOptions{
			Src:    imgB64,
			Prompt: "Locate all the instances that matches the following description: person</c>car.",
		})
		Expect(err).ToNot(HaveOccurred(), "Detect")
		Expect(detResp.GetDetections()).ToNot(BeEmpty(), "no detections returned on a known-good COCO image")

		_, _ = fmt.Fprintf(GinkgoWriter, "detection OK: %d detections\n", len(detResp.GetDetections()))
		for i, d := range detResp.GetDetections() {
			Expect(d.GetClassName()).ToNot(BeEmpty(), "detection %d has empty class_name", i)
			Expect(d.GetWidth()).To(BeNumerically(">", float32(0)),
				"detection %d has non-positive width", i)
			Expect(d.GetHeight()).To(BeNumerically(">", float32(0)),
				"detection %d has non-positive height", i)
			_, _ = fmt.Fprintf(GinkgoWriter, "  [%d] %s box=(%.1f,%.1f,%.1fx%.1f)\n",
				i, d.GetClassName(), d.GetX(), d.GetY(), d.GetWidth(), d.GetHeight())
		}
	})
})
