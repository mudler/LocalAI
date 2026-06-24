package nodes

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	ggrpc "google.golang.org/grpc"
)

// InFlightTrackingClient wraps a grpc.Backend and tracks active inference requests
// in the NodeRegistry. This allows the router's eviction logic to know which models
// are actively serving and should not be unloaded.
//
// Per-replica: a single tracker instance is bound to (nodeID, modelName, replicaIndex).
// The router constructs one tracker per Route() result, so each in-flight tick lands
// on the correct row even when multiple replicas of the same model live on the same node.
type InFlightTrackingClient struct {
	grpc.Backend // embed for passthrough of untracked methods
	registry     InFlightTracker
	nodeID       string
	modelName    string
	replicaIndex int

	firstOnce       sync.Once // guards onFirstComplete
	onFirstComplete func()    // called once after the first tracked inference call completes
}

// NewInFlightTrackingClient wraps a gRPC backend client with in-flight tracking.
func NewInFlightTrackingClient(inner grpc.Backend, registry InFlightTracker, nodeID, modelName string, replicaIndex int) *InFlightTrackingClient {
	return &InFlightTrackingClient{
		Backend:      inner,
		registry:     registry,
		nodeID:       nodeID,
		modelName:    modelName,
		replicaIndex: replicaIndex,
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
	if err := c.registry.IncrementInFlight(ctx, c.nodeID, c.modelName, c.replicaIndex); err != nil {
		xlog.Warn("Failed to increment in-flight counter", "node", c.nodeID, "model", c.modelName, "replica", c.replicaIndex, "error", err)
		return func() {}
	}
	return func() {
		decCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.registry.DecrementInFlight(decCtx, c.nodeID, c.modelName, c.replicaIndex)
		// Release the initial reservation after the first inference call completes
		if c.onFirstComplete != nil {
			c.firstOnce.Do(c.onFirstComplete)
		}
	}
}

// reconcile self-heals stale routing: when a backend reports that the model is
// no longer loaded (the process survived but the model was evicted, while the
// registry still lists it as loaded), it drops the replica row so the next
// request triggers a fresh load instead of routing back here. Without this the
// model stays unreachable until the controller restarts. The original error is
// returned unchanged.
func (c *InFlightTrackingClient) reconcile(err error) error {
	if !grpcerrors.IsModelNotLoaded(err) {
		return err
	}
	rmCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if rmErr := c.registry.RemoveNodeModel(rmCtx, c.nodeID, c.modelName, c.replicaIndex); rmErr != nil {
		xlog.Warn("Failed to drop stale replica after model-not-loaded",
			"node", c.nodeID, "model", c.modelName, "replica", c.replicaIndex, "error", rmErr)
	} else {
		xlog.Warn("Backend reports model not loaded; dropped stale replica so the next request reloads",
			"node", c.nodeID, "model", c.modelName, "replica", c.replicaIndex)
	}
	return err
}

// --- Tracked inference methods ---

func (c *InFlightTrackingClient) Predict(ctx context.Context, in *pb.PredictOptions, opts ...ggrpc.CallOption) (*pb.Reply, error) {
	defer c.track(ctx)()
	reply, err := c.Backend.Predict(ctx, in, opts...)
	return reply, c.reconcile(err)
}

func (c *InFlightTrackingClient) PredictStream(ctx context.Context, in *pb.PredictOptions, f func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	defer c.track(ctx)()
	return c.reconcile(c.Backend.PredictStream(ctx, in, f, opts...))
}

func (c *InFlightTrackingClient) Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...ggrpc.CallOption) (*pb.EmbeddingResult, error) {
	defer c.track(ctx)()
	res, err := c.Backend.Embeddings(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	res, err := c.Backend.GenerateImage(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	res, err := c.Backend.GenerateVideo(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) TTS(ctx context.Context, in *pb.TTSRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	res, err := c.Backend.TTS(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) TTSStream(ctx context.Context, in *pb.TTSRequest, f func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	defer c.track(ctx)()
	return c.reconcile(c.Backend.TTSStream(ctx, in, f, opts...))
}

func (c *InFlightTrackingClient) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	res, err := c.Backend.SoundGeneration(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...ggrpc.CallOption) (*pb.TranscriptResult, error) {
	defer c.track(ctx)()
	res, err := c.Backend.AudioTranscription(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) AudioTranscriptionStream(ctx context.Context, in *pb.TranscriptRequest, f func(chunk *pb.TranscriptStreamResponse), opts ...ggrpc.CallOption) error {
	defer c.track(ctx)()
	return c.reconcile(c.Backend.AudioTranscriptionStream(ctx, in, f, opts...))
}

func (c *InFlightTrackingClient) Detect(ctx context.Context, in *pb.DetectOptions, opts ...ggrpc.CallOption) (*pb.DetectResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.Detect(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) Depth(ctx context.Context, in *pb.DepthRequest, opts ...ggrpc.CallOption) (*pb.DepthResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.Depth(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) Rerank(ctx context.Context, in *pb.RerankRequest, opts ...ggrpc.CallOption) (*pb.RerankResult, error) {
	defer c.track(ctx)()
	res, err := c.Backend.Rerank(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) VAD(ctx context.Context, in *pb.VADRequest, opts ...ggrpc.CallOption) (*pb.VADResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.VAD(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) Diarize(ctx context.Context, in *pb.DiarizeRequest, opts ...ggrpc.CallOption) (*pb.DiarizeResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.Diarize(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) FaceVerify(ctx context.Context, in *pb.FaceVerifyRequest, opts ...ggrpc.CallOption) (*pb.FaceVerifyResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.FaceVerify(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) FaceAnalyze(ctx context.Context, in *pb.FaceAnalyzeRequest, opts ...ggrpc.CallOption) (*pb.FaceAnalyzeResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.FaceAnalyze(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) VoiceVerify(ctx context.Context, in *pb.VoiceVerifyRequest, opts ...ggrpc.CallOption) (*pb.VoiceVerifyResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.VoiceVerify(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) VoiceAnalyze(ctx context.Context, in *pb.VoiceAnalyzeRequest, opts ...ggrpc.CallOption) (*pb.VoiceAnalyzeResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.VoiceAnalyze(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) VoiceEmbed(ctx context.Context, in *pb.VoiceEmbedRequest, opts ...ggrpc.CallOption) (*pb.VoiceEmbedResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.VoiceEmbed(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) TokenClassify(ctx context.Context, in *pb.TokenClassifyRequest, opts ...ggrpc.CallOption) (*pb.TokenClassifyResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.TokenClassify(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) Score(ctx context.Context, in *pb.ScoreRequest, opts ...ggrpc.CallOption) (*pb.ScoreResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.Score(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) SoundDetection(ctx context.Context, in *pb.SoundDetectionRequest, opts ...ggrpc.CallOption) (*pb.SoundDetectionResponse, error) {
	defer c.track(ctx)()
	res, err := c.Backend.SoundDetection(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) AudioEncode(ctx context.Context, in *pb.AudioEncodeRequest, opts ...ggrpc.CallOption) (*pb.AudioEncodeResult, error) {
	defer c.track(ctx)()
	res, err := c.Backend.AudioEncode(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) AudioDecode(ctx context.Context, in *pb.AudioDecodeRequest, opts ...ggrpc.CallOption) (*pb.AudioDecodeResult, error) {
	defer c.track(ctx)()
	res, err := c.Backend.AudioDecode(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) AudioTransform(ctx context.Context, in *pb.AudioTransformRequest, opts ...ggrpc.CallOption) (*pb.AudioTransformResult, error) {
	defer c.track(ctx)()
	res, err := c.Backend.AudioTransform(ctx, in, opts...)
	return res, c.reconcile(err)
}

// AudioTransformStream, AudioToAudioStream and Forward are deliberately left as
// embedded passthrough: they return a stream client and the inference spans the
// stream's lifetime, not the constructor call. Wrapping the constructor with
// track() would increment and immediately decrement (and fire onFirstComplete)
// before any audio flows. Tracking those correctly needs the done() func tied to
// stream close, which the current Backend interface doesn't surface here.
