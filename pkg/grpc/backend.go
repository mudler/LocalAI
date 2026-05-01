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

type Backend interface {
	IsBusy() bool
	HealthCheck(ctx context.Context) (bool, error)
	Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.EmbeddingResult, error)
	LoadModel(ctx context.Context, in *pb.ModelOptions, opts ...grpc.CallOption) (*pb.Result, error)
	PredictStream(ctx context.Context, in *pb.PredictOptions, f func(reply *pb.Reply), opts ...grpc.CallOption) error
	Predict(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.Reply, error)
	GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...grpc.CallOption) (*pb.Result, error)
	GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest, opts ...grpc.CallOption) (*pb.Result, error)
	TTS(ctx context.Context, in *pb.TTSRequest, opts ...grpc.CallOption) (*pb.Result, error)
	TTSStream(ctx context.Context, in *pb.TTSRequest, f func(reply *pb.Reply), opts ...grpc.CallOption) error
	SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, opts ...grpc.CallOption) (*pb.Result, error)
	Detect(ctx context.Context, in *pb.DetectOptions, opts ...grpc.CallOption) (*pb.DetectResponse, error)
	FaceVerify(ctx context.Context, in *pb.FaceVerifyRequest, opts ...grpc.CallOption) (*pb.FaceVerifyResponse, error)
	FaceAnalyze(ctx context.Context, in *pb.FaceAnalyzeRequest, opts ...grpc.CallOption) (*pb.FaceAnalyzeResponse, error)
	VoiceVerify(ctx context.Context, in *pb.VoiceVerifyRequest, opts ...grpc.CallOption) (*pb.VoiceVerifyResponse, error)
	VoiceAnalyze(ctx context.Context, in *pb.VoiceAnalyzeRequest, opts ...grpc.CallOption) (*pb.VoiceAnalyzeResponse, error)
	VoiceEmbed(ctx context.Context, in *pb.VoiceEmbedRequest, opts ...grpc.CallOption) (*pb.VoiceEmbedResponse, error)
	AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...grpc.CallOption) (*pb.TranscriptResult, error)
	AudioTranscriptionStream(ctx context.Context, in *pb.TranscriptRequest, f func(chunk *pb.TranscriptStreamResponse), opts ...grpc.CallOption) error
	TokenizeString(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.TokenizationResponse, error)
	Status(ctx context.Context) (*pb.StatusResponse, error)

	StoresSet(ctx context.Context, in *pb.StoresSetOptions, opts ...grpc.CallOption) (*pb.Result, error)
	StoresDelete(ctx context.Context, in *pb.StoresDeleteOptions, opts ...grpc.CallOption) (*pb.Result, error)
	StoresGet(ctx context.Context, in *pb.StoresGetOptions, opts ...grpc.CallOption) (*pb.StoresGetResult, error)
	StoresFind(ctx context.Context, in *pb.StoresFindOptions, opts ...grpc.CallOption) (*pb.StoresFindResult, error)

	Rerank(ctx context.Context, in *pb.RerankRequest, opts ...grpc.CallOption) (*pb.RerankResult, error)

	GetTokenMetrics(ctx context.Context, in *pb.MetricsRequest, opts ...grpc.CallOption) (*pb.MetricsResponse, error)

	VAD(ctx context.Context, in *pb.VADRequest, opts ...grpc.CallOption) (*pb.VADResponse, error)

	AudioEncode(ctx context.Context, in *pb.AudioEncodeRequest, opts ...grpc.CallOption) (*pb.AudioEncodeResult, error)
	AudioDecode(ctx context.Context, in *pb.AudioDecodeRequest, opts ...grpc.CallOption) (*pb.AudioDecodeResult, error)

	AudioTransform(ctx context.Context, in *pb.AudioTransformRequest, opts ...grpc.CallOption) (*pb.AudioTransformResult, error)
	AudioTransformStream(ctx context.Context, opts ...grpc.CallOption) (AudioTransformStreamClient, error)

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
