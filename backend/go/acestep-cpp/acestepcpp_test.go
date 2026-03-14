package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	testAddr    = "localhost:50051"
	startupWait = 5 * time.Second
)

func skipIfNoModel(t *testing.T) string {
	t.Helper()
	modelDir := os.Getenv("ACESTEP_MODEL_DIR")
	if modelDir == "" {
		t.Skip("ACESTEP_MODEL_DIR not set, skipping test (set to directory with GGUF models)")
	}
	if _, err := os.Stat(filepath.Join(modelDir, "acestep-5Hz-lm-0.6B-Q8_0.gguf")); os.IsNotExist(err) {
		t.Skipf("LM model file not found in %s, skipping", modelDir)
	}
	if _, err := os.Stat(filepath.Join(modelDir, "Qwen3-Embedding-0.6B-Q8_0.gguf")); os.IsNotExist(err) {
		t.Skipf("Text encoder model file not found in %s, skipping", modelDir)
	}
	if _, err := os.Stat(filepath.Join(modelDir, "acestep-v15-turbo-Q8_0.gguf")); os.IsNotExist(err) {
		t.Skipf("DiT model file not found in %s, skipping", modelDir)
	}
	if _, err := os.Stat(filepath.Join(modelDir, "vae-BF16.gguf")); os.IsNotExist(err) {
		t.Skipf("VAE model file not found in %s, skipping", modelDir)
	}
	return modelDir
}

func startServer(t *testing.T) *exec.Cmd {
	t.Helper()
	binary := os.Getenv("ACESTEP_BINARY")
	if binary == "" {
		binary = "./acestep-cpp"
	}
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		t.Skipf("Backend binary not found at %s, skipping", binary)
	}
	cmd := exec.Command(binary, "--addr", testAddr)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	time.Sleep(startupWait)
	return cmd
}

func stopServer(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

func dialGRPC(t *testing.T) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.Dial(testAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024),
			grpc.MaxCallSendMsgSize(50*1024*1024),
		),
	)
	if err != nil {
		t.Fatalf("Failed to dial gRPC: %v", err)
	}
	return conn
}

func TestServerHealth(t *testing.T) {
	cmd := startServer(t)
	defer stopServer(cmd)

	conn := dialGRPC(t)
	defer conn.Close()

	client := pb.NewBackendClient(conn)
	resp, err := client.Health(context.Background(), &pb.HealthMessage{})
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	if string(resp.Message) != "OK" {
		t.Fatalf("Expected OK, got %s", string(resp.Message))
	}
}

func TestLoadModel(t *testing.T) {
	modelDir := skipIfNoModel(t)
	cmd := startServer(t)
	defer stopServer(cmd)

	conn := dialGRPC(t)
	defer conn.Close()

	client := pb.NewBackendClient(conn)
	
	// Get base directory from main model file for relative paths
	mainModelPath := filepath.Join(modelDir, "acestep-5Hz-lm-0.6B-Q8_0.gguf")
	
	resp, err := client.LoadModel(context.Background(), &pb.ModelOptions{
		ModelFile: mainModelPath,
		Options: []string{
			"text_encoder_model:Qwen3-Embedding-0.6B-Q8_0.gguf",
			"dit_model:acestep-v15-turbo-Q8_0.gguf",
			"vae_model:vae-BF16.gguf",
		},
	})
	if err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("LoadModel returned failure: %s", resp.Message)
	}
}

func TestSoundGeneration(t *testing.T) {
	modelDir := skipIfNoModel(t)

	tmpDir, err := os.MkdirTemp("", "acestep-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	outputFile := filepath.Join(tmpDir, "output.wav")

	cmd := startServer(t)
	defer stopServer(cmd)

	conn := dialGRPC(t)
	defer conn.Close()

	client := pb.NewBackendClient(conn)

	// Get base directory from main model file for relative paths
	mainModelPath := filepath.Join(modelDir, "acestep-5Hz-lm-0.6B-Q8_0.gguf")

	// Load models
	loadResp, err := client.LoadModel(context.Background(), &pb.ModelOptions{
		ModelFile: mainModelPath,
		Options: []string{
			"text_encoder_model:Qwen3-Embedding-0.6B-Q8_0.gguf",
			"dit_model:acestep-v15-turbo-Q8_0.gguf",
			"vae_model:vae-BF16.gguf",
		},
	})
	if err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}
	if !loadResp.Success {
		t.Fatalf("LoadModel returned failure: %s", loadResp.Message)
	}

	// Generate music
	duration := float32(10.0)
	temperature := float32(0.85)
	bpm := int32(120)
	caption := "A cheerful electronic dance track"
	timesig := "4/4"

	_, err = client.SoundGeneration(context.Background(), &pb.SoundGenerationRequest{
		Text:          caption,
		Caption:       &caption,
		Dst:           outputFile,
		Duration:      &duration,
		Temperature:   &temperature,
		Bpm:           &bpm,
		Timesignature: &timesig,
	})
	if err != nil {
		t.Fatalf("SoundGeneration failed: %v", err)
	}

	// Verify output file exists and has content
	info, err := os.Stat(outputFile)
	if os.IsNotExist(err) {
		t.Fatal("Output audio file was not created")
	}
	if err != nil {
		t.Fatalf("Failed to stat output file: %v", err)
	}

	t.Logf("Output file size: %d bytes", info.Size())

	// WAV header is 44 bytes minimum; any real audio should be much larger
	if info.Size() < 1000 {
		t.Errorf("Output file too small (%d bytes), expected real audio data", info.Size())
	}
}
