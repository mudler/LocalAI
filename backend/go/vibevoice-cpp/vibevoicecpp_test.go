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

		// parseOptions + slot fill is the source of the closed-loop CI
		// regression where ModelFile=tts.gguf + Options[asr_model=...]
		// resulted in a load with empty tts slot. These specs assert
		// the slot resolution before we ever call into purego.
		Describe("ModelFile slot resolution", func() {
			It("fills tts slot from ModelFile when only asr_model is in Options", func() {
				v := &VibevoiceCpp{}
				v.modelRoot = "/abs/root"
				role := v.parseOptions([]string{"asr_model=/abs/root/asr.gguf", "tokenizer=/abs/root/tokenizer.gguf"}, v.modelRoot)
				Expect(v.asrModel).To(Equal("/abs/root/asr.gguf"))
				Expect(v.ttsModel).To(BeEmpty())
				Expect(role).To(BeEmpty())
				// Mirror the Load() default-fill block:
				if v.ttsModel == "" {
					v.ttsModel = "/abs/root/tts.gguf"
				}
				Expect(v.ttsModel).To(Equal("/abs/root/tts.gguf"))
				Expect(v.asrModel).To(Equal("/abs/root/asr.gguf"))
			})

			It("fills asr slot from ModelFile when type=asr is set", func() {
				v := &VibevoiceCpp{}
				v.modelRoot = "/abs/root"
				role := v.parseOptions([]string{"type=asr", "tokenizer=/abs/root/tokenizer.gguf"}, v.modelRoot)
				Expect(role).To(Equal("asr"))
				Expect(v.asrModel).To(BeEmpty())
				Expect(v.ttsModel).To(BeEmpty())
			})

			It("respects explicit tts_model override over ModelFile", func() {
				v := &VibevoiceCpp{}
				v.modelRoot = "/abs/root"
				_ = v.parseOptions([]string{"tts_model=/abs/root/alt.gguf"}, v.modelRoot)
				Expect(v.ttsModel).To(Equal("/abs/root/alt.gguf"))
			})

			It("accepts colon-separated options too", func() {
				v := &VibevoiceCpp{}
				v.modelRoot = "/abs/root"
				role := v.parseOptions([]string{"type:asr", "tokenizer:/abs/root/tokenizer.gguf"}, v.modelRoot)
				Expect(role).To(Equal("asr"))
				Expect(v.tokenizer).To(Equal("/abs/root/tokenizer.gguf"))
			})
		})

		// The gallery flow puts everything under <models_dir>/<entry>/,
		// and parameters/options carry paths *relative* to <models_dir>.
		// LocalAI core fills opts.ModelPath = <models_dir>; the backend
		// must resolve every relative path against that root, never CWD.
		Describe("resolvePath (relative-to-modelRoot)", func() {
			It("joins relative path onto relTo", func() {
				Expect(resolvePath("vibevoice-cpp/tokenizer.gguf", "/data/models")).
					To(Equal("/data/models/vibevoice-cpp/tokenizer.gguf"))
			})

			It("passes absolute paths through unchanged", func() {
				Expect(resolvePath("/abs/somewhere/tokenizer.gguf", "/data/models")).
					To(Equal("/abs/somewhere/tokenizer.gguf"))
			})

			It("returns input unchanged when relTo is empty", func() {
				Expect(resolvePath("vibevoice-cpp/tokenizer.gguf", "")).
					To(Equal("vibevoice-cpp/tokenizer.gguf"))
			})

			It("returns empty input unchanged", func() {
				Expect(resolvePath("", "/data/models")).To(BeEmpty())
			})

			It("does not consult CWD - bare filenames stay relative to modelRoot", func() {
				// Even if the test runs in a directory containing a
				// file with this name, the lookup must not fall back
				// to CWD. This is the trap the production gallery flow
				// would otherwise hit when LocalAI is launched from a
				// directory that happens to contain a same-named file.
				prev, _ := os.Getwd()
				DeferCleanup(func() { _ = os.Chdir(prev) })
				tmpCWD, err := os.MkdirTemp("", "vv-cwd-*")
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() { _ = os.RemoveAll(tmpCWD) })
				Expect(os.WriteFile(filepath.Join(tmpCWD, "tokenizer.gguf"),
					[]byte("not the real one"), 0o644)).To(Succeed())
				Expect(os.Chdir(tmpCWD)).To(Succeed())

				got := resolvePath("tokenizer.gguf", "/data/models")
				Expect(got).To(Equal("/data/models/tokenizer.gguf"))
			})
		})

		// Round-trip the gallery layout: relative paths in Options +
		// an absolute ModelFile (as LocalAI core delivers them) end
		// up resolved correctly inside the backend struct.
		It("Load resolves relative Options paths against opts.ModelPath", func() {
			tmpDir, err := os.MkdirTemp("", "vv-relpath-*")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(tmpDir) })

			// Lay out the bundle exactly as the gallery would after install:
			//   <modelpath>/vibevoice-cpp/{tts,tokenizer,voice}.gguf
			subDir := filepath.Join(tmpDir, "vibevoice-cpp")
			Expect(os.MkdirAll(subDir, 0o755)).To(Succeed())
			tts := filepath.Join(subDir, "vibevoice-realtime-stub.gguf")
			tok := filepath.Join(subDir, "tokenizer.gguf")
			voice := filepath.Join(subDir, "voice.gguf")
			for _, p := range []string{tts, tok, voice} {
				Expect(os.WriteFile(p, []byte("stub"), 0o644)).To(Succeed())
			}

			// Mirror Load()'s pre-purego prefix: parse + slot fill.
			v := &VibevoiceCpp{}
			modelFile := tts // core delivers this as an abspath already
			v.modelRoot = tmpDir
			role := v.parseOptions([]string{
				"tokenizer=vibevoice-cpp/tokenizer.gguf",
				"voice=vibevoice-cpp/voice.gguf",
			}, v.modelRoot)
			Expect(role).To(BeEmpty())
			if v.ttsModel == "" {
				v.ttsModel = modelFile
			}

			Expect(v.ttsModel).To(Equal(tts))
			Expect(v.tokenizer).To(Equal(tok))
			Expect(v.voice).To(Equal(voice))
			Expect(v.asrModel).To(BeEmpty())
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
			defer func() { _ = conn.Close() }()

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
			defer func() { _ = conn.Close() }()

			// Mirror the gallery contract: ModelFile is whatever LocalAI
			// core hands us; ModelPath is the models root; Options[]
			// carry paths relative to ModelPath.
			resp, err := pb.NewBackendClient(conn).LoadModel(context.Background(), &pb.ModelOptions{
				ModelFile: filepath.Base(tts[0]),
				ModelPath: dir,
				Threads:   4,
				Options:   []string{"tokenizer=tokenizer.gguf"},
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
			DeferCleanup(func() { _ = os.RemoveAll(tmpDir) })
			wav := filepath.Join(tmpDir, "say.wav")

			cmd = startServer()
			conn := dialGRPC()
			defer func() { _ = conn.Close() }()
			client := pb.NewBackendClient(conn)

			// Gallery convention: ModelPath is the models root, every
			// path inside Options[] is relative to it.
			voiceMatches, _ := filepath.Glob(filepath.Join(dir, "voice-*.gguf"))
			loadOpts := &pb.ModelOptions{
				ModelFile: filepath.Base(tts[0]),
				ModelPath: dir,
				Threads:   4,
				Options: []string{
					"asr_model=" + filepath.Base(asr[0]),
					"tokenizer=tokenizer.gguf",
				},
			}
			if len(voiceMatches) > 0 {
				loadOpts.Options = append(loadOpts.Options, "voice="+filepath.Base(voiceMatches[0]))
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
