package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	testAddr    = "localhost:50051"
	sampleAudio = "https://qianwen-res.oss-cn-beijing.aliyuncs.com/Qwen3-ASR-Repo/asr_en.wav"
	startupWait = 5 * time.Second
)

func skipIfNoModel(t *testing.T) string {
	t.Helper()
	modelDir := os.Getenv("VOXTRAL_MODEL_DIR")
	if modelDir == "" {
		t.Skip("VOXTRAL_MODEL_DIR not set, skipping test (set to voxtral model directory)")
	}
	if _, err := os.Stat(filepath.Join(modelDir, "consolidated.safetensors")); os.IsNotExist(err) {
		t.Skipf("Model file not found in %s, skipping", modelDir)
	}
	return modelDir
}

func startServer(t *testing.T) *exec.Cmd {
	t.Helper()
	binary := os.Getenv("VOXTRAL_BINARY")
	if binary == "" {
		binary = "./voxtral"
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

func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP GET failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
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
	resp, err := client.LoadModel(context.Background(), &pb.ModelOptions{
		ModelFile: modelDir,
	})
	if err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("LoadModel returned failure: %s", resp.Message)
	}
}

func TestAudioTranscription(t *testing.T) {
	modelDir := skipIfNoModel(t)

	tmpDir, err := os.MkdirTemp("", "voxtral-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Download sample audio — JFK "ask not what your country can do for you" clip
	audioFile := filepath.Join(tmpDir, "sample.wav")
	t.Log("Downloading sample audio...")
	if err := downloadFile(sampleAudio, audioFile); err != nil {
		t.Fatalf("Failed to download sample audio: %v", err)
	}

	cmd := startServer(t)
	defer stopServer(cmd)

	conn := dialGRPC(t)
	defer conn.Close()

	client := pb.NewBackendClient(conn)

	// Load model
	loadResp, err := client.LoadModel(context.Background(), &pb.ModelOptions{
		ModelFile: modelDir,
	})
	if err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}
	if !loadResp.Success {
		t.Fatalf("LoadModel returned failure: %s", loadResp.Message)
	}

	// Transcribe
	transcriptResp, err := client.AudioTranscription(context.Background(), &pb.TranscriptRequest{
		Dst: audioFile,
	})
	if err != nil {
		t.Fatalf("AudioTranscription failed: %v", err)
	}
	if transcriptResp == nil {
		t.Fatal("AudioTranscription returned nil")
	}

	t.Logf("Transcribed text: %s", transcriptResp.Text)
	t.Logf("Number of segments: %d", len(transcriptResp.Segments))

	if transcriptResp.Text == "" {
		t.Fatal("Transcription returned empty text")
	}

	allText := strings.ToLower(transcriptResp.Text)
	for _, seg := range transcriptResp.Segments {
		allText += " " + strings.ToLower(seg.Text)
	}
	t.Logf("All text: %s", allText)

	if !strings.Contains(allText, "big") {
		t.Errorf("Expected 'big' in transcription, got: %s", allText)
	}

	// The sample audio should contain recognizable speech
	if len(allText) < 10 {
		t.Errorf("Transcription too short: %q", allText)
	}
}
