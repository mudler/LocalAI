package main

// main_test.go - end-to-end smoke test for the rfdetr-cpp gRPC backend.
//
// Spawns the compiled rfdetr-cpp binary on a free local port, dials it via
// gRPC, and exercises LoadModel + Detect against the test fixtures
// downloaded by test.sh. Two scenarios:
//
//   1. detection — loads rfdetr-nano-q8_0.gguf and asserts at least one
//      detection comes back with a non-empty class name and a bounding box
//      of non-zero size.
//   2. segmentation — loads rfdetr-seg-nano-q8_0.gguf and additionally
//      asserts that at least one detection carries a PNG-encoded mask blob
//      (verified by PNG magic bytes).
//
// Both specs Skip cleanly if their fixtures are missing so the test target
// stays usable on a fresh checkout where models haven't been downloaded.

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

func TestRFDetrCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rfdetr-cpp backend smoke suite")
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

// startBackend spawns the rfdetr-cpp binary on the given port and waits
// until it accepts TCP connections (up to 10s). The returned cleanup func
// kills the process and reaps it.
func startBackend(port int) func() {
	binary, err := filepath.Abs("./rfdetr-cpp")
	Expect(err).ToNot(HaveOccurred())
	if _, err := os.Stat(binary); err != nil {
		Skip(fmt.Sprintf("backend binary not built: %s (run `make rfdetr-cpp` first)", binary))
	}

	libPath, err := filepath.Abs("./librfdetrcpp-fallback.so")
	Expect(err).ToNot(HaveOccurred())
	if _, err := os.Stat(libPath); err != nil {
		Skip(fmt.Sprintf("fallback library not built: %s (run `make librfdetrcpp-fallback.so` first)", libPath))
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd := exec.Command(binary, "--addr", addr)
	cmd.Env = append(os.Environ(), "RFDETR_LIBRARY="+libPath)
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

// loadTestImage reads the COCO test image downloaded by test.sh and returns
// its base64-encoded content (the wire format accepted by the Detect RPC).
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

// modelPathOrSkip resolves a model file under ./test-models/ and Skip()s
// the current spec if it's missing.
func modelPathOrSkip(name string) string {
	modelDir, err := filepath.Abs("test-models")
	Expect(err).ToNot(HaveOccurred())
	modelPath := filepath.Join(modelDir, name)
	if _, err := os.Stat(modelPath); err != nil {
		Skip(fmt.Sprintf("model not present: %s (run test.sh first)", modelPath))
	}
	return modelPath
}

var _ = Describe("rfdetr-cpp backend", func() {
	It("runs object detection against a known-good COCO image", func() {
		modelPath := modelPathOrSkip("rfdetr-nano-q8_0.gguf")
		imgB64 := loadTestImage()

		port := freePort()
		cleanup := startBackend(port)
		defer cleanup()

		client, closeConn := dialBackend(port)
		defer closeConn()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		loadResp, err := client.LoadModel(ctx, &pb.ModelOptions{
			Model:     "rfdetr-nano-q8_0.gguf",
			ModelFile: modelPath,
			Threads:   2,
		})
		Expect(err).ToNot(HaveOccurred(), "LoadModel")
		Expect(loadResp.GetSuccess()).To(BeTrue(), "LoadModel reported failure: %s", loadResp.GetMessage())

		detResp, err := client.Detect(ctx, &pb.DetectOptions{
			Src:       imgB64,
			Threshold: 0.5,
		})
		Expect(err).ToNot(HaveOccurred(), "Detect")
		Expect(detResp.GetDetections()).ToNot(BeEmpty(), "no detections returned on a known-good COCO image")

		_, _ = fmt.Fprintf(GinkgoWriter, "detection OK: %d detections\n", len(detResp.GetDetections()))
		for i, d := range detResp.GetDetections() {
			Expect(d.GetClassName()).ToNot(BeEmpty(), "detection %d has empty class_name", i)
			Expect(d.GetConfidence()).To(BeNumerically(">=", float32(0.5)),
				"detection %d below threshold", i)
			Expect(d.GetWidth()).To(BeNumerically(">", float32(0)),
				"detection %d has non-positive width", i)
			Expect(d.GetHeight()).To(BeNumerically(">", float32(0)),
				"detection %d has non-positive height", i)
		}
	})

	It("runs segmentation and returns PNG-encoded masks", func() {
		modelPath := modelPathOrSkip("rfdetr-seg-nano-q8_0.gguf")
		imgB64 := loadTestImage()

		port := freePort()
		cleanup := startBackend(port)
		defer cleanup()

		client, closeConn := dialBackend(port)
		defer closeConn()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		loadResp, err := client.LoadModel(ctx, &pb.ModelOptions{
			Model:     "rfdetr-seg-nano-q8_0.gguf",
			ModelFile: modelPath,
			Threads:   2,
		})
		Expect(err).ToNot(HaveOccurred(), "LoadModel")
		Expect(loadResp.GetSuccess()).To(BeTrue(), "LoadModel reported failure: %s", loadResp.GetMessage())

		detResp, err := client.Detect(ctx, &pb.DetectOptions{
			Src:       imgB64,
			Threshold: 0.5,
		})
		Expect(err).ToNot(HaveOccurred(), "Detect")
		Expect(detResp.GetDetections()).ToNot(BeEmpty(), "no detections returned from segmentation model")

		haveMask := false
		for i, d := range detResp.GetDetections() {
			m := d.GetMask()
			if len(m) == 0 {
				continue
			}
			haveMask = true
			// Verify PNG magic: 89 50 4E 47 ("\x89PNG").
			Expect(len(m)).To(BeNumerically(">=", 4), "detection %d mask too short", i)
			Expect([]byte{m[0], m[1], m[2], m[3]}).To(Equal([]byte{0x89, 'P', 'N', 'G'}),
				"detection %d mask is not a PNG", i)
		}
		Expect(haveMask).To(BeTrue(),
			"segmentation model returned %d detections but none carried a mask",
			len(detResp.GetDetections()))

		_, _ = fmt.Fprintf(GinkgoWriter, "segmentation OK: %d detections, at least one with PNG mask\n",
			len(detResp.GetDetections()))
	})
})
