package grpc

import (
	"context"
	"errors"
	"net"

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

// NewClientWithDialer creates a gRPC client that reaches its backend through
// dialer and not through a TCP connection to address. The tunnel carrier uses
// it, and the address is then the name of a backend process of a worker.
//
// The outcome of every dial is recorded, so that a caller can ask why a call
// failed: see LastDialErrorOf.
func NewClientWithDialer(address string, parallel bool, wd WatchDog, enableWatchDog bool, token string, dialer func(ctx context.Context, addr string) (net.Conn, error)) Backend {
	if bc, ok := embeds[address]; ok {
		return bc
	}
	// Assigned on the concrete type and not through an assertion that can fail:
	// a failed assertion would hand back a client that dials the address
	// directly, which is the bypass this constructor exists to close.
	c := buildClient(address, parallel, wd, enableWatchDog, token)
	c.dialer = func(ctx context.Context, addr string) (net.Conn, error) {
		c.startDial()
		conn, err := dialer(ctx, addr)
		c.recordDialErr(err)
		return conn, err
	}
	return c
}

// DialErrorReporter is implemented by a Backend that reaches its process through
// a custom transport and can say whether the transport failed. It is not part of
// Backend: the few callers that act on the difference ask for it, and widening
// Backend would make every wrapper and every double implement a method that they
// have no answer for.
type DialErrorReporter interface {
	LastDialError() error
}

// ErrDialPending is what LastDialError reports while a dial has started and has
// not finished. It is a failure of the transport and not an answer of the host:
// the caller knows nothing about the backend yet.
var ErrDialPending = errors.New("grpc: the dial to the backend has not finished")

// BackendAnswer is implemented by the error of a custom dialer when the dial
// reached the host of the backend and the host answered. A refusal of the worker
// to open a stream to a backend that is gone is one: it is evidence about the
// backend, like a refused connection on a direct dial. An error that does not
// implement it is a failure of the transport, and nothing was learned about the
// backend.
type BackendAnswer interface {
	error
	IsBackendAnswer() bool
}

// BackendUnwrapper is implemented by a Backend that decorates another one.
// Every decorator must implement it: a decorator that embeds the Backend
// interface inherits what Backend declares and nothing else, and
// DialErrorReporter is not declared there. A wrapped client would then stop
// answering whether the transport failed, and the guards built on the answer
// would read nil in production while every spec that built a raw client passed.
type BackendUnwrapper interface {
	Unwrap() Backend
}

// maxBackendUnwrapDepth bounds the walk of LastDialErrorOf. It guards against a
// cycle that a future wrapper could introduce, and nothing real nests this deep.
const maxBackendUnwrapDepth = 16

// LastDialErrorOf reports why the most recent dial under b failed, looking
// through any decorators, or nil when the dial succeeded or nothing under b has a
// custom transport.
func LastDialErrorOf(b Backend) error {
	for range maxBackendUnwrapDepth {
		if b == nil {
			return nil
		}
		if reporter, ok := b.(DialErrorReporter); ok {
			return reporter.LastDialError()
		}
		wrapper, ok := b.(BackendUnwrapper)
		if !ok {
			return nil
		}
		b = wrapper.Unwrap()
	}
	return nil
}

// TransportFailureOf is LastDialErrorOf for a caller that decides whether a
// backend is dead. It returns the error of the last dial when that error is a
// failure of the transport, and nil when the dial succeeded or when the host
// answered about the backend (see BackendAnswer).
//
// A caller that gets an error from it must not conclude anything about the
// backend: not that it is dead, and not that it is well. It must not reap the
// row of a model, evict a cached model or count a miss.
func TransportFailureOf(b Backend) error {
	err := LastDialErrorOf(b)
	if err == nil {
		return nil
	}
	var answer BackendAnswer
	if errors.As(err, &answer) && answer.IsBackendAnswer() {
		return nil
	}
	return err
}

func buildClient(address string, parallel bool, wd WatchDog, enableWatchDog bool, token string) *Client {
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
	UpscaleImage(ctx context.Context, in *pb.UpscaleImageRequest, opts ...grpc.CallOption) (*pb.Result, error)
	GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest, opts ...grpc.CallOption) (*pb.Result, error)
	Generate3D(ctx context.Context, in *pb.Generate3DRequest, opts ...grpc.CallOption) (*pb.Result, error)
	Animate3D(ctx context.Context, in *pb.Animate3DRequest, opts ...grpc.CallOption) (*pb.Result, error)
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
	Detokenize(ctx context.Context, in *pb.DetokenizeRequest, opts ...grpc.CallOption) (*pb.DetokenizeResponse, error)
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
	AudioTranscriptionLive(ctx context.Context, opts ...grpc.CallOption) (AudioTranscriptionLiveClient, error)

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
