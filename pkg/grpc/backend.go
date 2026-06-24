package grpc

import (
	"context"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
)

var embeds = map[string]*embedBackend{}

func Provide(addr string, llm AIModel) {
	embeds[addr] = &embedBackend{s: &server{llm: llm}}
}

func NewClient(address string, parallel bool, wd WatchDog, enableWatchDog bool) Backend {
	if bc, ok := embeds[address]; ok {
		return bc
	}
	return buildClient(address, parallel, wd, enableWatchDog, "")
}

// NewClientWithToken creates a gRPC client that sends a bearer token with every call.
// Used in distributed mode to authenticate with remote backend processes.
func NewClientWithToken(address string, parallel bool, wd WatchDog, enableWatchDog bool, token string) Backend {
	if bc, ok := embeds[address]; ok {
		return bc
	}
	return buildClient(address, parallel, wd, enableWatchDog, token)
}

func buildClient(address string, parallel bool, wd WatchDog, enableWatchDog bool, token string) Backend {
	if !enableWatchDog {
		wd = nil
	}
	return &Client{
		address:  address,
		parallel: parallel,
		wd:       wd,
		token:    token,
	}
}

// Backend is the full client surface of a model backend. It is deliberately
// composed of two sub-interfaces so that wrappers can get a COMPILE-TIME
// guarantee about which methods they must account for:
//
//   - InferenceBackend - methods that each perform one discrete inference call
//     (the call begins on entry and ends on return). A wrapper that does
//     per-call accounting - e.g. the distributed router's in-flight tracker,
//     core/services/nodes.InFlightTrackingClient - embeds only ControlBackend
//     and implements every InferenceBackend method explicitly. Adding a method
//     to InferenceBackend therefore breaks that wrapper's build until it is
//     implemented: inference can't be added without an accounting decision.
//   - ControlBackend - everything that is NOT a discrete inference call:
//     lifecycle/control-plane operations and the streaming constructors whose
//     work spans the returned stream rather than the constructor call. These
//     are safe to pass through untracked.
//
// Keep the two sets disjoint; every backend method belongs to exactly one.
type Backend interface {
	InferenceBackend
	ControlBackend
}

// InferenceBackend is the subset of Backend whose methods each map to a single
// inference call. Wrappers that account for in-flight work must implement these
// explicitly (see Backend). Do NOT add methods that return a stream client or
// that are control-plane only - those belong in ControlBackend.
type InferenceBackend interface {
	Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.EmbeddingResult, error)
	PredictStream(ctx context.Context, in *pb.PredictOptions, f func(reply *pb.Reply), opts ...grpc.CallOption) error
	Predict(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.Reply, error)
	GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...grpc.CallOption) (*pb.Result, error)
	GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest, opts ...grpc.CallOption) (*pb.Result, error)
	TTS(ctx context.Context, in *pb.TTSRequest, opts ...grpc.CallOption) (*pb.Result, error)
	TTSStream(ctx context.Context, in *pb.TTSRequest, f func(reply *pb.Reply), opts ...grpc.CallOption) error
	SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, opts ...grpc.CallOption) (*pb.Result, error)
	AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...grpc.CallOption) (*pb.TranscriptResult, error)
	AudioTranscriptionStream(ctx context.Context, in *pb.TranscriptRequest, f func(chunk *pb.TranscriptStreamResponse), opts ...grpc.CallOption) error
	Detect(ctx context.Context, in *pb.DetectOptions, opts ...grpc.CallOption) (*pb.DetectResponse, error)
	Depth(ctx context.Context, in *pb.DepthRequest, opts ...grpc.CallOption) (*pb.DepthResponse, error)
	FaceVerify(ctx context.Context, in *pb.FaceVerifyRequest, opts ...grpc.CallOption) (*pb.FaceVerifyResponse, error)
	FaceAnalyze(ctx context.Context, in *pb.FaceAnalyzeRequest, opts ...grpc.CallOption) (*pb.FaceAnalyzeResponse, error)
	VoiceVerify(ctx context.Context, in *pb.VoiceVerifyRequest, opts ...grpc.CallOption) (*pb.VoiceVerifyResponse, error)
	VoiceAnalyze(ctx context.Context, in *pb.VoiceAnalyzeRequest, opts ...grpc.CallOption) (*pb.VoiceAnalyzeResponse, error)
	VoiceEmbed(ctx context.Context, in *pb.VoiceEmbedRequest, opts ...grpc.CallOption) (*pb.VoiceEmbedResponse, error)
	Rerank(ctx context.Context, in *pb.RerankRequest, opts ...grpc.CallOption) (*pb.RerankResult, error)
	TokenClassify(ctx context.Context, in *pb.TokenClassifyRequest, opts ...grpc.CallOption) (*pb.TokenClassifyResponse, error)
	Score(ctx context.Context, in *pb.ScoreRequest, opts ...grpc.CallOption) (*pb.ScoreResponse, error)
	VAD(ctx context.Context, in *pb.VADRequest, opts ...grpc.CallOption) (*pb.VADResponse, error)
	Diarize(ctx context.Context, in *pb.DiarizeRequest, opts ...grpc.CallOption) (*pb.DiarizeResponse, error)
	SoundDetection(ctx context.Context, in *pb.SoundDetectionRequest, opts ...grpc.CallOption) (*pb.SoundDetectionResponse, error)
	AudioEncode(ctx context.Context, in *pb.AudioEncodeRequest, opts ...grpc.CallOption) (*pb.AudioEncodeResult, error)
	AudioDecode(ctx context.Context, in *pb.AudioDecodeRequest, opts ...grpc.CallOption) (*pb.AudioDecodeResult, error)
	AudioTransform(ctx context.Context, in *pb.AudioTransformRequest, opts ...grpc.CallOption) (*pb.AudioTransformResult, error)
}

// ControlBackend is the subset of Backend that is NOT per-call inference:
// lifecycle/control-plane operations and the streaming constructors whose work
// spans the returned stream rather than the constructor call. In-flight-tracking
// wrappers embed this directly and pass it through untracked (see Backend).
type ControlBackend interface {
	IsBusy() bool
	HealthCheck(ctx context.Context) (bool, error)
	LoadModel(ctx context.Context, in *pb.ModelOptions, opts ...grpc.CallOption) (*pb.Result, error)
	TokenizeString(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.TokenizationResponse, error)
	Status(ctx context.Context) (*pb.StatusResponse, error)

	StoresSet(ctx context.Context, in *pb.StoresSetOptions, opts ...grpc.CallOption) (*pb.Result, error)
	StoresDelete(ctx context.Context, in *pb.StoresDeleteOptions, opts ...grpc.CallOption) (*pb.Result, error)
	StoresGet(ctx context.Context, in *pb.StoresGetOptions, opts ...grpc.CallOption) (*pb.StoresGetResult, error)
	StoresFind(ctx context.Context, in *pb.StoresFindOptions, opts ...grpc.CallOption) (*pb.StoresFindResult, error)

	GetTokenMetrics(ctx context.Context, in *pb.MetricsRequest, opts ...grpc.CallOption) (*pb.MetricsResponse, error)

	// Streaming constructors: these return a stream client immediately; the
	// actual inference spans the stream's lifetime, not this call, so they are
	// NOT tracked as a single in-flight unit.
	AudioTransformStream(ctx context.Context, opts ...grpc.CallOption) (AudioTransformStreamClient, error)
	AudioToAudioStream(ctx context.Context, opts ...grpc.CallOption) (AudioToAudioStreamClient, error)

	// Forward proxies a raw HTTP request to an upstream provider for
	// passthrough-mode cloud-proxy backends. Caller streams a single
	// ForwardRequest carrying path/method/headers/body, then closes
	// send; backend streams back status/headers in the first reply
	// and body chunks thereafter.
	Forward(ctx context.Context, opts ...grpc.CallOption) (ForwardClient, error)

	ModelMetadata(ctx context.Context, in *pb.ModelOptions, opts ...grpc.CallOption) (*pb.ModelMetadataResponse, error)

	// Fine-tuning
	StartFineTune(ctx context.Context, in *pb.FineTuneRequest, opts ...grpc.CallOption) (*pb.FineTuneJobResult, error)
	FineTuneProgress(ctx context.Context, in *pb.FineTuneProgressRequest, f func(update *pb.FineTuneProgressUpdate), opts ...grpc.CallOption) error
	StopFineTune(ctx context.Context, in *pb.FineTuneStopRequest, opts ...grpc.CallOption) (*pb.Result, error)
	ListCheckpoints(ctx context.Context, in *pb.ListCheckpointsRequest, opts ...grpc.CallOption) (*pb.ListCheckpointsResponse, error)
	ExportModel(ctx context.Context, in *pb.ExportModelRequest, opts ...grpc.CallOption) (*pb.Result, error)

	// Quantization
	StartQuantization(ctx context.Context, in *pb.QuantizationRequest, opts ...grpc.CallOption) (*pb.QuantizationJobResult, error)
	QuantizationProgress(ctx context.Context, in *pb.QuantizationProgressRequest, f func(update *pb.QuantizationProgressUpdate), opts ...grpc.CallOption) error
	StopQuantization(ctx context.Context, in *pb.QuantizationStopRequest, opts ...grpc.CallOption) (*pb.Result, error)

	// Free releases GPU/model resources (e.g. VRAM) without stopping the process.
	Free(ctx context.Context) error
}
