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
//
// Embedding only grpc.ControlBackend (not the whole grpc.Backend) is what makes
// the in-flight accounting safe by construction: the control-plane methods pass
// through untracked, while every grpc.InferenceBackend method must be declared
// explicitly below to satisfy grpc.Backend. Adding an inference method to the
// interface therefore breaks this file's build (see the var assertion below)
// until it is wrapped with track() - so a new inference path can't be added
// without an in-flight accounting decision.
type InFlightTrackingClient struct {
	grpc.ControlBackend                       // passthrough for control-plane / streaming-constructor methods
	inner               grpc.InferenceBackend // tracked inference methods delegate here
	registry            InFlightTracker
	nodeID              string
	modelName           string
	replicaIndex        int

	firstOnce       sync.Once // guards onFirstComplete
	onFirstComplete func()    // called once after the first tracked inference call completes
}

// Compile-time contract: *InFlightTrackingClient must implement the FULL backend
// surface. Because it embeds only ControlBackend, this fails to compile if any
// InferenceBackend method is left unwrapped.
var _ grpc.Backend = (*InFlightTrackingClient)(nil)

// NewInFlightTrackingClient wraps a gRPC backend client with in-flight tracking.
func NewInFlightTrackingClient(inner grpc.Backend, registry InFlightTracker, nodeID, modelName string, replicaIndex int) *InFlightTrackingClient {
	return &InFlightTrackingClient{
		ControlBackend: inner,
		inner:          inner,
		registry:       registry,
		nodeID:         nodeID,
		modelName:      modelName,
		replicaIndex:   replicaIndex,
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
	reply, err := c.inner.Predict(ctx, in, opts...)
	return reply, c.reconcile(err)
}

func (c *InFlightTrackingClient) PredictStream(ctx context.Context, in *pb.PredictOptions, f func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	defer c.track(ctx)()
	return c.reconcile(c.inner.PredictStream(ctx, in, f, opts...))
}

func (c *InFlightTrackingClient) Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...ggrpc.CallOption) (*pb.EmbeddingResult, error) {
	defer c.track(ctx)()
	res, err := c.inner.Embeddings(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	res, err := c.inner.GenerateImage(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	res, err := c.inner.GenerateVideo(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) TTS(ctx context.Context, in *pb.TTSRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	res, err := c.inner.TTS(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) TTSStream(ctx context.Context, in *pb.TTSRequest, f func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	defer c.track(ctx)()
	return c.reconcile(c.inner.TTSStream(ctx, in, f, opts...))
}

func (c *InFlightTrackingClient) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	defer c.track(ctx)()
	res, err := c.inner.SoundGeneration(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...ggrpc.CallOption) (*pb.TranscriptResult, error) {
	defer c.track(ctx)()
	res, err := c.inner.AudioTranscription(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) AudioTranscriptionStream(ctx context.Context, in *pb.TranscriptRequest, f func(chunk *pb.TranscriptStreamResponse), opts ...ggrpc.CallOption) error {
	defer c.track(ctx)()
	return c.reconcile(c.inner.AudioTranscriptionStream(ctx, in, f, opts...))
}

func (c *InFlightTrackingClient) Detect(ctx context.Context, in *pb.DetectOptions, opts ...ggrpc.CallOption) (*pb.DetectResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.Detect(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) Depth(ctx context.Context, in *pb.DepthRequest, opts ...ggrpc.CallOption) (*pb.DepthResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.Depth(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) Rerank(ctx context.Context, in *pb.RerankRequest, opts ...ggrpc.CallOption) (*pb.RerankResult, error) {
	defer c.track(ctx)()
	res, err := c.inner.Rerank(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) VAD(ctx context.Context, in *pb.VADRequest, opts ...ggrpc.CallOption) (*pb.VADResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.VAD(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) Diarize(ctx context.Context, in *pb.DiarizeRequest, opts ...ggrpc.CallOption) (*pb.DiarizeResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.Diarize(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) FaceVerify(ctx context.Context, in *pb.FaceVerifyRequest, opts ...ggrpc.CallOption) (*pb.FaceVerifyResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.FaceVerify(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) FaceAnalyze(ctx context.Context, in *pb.FaceAnalyzeRequest, opts ...ggrpc.CallOption) (*pb.FaceAnalyzeResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.FaceAnalyze(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) VoiceVerify(ctx context.Context, in *pb.VoiceVerifyRequest, opts ...ggrpc.CallOption) (*pb.VoiceVerifyResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.VoiceVerify(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) VoiceAnalyze(ctx context.Context, in *pb.VoiceAnalyzeRequest, opts ...ggrpc.CallOption) (*pb.VoiceAnalyzeResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.VoiceAnalyze(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) VoiceEmbed(ctx context.Context, in *pb.VoiceEmbedRequest, opts ...ggrpc.CallOption) (*pb.VoiceEmbedResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.VoiceEmbed(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) TokenClassify(ctx context.Context, in *pb.TokenClassifyRequest, opts ...ggrpc.CallOption) (*pb.TokenClassifyResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.TokenClassify(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) Score(ctx context.Context, in *pb.ScoreRequest, opts ...ggrpc.CallOption) (*pb.ScoreResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.Score(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) SoundDetection(ctx context.Context, in *pb.SoundDetectionRequest, opts ...ggrpc.CallOption) (*pb.SoundDetectionResponse, error) {
	defer c.track(ctx)()
	res, err := c.inner.SoundDetection(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) AudioEncode(ctx context.Context, in *pb.AudioEncodeRequest, opts ...ggrpc.CallOption) (*pb.AudioEncodeResult, error) {
	defer c.track(ctx)()
	res, err := c.inner.AudioEncode(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) AudioDecode(ctx context.Context, in *pb.AudioDecodeRequest, opts ...ggrpc.CallOption) (*pb.AudioDecodeResult, error) {
	defer c.track(ctx)()
	res, err := c.inner.AudioDecode(ctx, in, opts...)
	return res, c.reconcile(err)
}

func (c *InFlightTrackingClient) AudioTransform(ctx context.Context, in *pb.AudioTransformRequest, opts ...ggrpc.CallOption) (*pb.AudioTransformResult, error) {
	defer c.track(ctx)()
	res, err := c.inner.AudioTransform(ctx, in, opts...)
	return res, c.reconcile(err)
}

// AudioTransformStream, AudioToAudioStream and Forward live in grpc.ControlBackend
// and are passed through via the embedded field, NOT tracked: they return a stream
// client and the inference spans the stream's lifetime, not the constructor call.
// Wrapping the constructor with track() would increment and immediately decrement
// (and fire onFirstComplete) before any audio flows. Tracking those correctly needs
// the done() func tied to stream close, which the Backend interface doesn't surface
// here. If they ever need tracking, move them to grpc.InferenceBackend - the build
// will then force an explicit wrapper here.
