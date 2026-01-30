package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

// MockBackend implements the Backend gRPC service with mocked responses
type MockBackend struct {
	pb.UnimplementedBackendServer
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
	var response string
	toolName := mockToolNameFromRequest(in)
	if toolName != "" {
		response = fmt.Sprintf(`{"name": "%s", "arguments": {"location": "San Francisco"}}`, toolName)
	} else {
		response = "This is a mocked response."
	}
	return &pb.Reply{
		Message:                 []byte(response),
		Tokens:                  10,
		PromptTokens:            5,
		TimingPromptProcessing:  0.1,
		TimingTokenGeneration:   0.2,
	}, nil
}

func (m *MockBackend) PredictStream(in *pb.PredictOptions, stream pb.Backend_PredictStreamServer) error {
	xlog.Debug("PredictStream called", "prompt", in.Prompt)
	var toStream string
	toolName := mockToolNameFromRequest(in)
	if toolName != "" {
		toStream = fmt.Sprintf(`{"name": "%s", "arguments": {"location": "San Francisco"}}`, toolName)
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
	// Return success - actual audio would be in the Result message for real backends
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
	xlog.Debug("SoundGeneration called", "text", in.Text)
	return &pb.Result{
		Message: "Sound generated successfully (mocked)",
		Success: true,
	}, nil
}

func (m *MockBackend) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest) (*pb.TranscriptResult, error) {
	xlog.Debug("AudioTranscription called")
	return &pb.TranscriptResult{
		Text: "This is a mocked transcription.",
		Segments: []*pb.TranscriptSegment{
			{
				Id:    0,
				Start: 0,
				End:   3000,
				Text:  "This is a mocked transcription.",
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
		Tokens:  tokens,
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
				X:         10.0,
				Y:         20.0,
				Width:     100.0,
				Height:    200.0,
				Confidence: 0.95,
				ClassName: "mocked_object",
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
		Keys:        keys,
		Values:      values,
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
		SlotId:              0,
		PromptJsonForSlot:   `{"prompt":"mocked"}`,
		TokensPerSecond:     10.0,
		TokensGenerated:     100,
		PromptTokensProcessed: 50,
	}, nil
}

func (m *MockBackend) VAD(ctx context.Context, in *pb.VADRequest) (*pb.VADResponse, error) {
	xlog.Debug("VAD called", "audio_length", len(in.Audio))
	return &pb.VADResponse{
		Segments: []*pb.VADSegment{
			{
				Start: 0.0,
				End:   1.5,
			},
			{
				Start: 2.0,
				End:   3.5,
			},
		},
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
