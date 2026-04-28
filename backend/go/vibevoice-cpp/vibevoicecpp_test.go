package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	testAddr    = "localhost:50098"
	startupWait = 5 * time.Second
)

func skipIfNoModel(t *testing.T) string {
	t.Helper()
	modelDir := os.Getenv("VIBEVOICE_MODEL_DIR")
	if modelDir == "" {
		t.Skip("VIBEVOICE_MODEL_DIR not set, skipping model-dependent tests")
	}
	required := []string{
		"tokenizer.gguf",
	}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(modelDir, name)); os.IsNotExist(err) {
			t.Skipf("required %s not found in %s, skipping", name, modelDir)
		}
	}
	hasTTS, _ := filepath.Glob(filepath.Join(modelDir, "vibevoice-realtime-*.gguf"))
	hasASR, _ := filepath.Glob(filepath.Join(modelDir, "vibevoice-asr-*.gguf"))
	if len(hasTTS) == 0 && len(hasASR) == 0 {
		t.Skipf("neither vibevoice-realtime nor vibevoice-asr GGUFs found in %s", modelDir)
	}
	return modelDir
}

func startServer(t *testing.T) *exec.Cmd {
	t.Helper()
	binary := os.Getenv("VIBEVOICE_BINARY")
	if binary == "" {
		binary = "./vibevoice-cpp"
	}
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		t.Skipf("backend binary not found at %s, skipping", binary)
	}
	cmd := exec.Command(binary, "--addr", testAddr)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	time.Sleep(startupWait)
	return cmd
}

func stopServer(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
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
		t.Fatalf("failed to dial gRPC: %v", err)
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
		t.Fatalf("health check failed: %v", err)
	}
	if string(resp.Message) != "OK" {
		t.Fatalf("expected OK, got %s", string(resp.Message))
	}
}

func TestLoadModel(t *testing.T) {
	modelDir := skipIfNoModel(t)
	cmd := startServer(t)
	defer stopServer(cmd)

	conn := dialGRPC(t)
	defer conn.Close()

	client := pb.NewBackendClient(conn)
	tok := filepath.Join(modelDir, "tokenizer.gguf")
	tts, _ := filepath.Glob(filepath.Join(modelDir, "vibevoice-realtime-*.gguf"))
	if len(tts) == 0 {
		t.Skip("realtime TTS gguf required")
	}
	resp, err := client.LoadModel(context.Background(), &pb.ModelOptions{
		ModelFile: tts[0],
		Threads:   4,
		Options:   []string{"tokenizer=" + tok},
	})
	if err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("LoadModel returned failure: %s", resp.Message)
	}
}

// TestClosedLoop runs TTS → ASR through the gRPC surface and asserts
// that ≥80 % of the source words come back from the recovered
// transcript, mirroring upstream's tests/test_closed_loop.cpp.
func TestClosedLoop(t *testing.T) {
	modelDir := skipIfNoModel(t)
	hasTTS, _ := filepath.Glob(filepath.Join(modelDir, "vibevoice-realtime-*.gguf"))
	hasASR, _ := filepath.Glob(filepath.Join(modelDir, "vibevoice-asr-*.gguf"))
	if len(hasTTS) == 0 || len(hasASR) == 0 {
		t.Skip("closed-loop needs both realtime TTS and ASR GGUFs")
	}

	tmpDir, err := os.MkdirTemp("", "vibevoice-cpp-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	wav := filepath.Join(tmpDir, "say.wav")

	cmd := startServer(t)
	defer stopServer(cmd)

	conn := dialGRPC(t)
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	// Closed-loop needs the engine loaded with both TTS and ASR roles, so
	// we pass the realtime gguf as ModelFile (default role=tts) and add
	// asr_model + tokenizer + voice as explicit Options.
	tts := hasTTS[0]
	asr := hasASR[0]
	tok := filepath.Join(modelDir, "tokenizer.gguf")
	voiceMatches, _ := filepath.Glob(filepath.Join(modelDir, "voice-*.gguf"))
	voice := ""
	if len(voiceMatches) > 0 {
		voice = voiceMatches[0]
	}
	loadOpts := &pb.ModelOptions{
		ModelFile: tts,
		Threads:   4,
		Options: []string{
			"asr_model=" + asr,
			"tokenizer=" + tok,
		},
	}
	if voice != "" {
		loadOpts.Options = append(loadOpts.Options, "voice="+voice)
	}
	if loadResp, err := client.LoadModel(context.Background(), loadOpts); err != nil || !loadResp.Success {
		t.Fatalf("LoadModel: err=%v success=%v msg=%s", err, loadResp.Success, loadResp.Message)
	}

	srcText := "Hello world this is a test of the synthesis system."
	if _, err := client.TTS(context.Background(), &pb.TTSRequest{
		Text: srcText,
		Dst:  wav,
	}); err != nil {
		t.Fatalf("TTS failed: %v", err)
	}
	if info, err := os.Stat(wav); err != nil || info.Size() < 1000 {
		t.Fatalf("TTS produced no/empty wav at %s (size=%v err=%v)", wav, info, err)
	}

	resp, err := client.AudioTranscription(context.Background(), &pb.TranscriptRequest{
		Dst: wav,
	})
	if err != nil {
		t.Fatalf("AudioTranscription failed: %v", err)
	}
	got := strings.ToLower(resp.Text)
	t.Logf("source     : %s", srcText)
	t.Logf("transcribed: %s", got)

	wordRE := regexp.MustCompile(`[a-z]+`)
	srcWords := wordRE.FindAllString(strings.ToLower(srcText), -1)
	if len(srcWords) == 0 {
		t.Fatal("source has no alphabetic words?")
	}
	hits := 0
	for _, w := range srcWords {
		if strings.Contains(got, w) {
			hits++
		}
	}
	recall := float64(hits) / float64(len(srcWords))
	if recall < 0.80 {
		t.Fatalf("closed-loop recall too low: %d/%d = %.2f%% < 80%%",
			hits, len(srcWords), recall*100)
	}
	t.Logf("closed-loop recall: %d/%d = %.2f%%", hits, len(srcWords), recall*100)
}
