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
//	BACKEND_TEST_MODEL_NAME  HuggingFace model id (e.g. "Qwen/Qwen2.5-0.5B-Instruct").
//	                         Passed verbatim as ModelOptions.Model; backends like vllm
//	                         resolve it themselves and no local file is downloaded.
//
// Optional:
//
//	BACKEND_TEST_MMPROJ_URL  HTTP(S) URL of an mmproj file (audio/vision encoder)
//	                         to download alongside the main model — required for
//	                         multimodal models like Qwen3-ASR-0.6B-GGUF.
//	BACKEND_TEST_MMPROJ_FILE Path to an already-available mmproj file.
//	BACKEND_TEST_AUDIO_URL   HTTP(S) URL of a sample audio file used by the
//	                         transcription specs.
//	BACKEND_TEST_AUDIO_FILE  Path to an already-available sample audio file.
//	BACKEND_TEST_CAPS        Comma-separated list of capabilities to exercise.
//	                         Supported values: health, load, predict, stream,
//	                         embeddings, tools, transcription.
//	                         Defaults to "health,load,predict,stream".
//	                         A backend that only does embeddings would set this to
//	                         "health,load,embeddings"; an image/TTS backend that cannot
//	                         be driven by a text prompt can set it to "health,load".
//	                         "tools" asks the backend to extract a tool call from the
//	                         model output into ChatDelta.tool_calls.
//	BACKEND_TEST_PROMPT      Override the prompt used by predict/stream specs.
//	BACKEND_TEST_CTX_SIZE    Override the context size passed to LoadModel (default 512).
//	BACKEND_TEST_THREADS     Override Threads passed to LoadModel (default 4).
//	BACKEND_TEST_OPTIONS     Comma-separated Options[] entries passed to LoadModel,
//	                         e.g. "tool_parser:hermes,reasoning_parser:qwen3".
//	BACKEND_TEST_CACHE_TYPE_K Sets ModelOptions.CacheTypeKey (llama.cpp -ctk),
//	                         e.g. "q8_0" — exercises KV-cache quantization code paths.
//	BACKEND_TEST_CACHE_TYPE_V Sets ModelOptions.CacheTypeValue (llama.cpp -ctv).
//	BACKEND_TEST_TOOL_PROMPT Override the user prompt for the tools spec
//	                         (default: "What's the weather like in Paris, France?").
//	BACKEND_TEST_TOOL_NAME   Override the function name expected in the tool call
//	                         (default: "get_weather").
//
// The suite is intentionally model-format-agnostic: it only ever passes the
// file path to LoadModel, so GGUF, ONNX, safetensors, .bin etc. all work so
// long as the backend under test accepts that format.
const (
	capHealth        = "health"
	capLoad          = "load"
	capPredict       = "predict"
	capStream        = "stream"
	capEmbeddings    = "embeddings"
	capTools         = "tools"
	capTranscription = "transcription"

	defaultPrompt     = "The capital of France is"
	streamPrompt      = "Once upon a time"
	defaultToolPrompt = "What's the weather like in Paris, France?"
	defaultToolName   = "get_weather"
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
		caps       map[string]bool
		workDir    string
		binaryDir  string
		modelFile  string // set when a local file is used
		modelName  string // set when a HuggingFace model id is used
		mmprojFile string // optional multimodal projector
		audioFile  string // optional audio fixture for transcription specs
		addr       string
		serverCmd  *exec.Cmd
		conn       *grpc.ClientConn
		client     pb.BackendClient
		prompt     string
		options    []string
	)

	BeforeAll(func() {
		image := os.Getenv("BACKEND_IMAGE")
		Expect(image).NotTo(BeEmpty(), "BACKEND_IMAGE env var must be set (e.g. local-ai-backend:llama-cpp)")

		modelURL := os.Getenv("BACKEND_TEST_MODEL_URL")
		modelFile = os.Getenv("BACKEND_TEST_MODEL_FILE")
		modelName = os.Getenv("BACKEND_TEST_MODEL_NAME")
		Expect(modelURL != "" || modelFile != "" || modelName != "").To(BeTrue(),
			"one of BACKEND_TEST_MODEL_URL, BACKEND_TEST_MODEL_FILE, or BACKEND_TEST_MODEL_NAME must be set")

		caps = parseCaps()
		GinkgoWriter.Printf("Testing image=%q with capabilities=%v\n", image, keys(caps))

		prompt = os.Getenv("BACKEND_TEST_PROMPT")
		if prompt == "" {
			prompt = defaultPrompt
		}

		if raw := strings.TrimSpace(os.Getenv("BACKEND_TEST_OPTIONS")); raw != "" {
			for _, opt := range strings.Split(raw, ",") {
				opt = strings.TrimSpace(opt)
				if opt != "" {
					options = append(options, opt)
				}
			}
		}

		var err error
		workDir, err = os.MkdirTemp("", "backend-e2e-*")
		Expect(err).NotTo(HaveOccurred())

		// Extract the image filesystem so we can run run.sh directly.
		binaryDir = filepath.Join(workDir, "rootfs")
		Expect(os.MkdirAll(binaryDir, 0o755)).To(Succeed())
		extractImage(image, binaryDir)
		Expect(filepath.Join(binaryDir, "run.sh")).To(BeAnExistingFile())

		// Download the model once if not provided and no HF name given.
		if modelFile == "" && modelName == "" {
			modelFile = filepath.Join(workDir, "model.bin")
			downloadFile(modelURL, modelFile)
		}

		// Multimodal projector (mmproj): required by audio/vision-capable
		// llama.cpp models like Qwen3-ASR-0.6B-GGUF. Either file or URL.
		mmprojFile = os.Getenv("BACKEND_TEST_MMPROJ_FILE")
		if mmprojFile == "" {
			if url := os.Getenv("BACKEND_TEST_MMPROJ_URL"); url != "" {
				mmprojFile = filepath.Join(workDir, "mmproj.bin")
				downloadFile(url, mmprojFile)
			}
		}

		// Audio fixture for the transcription specs.
		audioFile = os.Getenv("BACKEND_TEST_AUDIO_FILE")
		if audioFile == "" {
			if url := os.Getenv("BACKEND_TEST_AUDIO_URL"); url != "" {
				audioFile = filepath.Join(workDir, "sample.wav")
				downloadFile(url, audioFile)
			}
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

		// Prefer a HuggingFace model id when provided (e.g. for vllm);
		// otherwise fall back to a downloaded/local file path.
		modelRef := modelFile
		var modelPath string
		if modelName != "" {
			modelRef = modelName
		} else {
			modelPath = modelFile
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		res, err := client.LoadModel(ctx, &pb.ModelOptions{
			Model:          modelRef,
			ModelFile:      modelPath,
			ContextSize:    ctxSize,
			Threads:        threads,
			NGPULayers:     0,
			MMap:           true,
			NBatch:         128,
			Options:        options,
			MMProj:         mmprojFile,
			CacheTypeKey:   os.Getenv("BACKEND_TEST_CACHE_TYPE_K"),
			CacheTypeValue: os.Getenv("BACKEND_TEST_CACHE_TYPE_V"),
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

	It("extracts tool calls into ChatDelta", func() {
		if !caps[capTools] {
			Skip("tools capability not enabled")
		}

		toolPrompt := os.Getenv("BACKEND_TEST_TOOL_PROMPT")
		if toolPrompt == "" {
			toolPrompt = defaultToolPrompt
		}
		toolName := os.Getenv("BACKEND_TEST_TOOL_NAME")
		if toolName == "" {
			toolName = defaultToolName
		}

		toolsJSON := fmt.Sprintf(`[{
			"type": "function",
			"function": {
				"name": %q,
				"description": "Get the current weather for a location",
				"parameters": {
					"type": "object",
					"properties": {
						"location": {
							"type": "string",
							"description": "The city and state, e.g. San Francisco, CA"
						}
					},
					"required": ["location"]
				}
			}
		}]`, toolName)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := client.Predict(ctx, &pb.PredictOptions{
			Messages: []*pb.Message{
				{Role: "system", Content: "You are a helpful assistant. Use the provided tool when the user asks about weather."},
				{Role: "user", Content: toolPrompt},
			},
			Tools:                toolsJSON,
			ToolChoice:           "auto",
			UseTokenizerTemplate: true,
			Tokens:               200,
			Temperature:          0.1,
		})
		Expect(err).NotTo(HaveOccurred())

		// Collect tool calls from every delta — some backends emit a single
		// final delta, others stream incremental pieces in one Reply.
		var toolCalls []*pb.ToolCallDelta
		for _, delta := range res.GetChatDeltas() {
			toolCalls = append(toolCalls, delta.GetToolCalls()...)
		}

		GinkgoWriter.Printf("Tool call: raw=%q deltas=%d tool_calls=%d\n",
			string(res.GetMessage()), len(res.GetChatDeltas()), len(toolCalls))

		Expect(toolCalls).NotTo(BeEmpty(),
			"Predict did not return any ToolCallDelta. raw=%q", string(res.GetMessage()))

		matched := false
		for _, tc := range toolCalls {
			GinkgoWriter.Printf("  - idx=%d id=%q name=%q args=%q\n",
				tc.GetIndex(), tc.GetId(), tc.GetName(), tc.GetArguments())
			if tc.GetName() == toolName {
				matched = true
			}
		}
		Expect(matched).To(BeTrue(),
			"Expected a tool call named %q in ChatDelta.tool_calls", toolName)
	})

	It("transcribes audio via AudioTranscription", func() {
		if !caps[capTranscription] {
			Skip("transcription capability not enabled")
		}
		Expect(audioFile).NotTo(BeEmpty(),
			"BACKEND_TEST_AUDIO_FILE or BACKEND_TEST_AUDIO_URL must be set when transcription cap is enabled")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := client.AudioTranscription(ctx, &pb.TranscriptRequest{
			Dst:         audioFile,
			Threads:     uint32(envInt32("BACKEND_TEST_THREADS", 4)),
			Temperature: 0.0,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(res.GetText())).NotTo(BeEmpty(),
			"AudioTranscription returned empty text")
		GinkgoWriter.Printf("AudioTranscription: text=%q language=%q duration=%v\n",
			res.GetText(), res.GetLanguage(), res.GetDuration())
	})

	It("streams audio transcription via AudioTranscriptionStream", func() {
		if !caps[capTranscription] {
			Skip("transcription capability not enabled")
		}
		Expect(audioFile).NotTo(BeEmpty(),
			"BACKEND_TEST_AUDIO_FILE or BACKEND_TEST_AUDIO_URL must be set when transcription cap is enabled")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		stream, err := client.AudioTranscriptionStream(ctx, &pb.TranscriptRequest{
			Dst:         audioFile,
			Threads:     uint32(envInt32("BACKEND_TEST_THREADS", 4)),
			Temperature: 0.0,
			Stream:      true,
		})
		Expect(err).NotTo(HaveOccurred())

		var deltas []string
		var assembled strings.Builder
		var finalText string
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				break
			}
			Expect(err).NotTo(HaveOccurred())
			if d := chunk.GetDelta(); d != "" {
				deltas = append(deltas, d)
				assembled.WriteString(d)
			}
			if final := chunk.GetFinalResult(); final != nil && final.GetText() != "" {
				finalText = final.GetText()
			}
		}
		// At least one of: a delta arrived, or the final event carried text.
		Expect(deltas).NotTo(BeEmpty(),
			"AudioTranscriptionStream did not emit any deltas (assembled=%q final=%q)",
			assembled.String(), finalText)

		// If both arrived, the final event should match the assembled deltas.
		if finalText != "" && assembled.Len() > 0 {
			Expect(finalText).To(Equal(assembled.String()),
				"final transcript should match concatenated deltas")
		}
		GinkgoWriter.Printf("AudioTranscriptionStream: deltas=%d assembled=%q final=%q\n",
			len(deltas), assembled.String(), finalText)
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
