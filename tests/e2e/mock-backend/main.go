package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

// MockBackend implements the Backend gRPC service with mocked responses.
// When tools are present but the prompt already contains MCP tool results
// (indicated by the marker from the mock MCP server), it returns a plain
// text response instead of another tool call, letting the MCP loop complete.
type MockBackend struct {
	pb.UnimplementedBackendServer
}

// promptHasToolResults checks if the prompt contains evidence of prior tool
// execution — specifically the output from the mock MCP server's get_weather tool.
func promptHasToolResults(prompt string) bool {
	return strings.Contains(prompt, "Weather in")
}

func (m *MockBackend) Health(ctx context.Context, in *pb.HealthMessage) (*pb.Reply, error) {
	xlog.Debug("Health check called")
	return &pb.Reply{Message: []byte("OK")}, nil
}

func (m *MockBackend) LoadModel(ctx context.Context, in *pb.ModelOptions) (*pb.Result, error) {
	xlog.Debug("LoadModel called", "model", in.Model)
	return &pb.Result{
		Message: "Model loaded successfully (mocked)",
		Success: true,
	}, nil
}

func (m *MockBackend) Predict(ctx context.Context, in *pb.PredictOptions) (*pb.Reply, error) {
	xlog.Debug("Predict called", "prompt", in.Prompt)
	if strings.Contains(in.Prompt, "MOCK_ERROR") {
		return nil, fmt.Errorf("mock backend predict error: simulated failure")
	}

	// Simulate C++ autoparser: tool call via ChatDeltas, empty message
	if strings.Contains(in.Prompt, "AUTOPARSER_TOOL_CALL") {
		toolName := mockToolNameFromRequest(in)
		if toolName == "" {
			toolName = "search_collections"
		}
		return &pb.Reply{
			Message:      []byte{},
			Tokens:       10,
			PromptTokens: 5,
			ChatDeltas: []*pb.ChatDelta{
				{ReasoningContent: "I need to search for information."},
				{
					ToolCalls: []*pb.ToolCallDelta{
						{
							Index:     0,
							Id:        "call_mock_123",
							Name:      toolName,
							Arguments: `{"query":"localai"}`,
						},
					},
				},
			},
		}, nil
	}

	// Simulate C++ autoparser: content via ChatDeltas, empty message
	if strings.Contains(in.Prompt, "AUTOPARSER_CONTENT") {
		return &pb.Reply{
			Message:      []byte{},
			Tokens:       10,
			PromptTokens: 5,
			ChatDeltas: []*pb.ChatDelta{
				{ReasoningContent: "Let me compose a response."},
				{Content: "LocalAI is an open-source AI platform."},
			},
		}, nil
	}

	// Simulate Gemma 4 / thinking model with C++ autoparser:
	// - Message contains the clean content (autoparser extracts it from OAI choices[0].message.content)
	// - ChatDeltas contain both reasoning and content separately
	// This reproduces the bug where Go-side PrependThinkingTokenIfNeeded
	// incorrectly prepends a thinking start token to the clean content,
	// causing the entire response to be classified as unclosed reasoning.
	if strings.Contains(in.Prompt, "AUTOPARSER_THINKING_CONTENT") {
		return &pb.Reply{
			Message:      []byte("I am a helpful AI assistant designed to assist you with a wide range of tasks."),
			Tokens:       20,
			PromptTokens: 50,
			ChatDeltas: []*pb.ChatDelta{
				{
					ReasoningContent: "The user is asking a simple introductory question. I should respond directly.",
					Content:          "I am a helpful AI assistant designed to assist you with a wide range of tasks.",
				},
			},
		}, nil
	}

	// Simulate multiple tool calls in a single response (Go-side JSON parser path).
	if strings.Contains(in.Prompt, "MULTI_TOOL_CALL") {
		return &pb.Reply{
			Message:      []byte(`{"name": "get_weather", "arguments": {"location": "Rome"}}
{"name": "get_weather", "arguments": {"location": "Paris"}}`),
			Tokens:       30,
			PromptTokens: 10,
		}, nil
	}
	var response string
	toolName := mockToolNameFromRequest(in)
	if toolName != "" && !promptHasToolResults(in.Prompt) {
		// First call with tools: return a tool call so the MCP loop executes it.
		response = fmt.Sprintf(`{"name": "%s", "arguments": {"location": "San Francisco"}}`, toolName)
	} else if toolName != "" {
		// Subsequent call: tool results already in prompt, return final text.
		response = "Based on the tool results, the weather in San Francisco is sunny, 72°F."
	} else {
		response = "This is a mocked response."
	}
	return &pb.Reply{
		Message:                []byte(response),
		Tokens:                 10,
		PromptTokens:           5,
		TimingPromptProcessing: 0.1,
		TimingTokenGeneration:  0.2,
	}, nil
}

func (m *MockBackend) PredictStream(in *pb.PredictOptions, stream pb.Backend_PredictStreamServer) error {
	xlog.Debug("PredictStream called", "prompt", in.Prompt)
	if strings.Contains(in.Prompt, "MOCK_ERROR_IMMEDIATE") {
		return fmt.Errorf("mock backend stream error: simulated failure")
	}
	if strings.Contains(in.Prompt, "MOCK_ERROR_MIDSTREAM") {
		for _, r := range "Partial resp" {
			if err := stream.Send(&pb.Reply{Message: []byte(string(r))}); err != nil {
				return err
			}
		}
		return fmt.Errorf("mock backend stream error: simulated mid-stream failure")
	}

	// Simulate C++ autoparser behavior: tool calls delivered via ChatDeltas
	// with empty message (autoparser clears raw message during parsing).
	if strings.Contains(in.Prompt, "AUTOPARSER_TOOL_CALL") {
		toolName := mockToolNameFromRequest(in)
		if toolName == "" {
			toolName = "search_collections"
		}
		// Phase 1: Stream reasoning tokens with empty message (autoparser active)
		reasoning := "I need to search for information."
		for _, r := range reasoning {
			if err := stream.Send(&pb.Reply{
				Message: []byte{}, // autoparser clears raw message
				ChatDeltas: []*pb.ChatDelta{
					{ReasoningContent: string(r)},
				},
			}); err != nil {
				return err
			}
		}
		// Phase 2: Emit tool call via ChatDeltas (no raw message)
		if err := stream.Send(&pb.Reply{
			Message: []byte{}, // autoparser clears raw message
			ChatDeltas: []*pb.ChatDelta{
				{
					ToolCalls: []*pb.ToolCallDelta{
						{
							Index:     0,
							Id:        "call_mock_123",
							Name:      toolName,
							Arguments: `{"query":"localai"}`,
						},
					},
				},
			},
		}); err != nil {
			return err
		}
		return nil
	}

	// Simulate C++ autoparser behavior: content delivered via ChatDeltas
	// with empty message (autoparser clears raw message during parsing).
	if strings.Contains(in.Prompt, "AUTOPARSER_CONTENT") {
		// Phase 1: Stream reasoning via ChatDeltas
		reasoning := "Let me compose a response."
		for _, r := range reasoning {
			if err := stream.Send(&pb.Reply{
				Message: []byte{},
				ChatDeltas: []*pb.ChatDelta{
					{ReasoningContent: string(r)},
				},
			}); err != nil {
				return err
			}
		}
		// Phase 2: Stream content via ChatDeltas (no raw message)
		content := "LocalAI is an open-source AI platform."
		for _, r := range content {
			if err := stream.Send(&pb.Reply{
				Message: []byte{},
				ChatDeltas: []*pb.ChatDelta{
					{Content: string(r)},
				},
			}); err != nil {
				return err
			}
		}
		return nil
	}

	// Simulate tool calls streamed as whole JSON objects (Go-side parser path).
	// Each object is sent as a complete chunk so the incremental parser can
	// detect tool calls mid-stream (unlike char-by-char which only parses after
	// streaming completes).
	if strings.Contains(in.Prompt, "MULTI_TOOL_CALL") {
		chunks := []string{
			`{"name": "get_weather", "arguments": {"location": "Rome"}}`,
			"\n",
			`{"name": "get_weather", "arguments": {"location": "Paris"}}`,
		}
		for i, chunk := range chunks {
			if err := stream.Send(&pb.Reply{
				Message: []byte(chunk),
				Tokens:  int32(i + 1),
			}); err != nil {
				return err
			}
		}
		return nil
	}

	// Simulate single tool call streamed as whole JSON (Go-side parser path).
	if strings.Contains(in.Prompt, "SINGLE_TOOL_CALL") {
		if err := stream.Send(&pb.Reply{
			Message: []byte(`{"name": "get_weather", "arguments": {"location": "San Francisco"}}`),
			Tokens:  1,
		}); err != nil {
			return err
		}
		return nil
	}

	var toStream string
	toolName := mockToolNameFromRequest(in)
	if toolName != "" && !promptHasToolResults(in.Prompt) {
		toStream = fmt.Sprintf(`{"name": "%s", "arguments": {"location": "San Francisco"}}`, toolName)
	} else if toolName != "" {
		toStream = "Based on the tool results, the weather in San Francisco is sunny, 72°F."
	} else {
		toStream = "This is a mocked streaming response."
	}
	for i, r := range toStream {
		if err := stream.Send(&pb.Reply{
			Message: []byte(string(r)),
			Tokens:  int32(i + 1),
		}); err != nil {
			return err
		}
	}
	return nil
}

// mockToolNameFromRequest returns the first tool name from the request's Tools JSON (same as other endpoints).
func mockToolNameFromRequest(in *pb.PredictOptions) string {
	if in.Tools == "" {
		return ""
	}
	var tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(in.Tools), &tools); err != nil || len(tools) == 0 || tools[0].Function.Name == "" {
		return ""
	}
	return tools[0].Function.Name
}

func (m *MockBackend) Embedding(ctx context.Context, in *pb.PredictOptions) (*pb.EmbeddingResult, error) {
	xlog.Debug("Embedding called", "prompt", in.Prompt)
	// Return a mock embedding vector of 768 dimensions
	embeddings := make([]float32, 768)
	for i := range embeddings {
		embeddings[i] = float32(i%100) / 100.0 // Pattern: 0.0, 0.01, 0.02, ..., 0.99, 0.0, ...
	}
	return &pb.EmbeddingResult{Embeddings: embeddings}, nil
}

func (m *MockBackend) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest) (*pb.Result, error) {
	xlog.Debug("GenerateImage called", "prompt", in.PositivePrompt)
	return &pb.Result{
		Message: "Image generated successfully (mocked)",
		Success: true,
	}, nil
}

func (m *MockBackend) GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest) (*pb.Result, error) {
	xlog.Debug("GenerateVideo called", "prompt", in.Prompt)
	return &pb.Result{
		Message: "Video generated successfully (mocked)",
		Success: true,
	}, nil
}

func (m *MockBackend) TTS(ctx context.Context, in *pb.TTSRequest) (*pb.Result, error) {
	xlog.Debug("TTS called", "text", in.Text)
	dst := in.GetDst()
	if dst != "" {
		if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
			return &pb.Result{Message: err.Error(), Success: false}, nil
		}
		if err := writeMinimalWAV(dst); err != nil {
			return &pb.Result{Message: err.Error(), Success: false}, nil
		}
	}
	return &pb.Result{
		Message: "TTS audio generated successfully (mocked)",
		Success: true,
	}, nil
}

func (m *MockBackend) TTSStream(in *pb.TTSRequest, stream pb.Backend_TTSStreamServer) error {
	xlog.Debug("TTSStream called", "text", in.Text)
	// Stream mock audio chunks (simplified - just send a few bytes)
	chunks := [][]byte{
		{0x52, 0x49, 0x46, 0x46}, // Mock WAV header start
		{0x57, 0x41, 0x56, 0x45}, // Mock WAV header
		{0x64, 0x61, 0x74, 0x61}, // Mock data chunk
	}
	for _, chunk := range chunks {
		if err := stream.Send(&pb.Reply{Audio: chunk}); err != nil {
			return err
		}
	}
	return nil
}

func (m *MockBackend) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest) (*pb.Result, error) {
	xlog.Debug("SoundGeneration called",
		"text", in.Text,
		"caption", in.GetCaption(),
		"lyrics", in.GetLyrics(),
		"think", in.GetThink(),
		"bpm", in.GetBpm(),
		"keyscale", in.GetKeyscale(),
		"language", in.GetLanguage(),
		"timesignature", in.GetTimesignature(),
		"instrumental", in.GetInstrumental())
	dst := in.GetDst()
	if dst != "" {
		if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
			return &pb.Result{Message: err.Error(), Success: false}, nil
		}
		if err := writeMinimalWAV(dst); err != nil {
			return &pb.Result{Message: err.Error(), Success: false}, nil
		}
	}
	return &pb.Result{
		Message: "Sound generated successfully (mocked)",
		Success: true,
	}, nil
}

// ttsSampleRate returns the sample rate to use for TTS output, configurable
// via the MOCK_TTS_SAMPLE_RATE environment variable (default 16000).
func ttsSampleRate() int {
	if s := os.Getenv("MOCK_TTS_SAMPLE_RATE"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return 16000
}

// writeMinimalWAV writes a WAV file containing a 440Hz sine wave (0.5s)
// so that tests can verify audio integrity end-to-end. The sample rate
// is configurable via MOCK_TTS_SAMPLE_RATE to test rate mismatch bugs.
func writeMinimalWAV(path string) error {
	sampleRate := ttsSampleRate()
	const numChannels = 1
	const bitsPerSample = 16
	const freq = 440.0
	const durationSec = 0.5
	numSamples := int(float64(sampleRate) * durationSec)

	dataSize := numSamples * numChannels * (bitsPerSample / 8)
	const headerLen = 44
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// RIFF header
	_, _ = f.Write([]byte("RIFF"))
	_ = binary.Write(f, binary.LittleEndian, uint32(headerLen-8+dataSize))
	_, _ = f.Write([]byte("WAVE"))
	// fmt chunk
	_, _ = f.Write([]byte("fmt "))
	_ = binary.Write(f, binary.LittleEndian, uint32(16))
	_ = binary.Write(f, binary.LittleEndian, uint16(1))
	_ = binary.Write(f, binary.LittleEndian, uint16(numChannels))
	_ = binary.Write(f, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(f, binary.LittleEndian, uint32(sampleRate*numChannels*(bitsPerSample/8)))
	_ = binary.Write(f, binary.LittleEndian, uint16(numChannels*(bitsPerSample/8)))
	_ = binary.Write(f, binary.LittleEndian, uint16(bitsPerSample))
	// data chunk — 440Hz sine wave
	_, _ = f.Write([]byte("data"))
	_ = binary.Write(f, binary.LittleEndian, uint32(dataSize))
	for i := range numSamples {
		t := float64(i) / float64(sampleRate)
		sample := int16(math.MaxInt16 / 2 * math.Sin(2*math.Pi*freq*t))
		_ = binary.Write(f, binary.LittleEndian, sample)
	}
	return nil
}

func (m *MockBackend) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest) (*pb.TranscriptResult, error) {
	dst := in.GetDst()
	wavSR := 0
	dataLen := 0
	rms := 0.0

	if dst != "" {
		if data, err := os.ReadFile(dst); err == nil {
			if len(data) >= 44 {
				wavSR = int(binary.LittleEndian.Uint32(data[24:28]))
				dataLen = int(binary.LittleEndian.Uint32(data[40:44]))

				// Compute RMS of the PCM payload (16-bit LE samples)
				pcm := data[44:]
				var sumSq float64
				nSamples := len(pcm) / 2
				for i := range nSamples {
					s := int16(pcm[2*i]) | int16(pcm[2*i+1])<<8
					v := float64(s)
					sumSq += v * v
				}
				if nSamples > 0 {
					rms = math.Sqrt(sumSq / float64(nSamples))
				}
			}
		}
	}

	xlog.Debug("AudioTranscription called", "dst", dst, "wav_sample_rate", wavSR, "data_len", dataLen, "rms", rms)

	text := fmt.Sprintf("transcribed: rms=%.1f samples=%d sr=%d", rms, dataLen/2, wavSR)
	return &pb.TranscriptResult{
		Text: text,
		Segments: []*pb.TranscriptSegment{
			{
				Id:     0,
				Start:  0,
				End:    3000,
				Text:   text,
				Tokens: []int32{1, 2, 3, 4, 5, 6},
			},
		},
	}, nil
}

func (m *MockBackend) TokenizeString(ctx context.Context, in *pb.PredictOptions) (*pb.TokenizationResponse, error) {
	xlog.Debug("TokenizeString called", "prompt", in.Prompt)
	// Return mock token IDs
	tokens := []int32{101, 2023, 2003, 1037, 3231, 1012}
	return &pb.TokenizationResponse{
		Length: int32(len(tokens)),
		Tokens: tokens,
	}, nil
}

func (m *MockBackend) Status(ctx context.Context, in *pb.HealthMessage) (*pb.StatusResponse, error) {
	xlog.Debug("Status called")
	return &pb.StatusResponse{
		State: pb.StatusResponse_READY,
		Memory: &pb.MemoryUsageData{
			Total: 1024 * 1024 * 100, // 100MB
			Breakdown: map[string]uint64{
				"mock": 1024 * 1024 * 50,
			},
		},
	}, nil
}

func (m *MockBackend) Detect(ctx context.Context, in *pb.DetectOptions) (*pb.DetectResponse, error) {
	xlog.Debug("Detect called", "src", in.Src)
	return &pb.DetectResponse{
		Detections: []*pb.Detection{
			{
				X:          10.0,
				Y:          20.0,
				Width:      100.0,
				Height:     200.0,
				Confidence: 0.95,
				ClassName:  "mocked_object",
			},
		},
	}, nil
}

func (m *MockBackend) StoresSet(ctx context.Context, in *pb.StoresSetOptions) (*pb.Result, error) {
	xlog.Debug("StoresSet called", "keys", len(in.Keys))
	return &pb.Result{
		Message: "Keys set successfully (mocked)",
		Success: true,
	}, nil
}

func (m *MockBackend) StoresDelete(ctx context.Context, in *pb.StoresDeleteOptions) (*pb.Result, error) {
	xlog.Debug("StoresDelete called", "keys", len(in.Keys))
	return &pb.Result{
		Message: "Keys deleted successfully (mocked)",
		Success: true,
	}, nil
}

func (m *MockBackend) StoresGet(ctx context.Context, in *pb.StoresGetOptions) (*pb.StoresGetResult, error) {
	xlog.Debug("StoresGet called", "keys", len(in.Keys))
	// Return mock keys and values
	keys := make([]*pb.StoresKey, len(in.Keys))
	values := make([]*pb.StoresValue, len(in.Keys))
	for i := range in.Keys {
		keys[i] = in.Keys[i]
		values[i] = &pb.StoresValue{
			Bytes: []byte(fmt.Sprintf("mocked_value_%d", i)),
		}
	}
	return &pb.StoresGetResult{
		Keys:   keys,
		Values: values,
	}, nil
}

func (m *MockBackend) StoresFind(ctx context.Context, in *pb.StoresFindOptions) (*pb.StoresFindResult, error) {
	xlog.Debug("StoresFind called", "topK", in.TopK)
	// Return mock similar keys
	keys := []*pb.StoresKey{
		{Floats: []float32{0.1, 0.2, 0.3}},
		{Floats: []float32{0.4, 0.5, 0.6}},
	}
	values := []*pb.StoresValue{
		{Bytes: []byte("mocked_value_1")},
		{Bytes: []byte("mocked_value_2")},
	}
	similarities := []float32{0.95, 0.85}
	return &pb.StoresFindResult{
		Keys:         keys,
		Values:       values,
		Similarities: similarities,
	}, nil
}

func (m *MockBackend) Rerank(ctx context.Context, in *pb.RerankRequest) (*pb.RerankResult, error) {
	xlog.Debug("Rerank called", "query", in.Query, "documents", len(in.Documents))
	// Return mock reranking results
	results := make([]*pb.DocumentResult, len(in.Documents))
	for i, doc := range in.Documents {
		results[i] = &pb.DocumentResult{
			Index:          int32(i),
			Text:           doc,
			RelevanceScore: 0.9 - float32(i)*0.1, // Decreasing scores
		}
	}
	return &pb.RerankResult{
		Usage: &pb.Usage{
			TotalTokens:  int32(len(in.Documents) * 10),
			PromptTokens: int32(len(in.Documents) * 10),
		},
		Results: results,
	}, nil
}

func (m *MockBackend) GetMetrics(ctx context.Context, in *pb.MetricsRequest) (*pb.MetricsResponse, error) {
	xlog.Debug("GetMetrics called")
	return &pb.MetricsResponse{
		SlotId:                0,
		PromptJsonForSlot:     `{"prompt":"mocked"}`,
		TokensPerSecond:       10.0,
		TokensGenerated:       100,
		PromptTokensProcessed: 50,
	}, nil
}

func (m *MockBackend) VAD(ctx context.Context, in *pb.VADRequest) (*pb.VADResponse, error) {
	// Compute RMS of the received float32 audio to decide whether speech is present.
	var sumSq float64
	for _, s := range in.Audio {
		v := float64(s)
		sumSq += v * v
	}
	rms := 0.0
	if len(in.Audio) > 0 {
		rms = math.Sqrt(sumSq / float64(len(in.Audio)))
	}
	xlog.Debug("VAD called", "audio_length", len(in.Audio), "rms", rms)

	// If audio is near-silence, return no segments (no speech detected).
	if rms < 0.001 {
		return &pb.VADResponse{}, nil
	}

	// Audio has signal — return a single segment covering the duration.
	duration := float64(len(in.Audio)) / 16000.0
	return &pb.VADResponse{
		Segments: []*pb.VADSegment{
			{
				Start: 0.0,
				End:   float32(duration),
			},
		},
	}, nil
}

func (m *MockBackend) AudioEncode(ctx context.Context, in *pb.AudioEncodeRequest) (*pb.AudioEncodeResult, error) {
	xlog.Debug("AudioEncode called", "pcm_len", len(in.PcmData), "sample_rate", in.SampleRate)
	// Return a single mock Opus frame per 960-sample chunk (20ms at 48kHz).
	numSamples := len(in.PcmData) / 2 // 16-bit samples
	frameSize := 960
	var frames [][]byte
	for offset := 0; offset+frameSize <= numSamples; offset += frameSize {
		// Minimal mock frame — just enough bytes to be non-empty.
		frames = append(frames, []byte{0xFC, 0xFF, 0xFE})
	}
	return &pb.AudioEncodeResult{
		Frames:          frames,
		SampleRate:      48000,
		SamplesPerFrame: int32(frameSize),
	}, nil
}

func (m *MockBackend) AudioDecode(ctx context.Context, in *pb.AudioDecodeRequest) (*pb.AudioDecodeResult, error) {
	xlog.Debug("AudioDecode called", "frames", len(in.Frames))
	// Return silent PCM (960 samples per frame at 48kHz, 16-bit LE).
	samplesPerFrame := 960
	totalSamples := len(in.Frames) * samplesPerFrame
	pcm := make([]byte, totalSamples*2)
	return &pb.AudioDecodeResult{
		PcmData:         pcm,
		SampleRate:      48000,
		SamplesPerFrame: int32(samplesPerFrame),
	}, nil
}

func (m *MockBackend) ModelMetadata(ctx context.Context, in *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
	xlog.Debug("ModelMetadata called", "model", in.Model)
	return &pb.ModelMetadataResponse{
		SupportsThinking: false,
		RenderedTemplate: "",
	}, nil
}

func main() {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(os.Getenv("LOCALAI_LOG_LEVEL")), os.Getenv("LOCALAI_LOG_FORMAT")))

	flag.Parse()

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer(
		grpc.MaxRecvMsgSize(50*1024*1024), // 50MB
		grpc.MaxSendMsgSize(50*1024*1024), // 50MB
	)
	pb.RegisterBackendServer(s, &MockBackend{})

	xlog.Info("Mock gRPC Server listening", "address", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
