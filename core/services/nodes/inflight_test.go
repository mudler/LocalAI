package nodes

import (
	"context"
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
)

// --- Fakes ---

// fakeInFlightTracker implements InFlightTracker, counting calls.
type fakeInFlightTracker struct {
	mu           sync.Mutex
	increments   int
	decrements   int
	incrementErr error
}

func (f *fakeInFlightTracker) IncrementInFlight(_ context.Context, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.increments++
	return f.incrementErr
}

func (f *fakeInFlightTracker) DecrementInFlight(_ context.Context, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decrements++
	return nil
}

// fakeGRPCBackend implements grpc.Backend with stub methods.
// Only the methods we test (Predict, PredictStream) have real behavior;
// the rest panic if called unexpectedly.
type fakeGRPCBackend struct {
	predictReply  *pb.Reply
	predictErr    error
	streamReplies []*pb.Reply
	streamErr     error
}

func (f *fakeGRPCBackend) IsBusy() bool                                { return false }
func (f *fakeGRPCBackend) HealthCheck(_ context.Context) (bool, error) { return true, nil }
func (f *fakeGRPCBackend) LoadModel(_ context.Context, _ *pb.ModelOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{}, nil
}

func (f *fakeGRPCBackend) Predict(_ context.Context, _ *pb.PredictOptions, _ ...ggrpc.CallOption) (*pb.Reply, error) {
	return f.predictReply, f.predictErr
}

func (f *fakeGRPCBackend) PredictStream(_ context.Context, _ *pb.PredictOptions, fn func(reply *pb.Reply), _ ...ggrpc.CallOption) error {
	for _, r := range f.streamReplies {
		fn(r)
	}
	return f.streamErr
}

func (f *fakeGRPCBackend) Embeddings(_ context.Context, _ *pb.PredictOptions, _ ...ggrpc.CallOption) (*pb.EmbeddingResult, error) {
	return &pb.EmbeddingResult{}, nil
}

func (f *fakeGRPCBackend) GenerateImage(_ context.Context, _ *pb.GenerateImageRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{}, nil
}

func (f *fakeGRPCBackend) GenerateVideo(_ context.Context, _ *pb.GenerateVideoRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{}, nil
}

func (f *fakeGRPCBackend) TTS(_ context.Context, _ *pb.TTSRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{}, nil
}

func (f *fakeGRPCBackend) TTSStream(_ context.Context, _ *pb.TTSRequest, _ func(reply *pb.Reply), _ ...ggrpc.CallOption) error {
	return nil
}

func (f *fakeGRPCBackend) SoundGeneration(_ context.Context, _ *pb.SoundGenerationRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{}, nil
}

func (f *fakeGRPCBackend) Detect(_ context.Context, _ *pb.DetectOptions, _ ...ggrpc.CallOption) (*pb.DetectResponse, error) {
	return &pb.DetectResponse{}, nil
}

func (f *fakeGRPCBackend) FaceVerify(_ context.Context, _ *pb.FaceVerifyRequest, _ ...ggrpc.CallOption) (*pb.FaceVerifyResponse, error) {
	return &pb.FaceVerifyResponse{}, nil
}

func (f *fakeGRPCBackend) FaceAnalyze(_ context.Context, _ *pb.FaceAnalyzeRequest, _ ...ggrpc.CallOption) (*pb.FaceAnalyzeResponse, error) {
	return &pb.FaceAnalyzeResponse{}, nil
}

func (f *fakeGRPCBackend) AudioTranscription(_ context.Context, _ *pb.TranscriptRequest, _ ...ggrpc.CallOption) (*pb.TranscriptResult, error) {
	return &pb.TranscriptResult{}, nil
}

func (f *fakeGRPCBackend) AudioTranscriptionStream(_ context.Context, _ *pb.TranscriptRequest, _ func(chunk *pb.TranscriptStreamResponse), _ ...ggrpc.CallOption) error {
	return nil
}

func (f *fakeGRPCBackend) TokenizeString(_ context.Context, _ *pb.PredictOptions, _ ...ggrpc.CallOption) (*pb.TokenizationResponse, error) {
	return &pb.TokenizationResponse{}, nil
}

func (f *fakeGRPCBackend) Status(_ context.Context) (*pb.StatusResponse, error) {
	return &pb.StatusResponse{}, nil
}

func (f *fakeGRPCBackend) StoresSet(_ context.Context, _ *pb.StoresSetOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{}, nil
}

func (f *fakeGRPCBackend) StoresDelete(_ context.Context, _ *pb.StoresDeleteOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{}, nil
}

func (f *fakeGRPCBackend) StoresGet(_ context.Context, _ *pb.StoresGetOptions, _ ...ggrpc.CallOption) (*pb.StoresGetResult, error) {
	return &pb.StoresGetResult{}, nil
}

func (f *fakeGRPCBackend) StoresFind(_ context.Context, _ *pb.StoresFindOptions, _ ...ggrpc.CallOption) (*pb.StoresFindResult, error) {
	return &pb.StoresFindResult{}, nil
}

func (f *fakeGRPCBackend) Rerank(_ context.Context, _ *pb.RerankRequest, _ ...ggrpc.CallOption) (*pb.RerankResult, error) {
	return &pb.RerankResult{}, nil
}

func (f *fakeGRPCBackend) GetTokenMetrics(_ context.Context, _ *pb.MetricsRequest, _ ...ggrpc.CallOption) (*pb.MetricsResponse, error) {
	return &pb.MetricsResponse{}, nil
}

func (f *fakeGRPCBackend) VAD(_ context.Context, _ *pb.VADRequest, _ ...ggrpc.CallOption) (*pb.VADResponse, error) {
	return &pb.VADResponse{}, nil
}

func (f *fakeGRPCBackend) AudioEncode(_ context.Context, _ *pb.AudioEncodeRequest, _ ...ggrpc.CallOption) (*pb.AudioEncodeResult, error) {
	return &pb.AudioEncodeResult{}, nil
}

func (f *fakeGRPCBackend) AudioDecode(_ context.Context, _ *pb.AudioDecodeRequest, _ ...ggrpc.CallOption) (*pb.AudioDecodeResult, error) {
	return &pb.AudioDecodeResult{}, nil
}

func (f *fakeGRPCBackend) ModelMetadata(_ context.Context, _ *pb.ModelOptions, _ ...ggrpc.CallOption) (*pb.ModelMetadataResponse, error) {
	return &pb.ModelMetadataResponse{}, nil
}

func (f *fakeGRPCBackend) StartFineTune(_ context.Context, _ *pb.FineTuneRequest, _ ...ggrpc.CallOption) (*pb.FineTuneJobResult, error) {
	return &pb.FineTuneJobResult{}, nil
}

func (f *fakeGRPCBackend) FineTuneProgress(_ context.Context, _ *pb.FineTuneProgressRequest, _ func(update *pb.FineTuneProgressUpdate), _ ...ggrpc.CallOption) error {
	return nil
}

func (f *fakeGRPCBackend) StopFineTune(_ context.Context, _ *pb.FineTuneStopRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{}, nil
}

func (f *fakeGRPCBackend) ListCheckpoints(_ context.Context, _ *pb.ListCheckpointsRequest, _ ...ggrpc.CallOption) (*pb.ListCheckpointsResponse, error) {
	return &pb.ListCheckpointsResponse{}, nil
}

func (f *fakeGRPCBackend) ExportModel(_ context.Context, _ *pb.ExportModelRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{}, nil
}

func (f *fakeGRPCBackend) StartQuantization(_ context.Context, _ *pb.QuantizationRequest, _ ...ggrpc.CallOption) (*pb.QuantizationJobResult, error) {
	return &pb.QuantizationJobResult{}, nil
}

func (f *fakeGRPCBackend) QuantizationProgress(_ context.Context, _ *pb.QuantizationProgressRequest, _ func(update *pb.QuantizationProgressUpdate), _ ...ggrpc.CallOption) error {
	return nil
}

func (f *fakeGRPCBackend) StopQuantization(_ context.Context, _ *pb.QuantizationStopRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{}, nil
}

func (f *fakeGRPCBackend) Free(_ context.Context) error {
	return nil
}

// --- Tests ---

var _ = Describe("InFlightTrackingClient", func() {
	var (
		tracker *fakeInFlightTracker
		backend *fakeGRPCBackend
		client  *InFlightTrackingClient
	)

	BeforeEach(func() {
		tracker = &fakeInFlightTracker{}
		backend = &fakeGRPCBackend{
			predictReply:  &pb.Reply{Message: []byte("hello")},
			streamReplies: []*pb.Reply{{Message: []byte("chunk")}},
		}
		client = NewInFlightTrackingClient(backend, tracker, "node-1", "llama")
	})

	Describe("track", func() {
		It("increments and decrements via InFlightTracker", func() {
			done := client.track(context.Background())
			Expect(tracker.increments).To(Equal(1))
			Expect(tracker.decrements).To(Equal(0))
			done()
			Expect(tracker.decrements).To(Equal(1))
		})

		It("returns no-op when increment fails", func() {
			tracker.incrementErr = fmt.Errorf("registry down")
			done := client.track(context.Background())
			Expect(tracker.increments).To(Equal(1))
			// Decrement should NOT be called on cleanup since increment failed.
			done()
			Expect(tracker.decrements).To(Equal(0))
		})
	})

	Describe("Predict", func() {
		It("calls track and delegates to backend", func() {
			reply, err := client.Predict(context.Background(), &pb.PredictOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Message).To(Equal([]byte("hello")))

			// track was called and cleaned up (defer).
			Expect(tracker.increments).To(Equal(1))
			Expect(tracker.decrements).To(Equal(1))
		})
	})

	Describe("PredictStream", func() {
		It("calls track and delegates to backend", func() {
			var replies []*pb.Reply
			err := client.PredictStream(context.Background(), &pb.PredictOptions{}, func(r *pb.Reply) {
				replies = append(replies, r)
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(replies).To(HaveLen(1))
			Expect(replies[0].Message).To(Equal([]byte("chunk")))

			Expect(tracker.increments).To(Equal(1))
			Expect(tracker.decrements).To(Equal(1))
		})
	})
})
