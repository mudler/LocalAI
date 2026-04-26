package nodes

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	ggrpc "google.golang.org/grpc"
)

// InFlightTrackingClient wraps a grpc.Backend and tracks active inference requests
// in the NodeRegistry. This allows the router's eviction logic to know which models
// are actively serving and should not be unloaded.
type InFlightTrackingClient struct {
	grpc.Backend // embed for passthrough of untracked methods
	registry     InFlightTracker
	nodeID       string
	modelName    string

	firstOnce     sync.Once  // guards onFirstComplete
	onFirstComplete func()   // called once after the first tracked inference call completes
}

// NewInFlightTrackingClient wraps a gRPC backend client with in-flight tracking.
func NewInFlightTrackingClient(inner grpc.Backend, registry InFlightTracker, nodeID, modelName string) *InFlightTrackingClient {
	return &InFlightTrackingClient{
		Backend:   inner,
		registry:  registry,
		nodeID:    nodeID,
		modelName: modelName,
	}
}

// OnFirstComplete registers a callback that fires once after the first tracked
// inference call completes. This is used to release the initial in-flight
// reservation (set during model load) after the triggering request finishes,
// so that in-flight returns to 0 when the model is idle.
func (c *InFlightTrackingClient) OnFirstComplete(fn func()) {
	c.onFirstComplete = fn
}

func (c *InFlightTrackingClient) track(ctx context.Context) func() {
	if err := c.registry.IncrementInFlight(ctx, c.nodeID, c.modelName); err != nil {
		xlog.Warn("Failed to increment in-flight counter", "node", c.nodeID, "model", c.modelName, "error", err)
		return func() {}
	}
	return func() {
		decCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.registry.DecrementInFlight(decCtx, c.nodeID, c.modelName)
		// Release the initial reservation after the first inference call completes
		if c.onFirstComplete != nil {
			c.firstOnce.Do(c.onFirstComplete)
		}
	}
}

// --- Tracked inference methods ---

func (c *InFlightTrackingClient) Predict(ctx context.Context, in *pb.PredictOptions, opts ...ggrpc.CallOption) (*pb.Reply, error) {
	defer c.track(ctx)()
	return c.Backend.Predict(ctx, in, opts...)
}

func (c *InFlightTrackingClient) PredictStream(ctx context.Context, in *pb.PredictOptions, f func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	defer c.track(ctx)()
	return c.Backend.PredictStream(ctx, in, f, opts...)
}

func (c *InFlightTrackingClient) Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...ggrpc.CallOption) (*pb.EmbeddingResult, error) {
	defer c.track(ctx)()
	return c.Backend.Embeddings(ctx, in, opts...)
}

func (c *InFlightTrackingClient) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	return c.Backend.GenerateImage(ctx, in, opts...)
}

func (c *InFlightTrackingClient) GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	return c.Backend.GenerateVideo(ctx, in, opts...)
}

func (c *InFlightTrackingClient) TTS(ctx context.Context, in *pb.TTSRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	return c.Backend.TTS(ctx, in, opts...)
}

func (c *InFlightTrackingClient) TTSStream(ctx context.Context, in *pb.TTSRequest, f func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	defer c.track(ctx)()
	return c.Backend.TTSStream(ctx, in, f, opts...)
}

func (c *InFlightTrackingClient) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	return c.Backend.SoundGeneration(ctx, in, opts...)
}

func (c *InFlightTrackingClient) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...ggrpc.CallOption) (*pb.TranscriptResult, error) {
	defer c.track(ctx)()
	return c.Backend.AudioTranscription(ctx, in, opts...)
}

func (c *InFlightTrackingClient) AudioTranscriptionStream(ctx context.Context, in *pb.TranscriptRequest, f func(chunk *pb.TranscriptStreamResponse), opts ...ggrpc.CallOption) error {
	defer c.track(ctx)()
	return c.Backend.AudioTranscriptionStream(ctx, in, f, opts...)
}

func (c *InFlightTrackingClient) Detect(ctx context.Context, in *pb.DetectOptions, opts ...ggrpc.CallOption) (*pb.DetectResponse, error) {
	defer c.track(ctx)()
	return c.Backend.Detect(ctx, in, opts...)
}

func (c *InFlightTrackingClient) Rerank(ctx context.Context, in *pb.RerankRequest, opts ...ggrpc.CallOption) (*pb.RerankResult, error) {
	defer c.track(ctx)()
	return c.Backend.Rerank(ctx, in, opts...)
}
