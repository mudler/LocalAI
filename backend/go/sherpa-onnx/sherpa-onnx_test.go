package main

import (
	"context"
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
	startupWait = 5 * time.Second
)

func skipIfNoModel(t *testing.T) string {
	t.Helper()
	modelDir := os.Getenv("SHERPA_ONNX_MODEL_DIR")
	if modelDir == "" {
		t.Skip("SHERPA_ONNX_MODEL_DIR not set, skipping test (set to sherpa-onnx model directory)")
	}
	if _, err := os.Stat(filepath.Join(modelDir, "vits-ljs.onnx")); os.IsNotExist(err) {
		t.Skipf("Model file not found in %s, skipping", modelDir)
	}
	return modelDir
}

func skipIfNoASRModel(t *testing.T) string {
	t.Helper()
	modelDir := os.Getenv("SHERPA_ONNX_ASR_MODEL_DIR")
	if modelDir == "" {
		t.Skip("SHERPA_ONNX_ASR_MODEL_DIR not set, skipping test")
	}
	matches, _ := filepath.Glob(filepath.Join(modelDir, "*tokens.txt"))
	if len(matches) == 0 {
		t.Skipf("no *tokens.txt found in %s, skipping", modelDir)
	}
	return modelDir
}

func startServer(t *testing.T) *exec.Cmd {
	t.Helper()
	binary := os.Getenv("SHERPA_ONNX_BINARY")
	if binary == "" {
		binary = "./sherpa-onnx"
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
	resp, err := client.LoadModel(context.Background(), &pb.ModelOptions{
		ModelFile: filepath.Join(modelDir, "vits-ljs.onnx"),
	})
	if err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("LoadModel returned failure: %s", resp.Message)
	}
}

func TestTextToSpeech(t *testing.T) {
	modelDir := skipIfNoModel(t)

	tmpDir, err := os.MkdirTemp("", "sherpa-onnx-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cmd := startServer(t)
	defer stopServer(cmd)

	conn := dialGRPC(t)
	defer conn.Close()

	client := pb.NewBackendClient(conn)

	loadResp, err := client.LoadModel(context.Background(), &pb.ModelOptions{
		ModelFile: filepath.Join(modelDir, "vits-ljs.onnx"),
	})
	if err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}
	if !loadResp.Success {
		t.Fatalf("LoadModel returned failure: %s", loadResp.Message)
	}

	outputFile := filepath.Join(tmpDir, "output.wav")
	_, err = client.TTS(context.Background(), &pb.TTSRequest{
		Text: "Hello, this is a test of the Sherpa ONNX text to speech system.",
		Dst:  outputFile,
	})
	if err != nil {
		t.Fatalf("TTS failed: %v", err)
	}

	fileInfo, err := os.Stat(outputFile)
	if err != nil {
		t.Fatalf("Failed to stat output file: %v", err)
	}

	if fileInfo.Size() == 0 {
		t.Fatal("Output file is empty")
	}

	t.Logf("Generated audio file at %s, size: %d bytes", outputFile, fileInfo.Size())
}

func TestLoadASRModel(t *testing.T) {
	asrModelDir := skipIfNoASRModel(t)
	cmd := startServer(t)
	defer stopServer(cmd)

	conn := dialGRPC(t)
	defer conn.Close()

	client := pb.NewBackendClient(conn)

	// Point ModelFile at the directory (any file in it); findWhisperPair will
	// scan for *-encoder.onnx / *-decoder.onnx automatically.
	modelFile := filepath.Join(asrModelDir, "placeholder")

	resp, err := client.LoadModel(context.Background(), &pb.ModelOptions{
		ModelFile: modelFile,
		Type:      "asr",
	})
	if err != nil {
		t.Fatalf("LoadModel ASR failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("LoadModel ASR returned failure: %s", resp.Message)
	}
}

func TestAudioTranscription(t *testing.T) {
	asrModelDir := skipIfNoASRModel(t)
	ttsModelDir := skipIfNoModel(t)

	tmpDir, err := os.MkdirTemp("", "sherpa-onnx-asr-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Step 1: Generate audio with TTS
	cmd := startServer(t)
	defer stopServer(cmd)

	conn := dialGRPC(t)
	defer conn.Close()

	client := pb.NewBackendClient(conn)

	loadResp, err := client.LoadModel(context.Background(), &pb.ModelOptions{
		ModelFile: filepath.Join(ttsModelDir, "vits-ljs.onnx"),
	})
	if err != nil {
		t.Fatalf("LoadModel TTS failed: %v", err)
	}
	if !loadResp.Success {
		t.Fatalf("LoadModel TTS returned failure: %s", loadResp.Message)
	}

	audioFile := filepath.Join(tmpDir, "tts_output.wav")
	_, err = client.TTS(context.Background(), &pb.TTSRequest{
		Text: "The quick brown fox jumps over the lazy dog.",
		Dst:  audioFile,
	})
	if err != nil {
		t.Fatalf("TTS failed: %v", err)
	}

	fileInfo, err := os.Stat(audioFile)
	if err != nil {
		t.Fatalf("TTS output file not found: %v", err)
	}
	t.Logf("TTS generated %d bytes of audio", fileInfo.Size())

	// Step 2: Restart server and load ASR model
	stopServer(cmd)
	time.Sleep(1 * time.Second)
	cmd = startServer(t)
	defer stopServer(cmd)

	conn.Close()
	conn = dialGRPC(t)
	defer conn.Close()
	client = pb.NewBackendClient(conn)

	modelFile := filepath.Join(asrModelDir, "placeholder")

	loadResp, err = client.LoadModel(context.Background(), &pb.ModelOptions{
		ModelFile: modelFile,
		Type:      "asr",
	})
	if err != nil {
		t.Fatalf("LoadModel ASR failed: %v", err)
	}
	if !loadResp.Success {
		t.Fatalf("LoadModel ASR returned failure: %s", loadResp.Message)
	}

	// Step 3: Transcribe the TTS-generated audio
	transcriptResp, err := client.AudioTranscription(context.Background(), &pb.TranscriptRequest{
		Dst:      audioFile,
		Language: "en",
		Threads:  1,
	})
	if err != nil {
		t.Fatalf("AudioTranscription failed: %v", err)
	}

	text := strings.TrimSpace(transcriptResp.Text)
	if text == "" {
		t.Fatal("Transcription returned empty text")
	}

	t.Logf("Transcription result: %q", text)

	// Check that the transcription contains some expected words
	lower := strings.ToLower(text)
	expectedWords := []string{"fox", "dog"}
	for _, word := range expectedWords {
		if !strings.Contains(lower, word) {
			t.Logf("Warning: expected word %q not found in transcription %q", word, text)
		}
	}
}
