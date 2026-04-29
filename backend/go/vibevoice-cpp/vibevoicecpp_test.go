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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	testAddr    = "localhost:50098"
	startupWait = 5 * time.Second
)

func TestVibevoiceCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VibeVoice-cpp Backend Suite")
}

// modelDirOrSkip returns the staged model bundle dir, or Skip()s the
// current spec when VIBEVOICE_MODEL_DIR is unset / lacks the gguf
// files we need. Tests that don't depend on a model (Locking, error
// paths) don't call this.
func modelDirOrSkip() string {
	dir := os.Getenv("VIBEVOICE_MODEL_DIR")
	if dir == "" {
		Skip("VIBEVOICE_MODEL_DIR not set, skipping model-dependent specs")
	}
	if _, err := os.Stat(filepath.Join(dir, "tokenizer.gguf")); os.IsNotExist(err) {
		Skip("tokenizer.gguf missing in " + dir)
	}
	tts, _ := filepath.Glob(filepath.Join(dir, "vibevoice-realtime-*.gguf"))
	asr, _ := filepath.Glob(filepath.Join(dir, "vibevoice-asr-*.gguf"))
	if len(tts) == 0 && len(asr) == 0 {
		Skip("neither realtime TTS nor ASR gguf found in " + dir)
	}
	return dir
}

// startServer launches the prebuilt backend binary and returns a
// running *exec.Cmd. test.sh ensures `./vibevoice-cpp` is built; if
// it isn't, every gRPC spec is skipped with a clear reason.
func startServer() *exec.Cmd {
	binary := os.Getenv("VIBEVOICE_BINARY")
	if binary == "" {
		binary = "./vibevoice-cpp"
	}
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		Skip("backend binary not found at " + binary)
	}
	cmd := exec.Command(binary, "--addr", testAddr)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	Expect(cmd.Start()).To(Succeed())
	time.Sleep(startupWait)
	return cmd
}

func stopServer(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

func dialGRPC() *grpc.ClientConn {
	conn, err := grpc.Dial(testAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(50*1024*1024),
			grpc.MaxCallSendMsgSize(50*1024*1024),
		),
	)
	Expect(err).ToNot(HaveOccurred())
	return conn
}

var _ = Describe("VibeVoice-cpp", func() {
	Context("backend semantics (no purego load needed)", func() {
		It("is locking - the engine has process-global state", func() {
			Expect((&VibevoiceCpp{}).Locking()).To(BeTrue())
		})

		It("rejects Load with empty ModelFile", func() {
			err := (&VibevoiceCpp{}).Load(&pb.ModelOptions{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ModelFile"))
		})

		It("rejects TTS without a loaded TTS model", func() {
			err := (&VibevoiceCpp{}).TTS(&pb.TTSRequest{
				Text: "no model loaded",
				Dst:  "/tmp/should-not-be-written.wav",
			})
			Expect(err).To(HaveOccurred())
		})

		It("rejects AudioTranscription without a loaded ASR model", func() {
			_, err := (&VibevoiceCpp{}).AudioTranscription(&pb.TranscriptRequest{
				Dst: "/tmp/some.wav",
			})
			Expect(err).To(HaveOccurred())
		})

		It("closes the channel and errors on TTSStream without a loaded model", func() {
			ch := make(chan []byte, 4)
			err := (&VibevoiceCpp{}).TTSStream(&pb.TTSRequest{
				Text: "no model loaded",
				Dst:  "/tmp/should-not-be-written.wav",
			}, ch)
			Expect(err).To(HaveOccurred())
			// Server hangs forever if the channel stays open; this guard
			// is what regresses the e2e DeadlineExceeded we're fixing.
			_, ok := <-ch
			Expect(ok).To(BeFalse(), "TTSStream must close results channel even on error")
		})

		It("closes the channel and errors on AudioTranscriptionStream without a loaded model", func() {
			ch := make(chan *pb.TranscriptStreamResponse, 4)
			err := (&VibevoiceCpp{}).AudioTranscriptionStream(&pb.TranscriptRequest{
				Dst: "/tmp/some.wav",
			}, ch)
			Expect(err).To(HaveOccurred())
			_, ok := <-ch
			Expect(ok).To(BeFalse(), "AudioTranscriptionStream must close results channel even on error")
		})
	})

	Context("gRPC server lifecycle", func() {
		var cmd *exec.Cmd

		AfterEach(func() {
			stopServer(cmd)
			cmd = nil
		})

		It("answers Health checks", func() {
			cmd = startServer()
			conn := dialGRPC()
			defer conn.Close()

			resp, err := pb.NewBackendClient(conn).Health(context.Background(), &pb.HealthMessage{})
			Expect(err).ToNot(HaveOccurred())
			Expect(string(resp.Message)).To(Equal("OK"))
		})

		It("loads the realtime TTS model", func() {
			dir := modelDirOrSkip()
			tts, _ := filepath.Glob(filepath.Join(dir, "vibevoice-realtime-*.gguf"))
			if len(tts) == 0 {
				Skip("realtime TTS gguf missing")
			}

			cmd = startServer()
			conn := dialGRPC()
			defer conn.Close()

			resp, err := pb.NewBackendClient(conn).LoadModel(context.Background(), &pb.ModelOptions{
				ModelFile: tts[0],
				Threads:   4,
				Options:   []string{"tokenizer=" + filepath.Join(dir, "tokenizer.gguf")},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.Success).To(BeTrue(), "LoadModel msg=%q", resp.Message)
		})

		It("runs a closed-loop TTS -> ASR with >=80% word recall", func() {
			dir := modelDirOrSkip()
			tts, _ := filepath.Glob(filepath.Join(dir, "vibevoice-realtime-*.gguf"))
			asr, _ := filepath.Glob(filepath.Join(dir, "vibevoice-asr-*.gguf"))
			if len(tts) == 0 || len(asr) == 0 {
				Skip("closed-loop needs both realtime TTS and ASR ggufs")
			}

			tmpDir, err := os.MkdirTemp("", "vibevoice-cpp-closedloop-*")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tmpDir) })
			wav := filepath.Join(tmpDir, "say.wav")

			cmd = startServer()
			conn := dialGRPC()
			defer conn.Close()
			client := pb.NewBackendClient(conn)

			tok := filepath.Join(dir, "tokenizer.gguf")
			voiceMatches, _ := filepath.Glob(filepath.Join(dir, "voice-*.gguf"))
			loadOpts := &pb.ModelOptions{
				ModelFile: tts[0],
				Threads:   4,
				Options: []string{
					"asr_model=" + asr[0],
					"tokenizer=" + tok,
				},
			}
			if len(voiceMatches) > 0 {
				loadOpts.Options = append(loadOpts.Options, "voice="+voiceMatches[0])
			}
			loadResp, err := client.LoadModel(context.Background(), loadOpts)
			Expect(err).ToNot(HaveOccurred())
			Expect(loadResp.Success).To(BeTrue(), "LoadModel msg=%q", loadResp.Message)

			srcText := "Hello world this is a test of the synthesis system."
			_, err = client.TTS(context.Background(), &pb.TTSRequest{
				Text: srcText,
				Dst:  wav,
			})
			Expect(err).ToNot(HaveOccurred())

			info, err := os.Stat(wav)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Size()).To(BeNumerically(">=", 1000),
				"TTS produced suspiciously small wav (%d bytes)", info.Size())

			resp, err := client.AudioTranscription(context.Background(), &pb.TranscriptRequest{
				Dst: wav,
			})
			Expect(err).ToNot(HaveOccurred())
			got := strings.ToLower(resp.Text)
			GinkgoWriter.Printf("source     : %s\n", srcText)
			GinkgoWriter.Printf("transcribed: %s\n", got)

			wordRE := regexp.MustCompile(`[a-z]+`)
			srcWords := wordRE.FindAllString(strings.ToLower(srcText), -1)
			Expect(srcWords).ToNot(BeEmpty())
			hits := 0
			for _, w := range srcWords {
				if strings.Contains(got, w) {
					hits++
				}
			}
			recall := float64(hits) / float64(len(srcWords))
			GinkgoWriter.Printf("recall: %d/%d = %.2f%%\n", hits, len(srcWords), recall*100)
			Expect(recall).To(BeNumerically(">=", 0.80),
				"closed-loop recall too low: %d/%d = %.2f%%",
				hits, len(srcWords), recall*100)
		})
	})
})
