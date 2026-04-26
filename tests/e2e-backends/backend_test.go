package e2ebackends_test

import (
	"context"
	"encoding/base64"
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
//	BACKEND_TEST_EXTRA_FILES Pipe-separated list of companion files to download
//	                         next to the main model. Each entry is "<url>" or
//	                         "<url>#<local-name>" (the optional suffix renames
//	                         the file on disk — useful for sherpa-onnx models
//	                         whose loader expects specific names like
//	                         encoder.int8.onnx).
//	BACKEND_TEST_AUDIO_URL   HTTP(S) URL of a sample audio file used by the
//	                         transcription specs.
//	BACKEND_TEST_AUDIO_FILE  Path to an already-available sample audio file.
//	BACKEND_TEST_CAPS        Comma-separated list of capabilities to exercise.
//	                         Supported values: health, load, predict, stream,
//	                         embeddings, tools, transcription, image.
//	                         Defaults to "health,load,predict,stream".
//	                         A backend that only does embeddings would set this to
//	                         "health,load,embeddings"; an image-generation backend
//	                         that cannot be driven by a text prompt can set it to
//	                         "health,load,image".
//	                         "tools" asks the backend to extract a tool call from the
//	                         model output into ChatDelta.tool_calls.
//	                         "image" exercises the GenerateImage RPC and asserts a
//	                         non-empty file is written to the requested dst path.
//	BACKEND_TEST_IMAGE_PROMPT Override the positive prompt for the image spec
//	                         (default: "a photograph of an astronaut riding a horse").
//	BACKEND_TEST_IMAGE_STEPS Override the diffusion step count for the image spec
//	                         (default: 4 — keeps CPU-only runs under a few minutes).
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
//	BACKEND_TEST_TTS_TEXT    Override the text synthesized by the tts/ttsstream
//	                         specs (default: "The quick brown fox jumps over the
//	                         lazy dog.").
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
	capTTS           = "tts"
	capImage         = "image"
	capFaceDetect    = "face_detect"
	capFaceEmbed     = "face_embed"
	capFaceVerify    = "face_verify"
	capFaceAnalyze   = "face_analyze"
	capFaceAntispoof = "face_antispoof"
	capVoiceEmbed    = "voice_embed"
	capVoiceVerify   = "voice_verify"
	capVoiceAnalyze  = "voice_analyze"

	defaultPrompt             = "The capital of France is"
	streamPrompt              = "Once upon a time"
	defaultToolPrompt         = "What's the weather like in Paris, France?"
	defaultToolName           = "get_weather"
	defaultImagePrompt        = "a photograph of an astronaut riding a horse"
	defaultImageSteps         = 4
	defaultVerifyDistanceCeil = float32(0.6) // upper bound for same-person; SFace runs closer to 0.5 ArcFace to 0.35.
	defaultTTSText            = "The quick brown fox jumps over the lazy dog."
)

func defaultCaps() map[string]bool {
	return map[string]bool{
		capHealth:  true,
		capLoad:    true,
		capPredict: true,
		capStream:  true,
	}
}

// splitURLAndName parses a "<url>#<local-name>" entry. The #name suffix is
// optional — if absent, defaultName is returned. Used by the main-model
// and extras download paths so a test can rename downloaded files to the
// shape the backend's loader expects.
func splitURLAndName(entry, defaultName string) (url, name string) {
	if hash := strings.Index(entry, "#"); hash >= 0 {
		return entry[:hash], entry[hash+1:]
	}
	return entry, defaultName
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
		// Face fixtures: two photos of the same person + one different person.
		faceFile1 string
		faceFile2 string
		faceFile3 string
		// Spoof fixture: a photo that the antispoofing model should
		// classify as fake (e.g. printed photo / screen replay). Only
		// exercised when capFaceAntispoof is enabled and the env var
		// is set.
		faceSpoofFile string
		// Voice fixtures: two clips of the same speaker + one different speaker.
		voiceFile1 string
		voiceFile2 string
		voiceFile3 string
		// voiceVerifyCeiling is the upper-bound cosine distance for a
		// same-speaker pair; varies with the recognizer (ECAPA-TDNN
		// runs close to 0.2, WeSpeaker around 0.3).
		voiceVerifyCeiling float32
		// verifyCeiling is the upper-bound cosine distance for a
		// same-person pair; each model configuration can override it via
		// BACKEND_TEST_VERIFY_DISTANCE_CEILING because SFace's distance
		// distribution is wider than ArcFace's.
		verifyCeiling float32
		addr          string
		serverCmd     *exec.Cmd
		conn          *grpc.ClientConn
		client        pb.BackendClient
		prompt        string
		options       []string
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
		// BACKEND_TEST_MODEL_URL accepts an optional "#<local-name>" suffix
		// for cases where the backend expects the model file to have a
		// specific name (e.g. sherpa-onnx's online recognizer finds
		// encoder/decoder/joiner by filename substring).
		if modelFile == "" && modelName == "" {
			url, name := splitURLAndName(modelURL, "model.bin")
			modelFile = filepath.Join(workDir, name)
			downloadFile(url, modelFile)
		}

		// Multi-file models (sherpa-onnx streaming zipformer, sherpa-onnx
		// Omnilingual, any split encoder/decoder/joiner bundle) need
		// companion files next to the main model. BACKEND_TEST_EXTRA_FILES
		// is a pipe-separated list of "<url>[#<local-name>]" entries; each
		// is downloaded into the same directory as modelFile. The optional
		// <local-name> renames the saved file (useful when upstream URLs
		// have stamp/version suffixes the loader doesn't recognise).
		if extraSpec := strings.TrimSpace(os.Getenv("BACKEND_TEST_EXTRA_FILES")); extraSpec != "" && modelFile != "" {
			modelDir := filepath.Dir(modelFile)
			for _, entry := range strings.Split(extraSpec, "|") {
				entry = strings.TrimSpace(entry)
				if entry == "" {
					continue
				}
				url, name := splitURLAndName(entry, filepath.Base(entry))
				downloadFile(url, filepath.Join(modelDir, name))
			}
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

		// Face fixtures for the face-recognition specs.
		faceFile1 = resolveFaceFixture(workDir, "BACKEND_TEST_FACE_IMAGE_1", "face_a_1.jpg")
		faceFile2 = resolveFaceFixture(workDir, "BACKEND_TEST_FACE_IMAGE_2", "face_a_2.jpg")
		faceFile3 = resolveFaceFixture(workDir, "BACKEND_TEST_FACE_IMAGE_3", "face_b.jpg")
		faceSpoofFile = resolveFaceFixture(workDir, "BACKEND_TEST_FACE_SPOOF_IMAGE", "face_spoof.jpg")
		verifyCeiling = envFloat32("BACKEND_TEST_VERIFY_DISTANCE_CEILING", defaultVerifyDistanceCeil)

		// Voice fixtures for the voice-recognition specs. Same resolver
		// as faces — the helper is content-agnostic.
		voiceFile1 = resolveFaceFixture(workDir, "BACKEND_TEST_VOICE_AUDIO_1", "voice_a_1.wav")
		voiceFile2 = resolveFaceFixture(workDir, "BACKEND_TEST_VOICE_AUDIO_2", "voice_a_2.wav")
		voiceFile3 = resolveFaceFixture(workDir, "BACKEND_TEST_VOICE_AUDIO_3", "voice_b.wav")
		voiceVerifyCeiling = envFloat32("BACKEND_TEST_VOICE_VERIFY_DISTANCE_CEILING", 0.4)

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

	It("generates an image via GenerateImage", func() {
		if !caps[capImage] {
			Skip("image capability not enabled")
		}

		imgPrompt := os.Getenv("BACKEND_TEST_IMAGE_PROMPT")
		if imgPrompt == "" {
			imgPrompt = defaultImagePrompt
		}
		steps := envInt32("BACKEND_TEST_IMAGE_STEPS", defaultImageSteps)

		dst := filepath.Join(workDir, "generated.png")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		res, err := client.GenerateImage(ctx, &pb.GenerateImageRequest{
			PositivePrompt: imgPrompt,
			NegativePrompt: "",
			Width:          512,
			Height:         512,
			Step:           steps,
			Seed:           42,
			Dst:            dst,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.GetSuccess()).To(BeTrue(), "GenerateImage failed: %s", res.GetMessage())

		info, err := os.Stat(dst)
		Expect(err).NotTo(HaveOccurred(), "GenerateImage did not write a file at %s", dst)
		Expect(info.Size()).To(BeNumerically(">", int64(0)),
			"GenerateImage wrote an empty file at %s", dst)
		GinkgoWriter.Printf("GenerateImage: wrote %s (%d bytes)\n", dst, info.Size())
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

	// ─── face recognition specs ─────────────────────────────────────────

	It("detects faces via Detect", func() {
		if !caps[capFaceDetect] {
			Skip("face_detect capability not enabled")
		}
		Expect(faceFile1).NotTo(BeEmpty(), "BACKEND_TEST_FACE_IMAGE_1_FILE or _URL must be set")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := client.Detect(ctx, &pb.DetectOptions{Src: base64File(faceFile1)})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.GetDetections()).NotTo(BeEmpty(), "Detect returned no faces")
		for _, d := range res.GetDetections() {
			Expect(d.GetClassName()).To(Equal("face"))
			Expect(d.GetWidth()).To(BeNumerically(">", 0))
			Expect(d.GetHeight()).To(BeNumerically(">", 0))
		}
		GinkgoWriter.Printf("face_detect: %d faces\n", len(res.GetDetections()))
	})

	It("produces face embeddings via Embedding", func() {
		if !caps[capFaceEmbed] {
			Skip("face_embed capability not enabled")
		}
		Expect(faceFile1).NotTo(BeEmpty(), "BACKEND_TEST_FACE_IMAGE_1_FILE or _URL must be set")

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := client.Embedding(ctx, &pb.PredictOptions{Images: []string{base64File(faceFile1)}})
		Expect(err).NotTo(HaveOccurred())
		vec := res.GetEmbeddings()
		Expect(vec).NotTo(BeEmpty(), "Embedding returned empty vector")
		// Face embeddings are L2-normalized — expect unit norm.
		var sumSq float64
		for _, v := range vec {
			sumSq += float64(v) * float64(v)
		}
		Expect(sumSq).To(BeNumerically("~", 1.0, 0.05),
			"face embedding should be L2-normed (sum(x^2)=%.3f, dim=%d)", sumSq, len(vec))
		GinkgoWriter.Printf("face_embed: dim=%d\n", len(vec))
	})

	It("verifies faces via FaceVerify", func() {
		if !caps[capFaceVerify] {
			Skip("face_verify capability not enabled")
		}
		Expect(faceFile1).NotTo(BeEmpty(), "BACKEND_TEST_FACE_IMAGE_1_FILE or _URL must be set")

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Same image twice — expected verified=true with very small distance.
		b1 := base64File(faceFile1)
		same, err := client.FaceVerify(ctx, &pb.FaceVerifyRequest{Img1: b1, Img2: b1, Threshold: verifyCeiling})
		Expect(err).NotTo(HaveOccurred())
		Expect(same.GetVerified()).To(BeTrue(), "same image should verify: dist=%.3f", same.GetDistance())
		Expect(same.GetDistance()).To(BeNumerically("<", 0.1))
		GinkgoWriter.Printf("face_verify(same): dist=%.3f confidence=%.1f\n", same.GetDistance(), same.GetConfidence())

		// Different images — assert relative ordering when the detector
		// actually finds a face in both images. Some fixtures (masked
		// faces, profile shots, etc.) are legitimately borderline for
		// SCRFD's default threshold, so we don't fail the suite when the
		// second image gets a NotFound — we just log and skip the
		// cross-person check. The same-image assertion above is the
		// definitive proof the RPC works end-to-end.
		if faceFile3 != "" {
			b3 := base64File(faceFile3)
			diff, err := client.FaceVerify(ctx, &pb.FaceVerifyRequest{Img1: b1, Img2: b3, Threshold: verifyCeiling})
			if err != nil {
				GinkgoWriter.Printf("face_verify(diff): skipped — %v\n", err)
			} else {
				Expect(diff.GetDistance()).To(BeNumerically(">", same.GetDistance()),
					"cross-person distance %.3f should exceed same-image distance %.3f", diff.GetDistance(), same.GetDistance())
				GinkgoWriter.Printf("face_verify(diff): dist=%.3f verified=%v\n", diff.GetDistance(), diff.GetVerified())
			}
		}

		// If two photos of the same person were provided, the ordering
		// should also hold: d(a1,a2) < ceiling. Best-effort as above —
		// skip if the detector doesn't find a face in the second image.
		if faceFile2 != "" {
			b2 := base64File(faceFile2)
			sp, err := client.FaceVerify(ctx, &pb.FaceVerifyRequest{Img1: b1, Img2: b2, Threshold: verifyCeiling})
			if err != nil {
				GinkgoWriter.Printf("face_verify(same-person): skipped — %v\n", err)
			} else {
				Expect(sp.GetDistance()).To(BeNumerically("<", verifyCeiling),
					"same-person (different photos) distance %.3f exceeds ceiling %.3f", sp.GetDistance(), verifyCeiling)
				GinkgoWriter.Printf("face_verify(same-person): dist=%.3f verified=%v\n", sp.GetDistance(), sp.GetVerified())
			}
		}

		// Liveness: exercise BOTH real and spoof paths when the cap is
		// enabled. Gated on capFaceAntispoof so model configs without
		// MiniFASNet weights (which would correctly surface
		// FAILED_PRECONDITION) can still run the rest of the verify
		// spec.
		if caps[capFaceAntispoof] {
			// (a) Real-face path: same image twice → both is_real=true,
			// verified stays true, scores populated.
			asReal, err := client.FaceVerify(ctx, &pb.FaceVerifyRequest{
				Img1: b1, Img2: b1, Threshold: verifyCeiling, AntiSpoofing: true,
			})
			Expect(err).NotTo(HaveOccurred(), "FaceVerify(anti_spoofing=true, real) failed")
			Expect(asReal.GetImg1IsReal()).To(BeTrue(), "real face should be is_real=true (score=%.3f)", asReal.GetImg1AntispoofScore())
			Expect(asReal.GetImg2IsReal()).To(BeTrue(), "real face should be is_real=true (score=%.3f)", asReal.GetImg2AntispoofScore())
			Expect(asReal.GetImg1AntispoofScore()).To(BeNumerically(">", 0), "img1_antispoof_score must be populated")
			Expect(asReal.GetImg2AntispoofScore()).To(BeNumerically(">", 0), "img2_antispoof_score must be populated")
			Expect(asReal.GetVerified()).To(BeTrue(), "same image + real face should still verify with liveness on")
			GinkgoWriter.Printf("face_antispoof(verify,real): img1_score=%.3f img2_score=%.3f\n",
				asReal.GetImg1AntispoofScore(), asReal.GetImg2AntispoofScore())

			// (b) Spoof path: img2 is a known-spoof fixture → img2
			// classified as fake, liveness veto forces verified=false
			// even though img1 vs img2 similarity isn't tested (could
			// match or not). Skipped if no spoof fixture was provided,
			// since a synthetic spoof is not a reliable assertion.
			if faceSpoofFile != "" {
				bSpoof := base64File(faceSpoofFile)
				asFake, err := client.FaceVerify(ctx, &pb.FaceVerifyRequest{
					Img1: b1, Img2: bSpoof, Threshold: verifyCeiling, AntiSpoofing: true,
				})
				Expect(err).NotTo(HaveOccurred(), "FaceVerify(anti_spoofing=true, spoof img2) failed")
				Expect(asFake.GetImg1IsReal()).To(BeTrue(), "img1 (real) should still be is_real=true")
				Expect(asFake.GetImg2IsReal()).To(BeFalse(), "spoof fixture must classify as is_real=false (score=%.3f)", asFake.GetImg2AntispoofScore())
				Expect(asFake.GetVerified()).To(BeFalse(), "failed liveness on img2 must force verified=false regardless of similarity")
				GinkgoWriter.Printf("face_antispoof(verify,spoof): img1_score=%.3f img2_score=%.3f verified=%v\n",
					asFake.GetImg1AntispoofScore(), asFake.GetImg2AntispoofScore(), asFake.GetVerified())
			}
		}
	})

	It("analyzes faces via FaceAnalyze", func() {
		if !caps[capFaceAnalyze] {
			Skip("face_analyze capability not enabled")
		}
		Expect(faceFile1).NotTo(BeEmpty(), "BACKEND_TEST_FACE_IMAGE_1_FILE or _URL must be set")

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := client.FaceAnalyze(ctx, &pb.FaceAnalyzeRequest{Img: base64File(faceFile1)})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.GetFaces()).NotTo(BeEmpty(), "FaceAnalyze returned no faces")
		for _, f := range res.GetFaces() {
			Expect(f.GetFaceConfidence()).To(BeNumerically(">", 0))
			Expect(f.GetAge()).To(BeNumerically(">", 0), "age should be populated by analyze-capable engines")
			Expect(f.GetDominantGender()).To(BeElementOf("Man", "Woman"))
		}
		GinkgoWriter.Printf("face_analyze: %d faces\n", len(res.GetFaces()))

		// Liveness: exercise BOTH real and spoof paths. Gated on
		// capFaceAntispoof.
		if caps[capFaceAntispoof] {
			// (a) Real: every face on the real-face fixture must
			// classify as is_real=true with a non-zero score.
			asReal, err := client.FaceAnalyze(ctx, &pb.FaceAnalyzeRequest{
				Img: base64File(faceFile1), AntiSpoofing: true,
			})
			Expect(err).NotTo(HaveOccurred(), "FaceAnalyze(anti_spoofing=true, real) failed")
			Expect(asReal.GetFaces()).NotTo(BeEmpty())
			for _, f := range asReal.GetFaces() {
				Expect(f.GetIsReal()).To(BeTrue(), "real-face fixture must classify as is_real=true (score=%.3f)", f.GetAntispoofScore())
				Expect(f.GetAntispoofScore()).To(BeNumerically(">", 0), "antispoof_score must be populated")
			}
			GinkgoWriter.Printf("face_antispoof(analyze,real): %d faces\n", len(asReal.GetFaces()))

			// (b) Spoof: at least one detected face on the spoof
			// fixture must classify as is_real=false. Skipped if no
			// spoof fixture was provided.
			if faceSpoofFile != "" {
				asFake, err := client.FaceAnalyze(ctx, &pb.FaceAnalyzeRequest{
					Img: base64File(faceSpoofFile), AntiSpoofing: true,
				})
				Expect(err).NotTo(HaveOccurred(), "FaceAnalyze(anti_spoofing=true, spoof) failed")
				Expect(asFake.GetFaces()).NotTo(BeEmpty(), "detector must find a face in the spoof fixture")
				sawFake := false
				for _, f := range asFake.GetFaces() {
					if !f.GetIsReal() {
						sawFake = true
					}
					GinkgoWriter.Printf("face_antispoof(analyze,spoof): is_real=%v score=%.3f\n", f.GetIsReal(), f.GetAntispoofScore())
				}
				Expect(sawFake).To(BeTrue(), "known spoof fixture must produce at least one is_real=false face")
			}
		}
	})

	// ─── voice (speaker) recognition specs ──────────────────────────────

	It("produces speaker embeddings via VoiceEmbed", func() {
		if !caps[capVoiceEmbed] {
			Skip("voice_embed capability not enabled")
		}
		Expect(voiceFile1).NotTo(BeEmpty(), "BACKEND_TEST_VOICE_AUDIO_1_FILE or _URL must be set")

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := client.VoiceEmbed(ctx, &pb.VoiceEmbedRequest{Audio: voiceFile1})
		Expect(err).NotTo(HaveOccurred())
		vec := res.GetEmbedding()
		Expect(vec).NotTo(BeEmpty(), "VoiceEmbed returned empty vector")
		GinkgoWriter.Printf("voice_embed: dim=%d\n", len(vec))
	})

	It("verifies speakers via VoiceVerify", func() {
		if !caps[capVoiceVerify] {
			Skip("voice_verify capability not enabled")
		}
		Expect(voiceFile1).NotTo(BeEmpty(), "BACKEND_TEST_VOICE_AUDIO_1_FILE or _URL must be set")

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Same clip twice — expected verified=true with very small distance.
		same, err := client.VoiceVerify(ctx, &pb.VoiceVerifyRequest{
			Audio1: voiceFile1, Audio2: voiceFile1, Threshold: voiceVerifyCeiling,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(same.GetVerified()).To(BeTrue(), "same clip should verify: dist=%.3f", same.GetDistance())
		Expect(same.GetDistance()).To(BeNumerically("<", 0.05),
			"identical-clip distance should be near zero, got %.3f", same.GetDistance())
		GinkgoWriter.Printf("voice_verify(same): dist=%.3f confidence=%.1f\n", same.GetDistance(), same.GetConfidence())

		// Cross-pair distance — assert relative ordering: d(file1,file3) > d(same).
		// We don't require the fixtures to contain true same-speaker pairs —
		// good same-speaker audio is hard to source un-gated. The RPC
		// correctness is pinned by the same-clip check above; the pair
		// distances here are about asserting the embedding actually encodes
		// speaker info (ordering changes with speaker identity).
		var d12, d13 float32
		if voiceFile3 != "" {
			res, err := client.VoiceVerify(ctx, &pb.VoiceVerifyRequest{
				Audio1: voiceFile1, Audio2: voiceFile3, Threshold: voiceVerifyCeiling,
			})
			if err != nil {
				GinkgoWriter.Printf("voice_verify(1vs3): skipped — %v\n", err)
			} else {
				d13 = res.GetDistance()
				Expect(d13).To(BeNumerically(">", same.GetDistance()),
					"cross-clip distance %.3f should exceed same-clip distance %.3f", d13, same.GetDistance())
				GinkgoWriter.Printf("voice_verify(1vs3): dist=%.3f verified=%v\n", d13, res.GetVerified())
			}
		}

		if voiceFile2 != "" {
			res, err := client.VoiceVerify(ctx, &pb.VoiceVerifyRequest{
				Audio1: voiceFile1, Audio2: voiceFile2, Threshold: voiceVerifyCeiling,
			})
			if err != nil {
				GinkgoWriter.Printf("voice_verify(1vs2): skipped — %v\n", err)
			} else {
				d12 = res.GetDistance()
				Expect(d12).To(BeNumerically(">", same.GetDistance()),
					"cross-clip distance %.3f should exceed same-clip distance %.3f", d12, same.GetDistance())
				GinkgoWriter.Printf("voice_verify(1vs2): dist=%.3f verified=%v\n", d12, res.GetVerified())
			}
		}

		// If both pair distances were computed, record their ordering.
		// We log rather than assert: ordering depends on the specific
		// fixtures used, and CI defaults point at three different speakers.
		if d12 > 0 && d13 > 0 {
			GinkgoWriter.Printf("voice_verify ordering: d(1,2)=%.3f d(1,3)=%.3f\n", d12, d13)
		}
	})

	It("analyzes voice via VoiceAnalyze", func() {
		if !caps[capVoiceAnalyze] {
			Skip("voice_analyze capability not enabled")
		}
		Expect(voiceFile1).NotTo(BeEmpty(), "BACKEND_TEST_VOICE_AUDIO_1_FILE or _URL must be set")

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := client.VoiceAnalyze(ctx, &pb.VoiceAnalyzeRequest{Audio: voiceFile1})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.GetSegments()).NotTo(BeEmpty(), "VoiceAnalyze returned no segments")
		for _, s := range res.GetSegments() {
			Expect(s.GetAge()).To(BeNumerically(">", 0), "age should be populated by analyze-capable engines")
			// Audeering's age-gender head outputs female / male / child;
			// LocalAI capitalises those to Female / Male / Child. Custom
			// checkpoints wired via the age_gender_model option may use
			// different labels, so accept anything non-empty.
			Expect(s.GetDominantGender()).NotTo(BeEmpty())
		}
		GinkgoWriter.Printf("voice_analyze: %d segments\n", len(res.GetSegments()))
	})

	It("synthesizes speech via TTS", func() {
		if !caps[capTTS] {
			Skip("tts capability not enabled")
		}
		text := os.Getenv("BACKEND_TEST_TTS_TEXT")
		if text == "" {
			text = defaultTTSText
		}
		dst := filepath.Join(workDir, "tts.wav")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, err := client.TTS(ctx, &pb.TTSRequest{Text: text, Dst: dst})
		Expect(err).NotTo(HaveOccurred())

		info, err := os.Stat(dst)
		Expect(err).NotTo(HaveOccurred(), "TTS did not write a file at %s", dst)
		Expect(info.Size()).To(BeNumerically(">", int64(1024)),
			"TTS output too small: %d bytes", info.Size())
		GinkgoWriter.Printf("TTS: wrote %s (%d bytes)\n", dst, info.Size())
	})

	It("streams PCM via TTSStream", func() {
		if !caps[capTTS] {
			Skip("tts capability not enabled")
		}
		text := os.Getenv("BACKEND_TEST_TTS_TEXT")
		if text == "" {
			text = defaultTTSText
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		stream, err := client.TTSStream(ctx, &pb.TTSRequest{Text: text})
		Expect(err).NotTo(HaveOccurred())

		var chunks, totalBytes int
		for {
			reply, err := stream.Recv()
			if err == io.EOF {
				break
			}
			Expect(err).NotTo(HaveOccurred())
			if audio := reply.GetAudio(); len(audio) > 0 {
				chunks++
				totalBytes += len(audio)
			}
		}
		// Header + at least one PCM chunk proves real streaming (not emit-once).
		Expect(chunks).To(BeNumerically(">=", 2),
			"expected >=2 chunks (header + PCM), got %d (bytes=%d)", chunks, totalBytes)
		Expect(totalBytes).To(BeNumerically(">", 1024),
			"streamed audio too short: %d bytes", totalBytes)
		GinkgoWriter.Printf("TTSStream: %d chunks, %d bytes\n", chunks, totalBytes)
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
// Retry flags guard against transient CI network hiccups (github.com in
// particular has been flaky from GHA runners, timing out TCP connects).
func downloadFile(url, dest string) {
	GinkgoHelper()
	cmd := exec.Command("curl", "-sSfL",
		"--connect-timeout", "30",
		"--max-time", "600",
		"--retry", "5",
		"--retry-delay", "5",
		"--retry-all-errors",
		"-o", dest, url)
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

func envFloat32(name string, def float32) float32 {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	var v float32
	if _, err := fmt.Sscanf(raw, "%f", &v); err != nil {
		return def
	}
	return v
}

// resolveFaceFixture returns the local path of a face-fixture image,
// preferring BACKEND_TEST_<prefix>_FILE when set and otherwise
// downloading BACKEND_TEST_<prefix>_URL into workDir. Returns an empty
// string when neither is configured — specs that need it should skip.
func resolveFaceFixture(workDir, prefix, defaultName string) string {
	if path := os.Getenv(prefix + "_FILE"); path != "" {
		return path
	}
	url := os.Getenv(prefix + "_URL")
	if url == "" {
		return ""
	}
	dest := filepath.Join(workDir, defaultName)
	downloadFile(url, dest)
	return dest
}

// base64File reads a file and returns its base64 encoding.
func base64File(path string) string {
	GinkgoHelper()
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), "reading %s", path)
	return base64.StdEncoding.EncodeToString(data)
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
