package grpc

import (
	"context"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type AIModel interface {
	Busy() bool
	Lock()
	Unlock()
	Locking() bool
	Predict(*pb.PredictOptions) (string, error)
	PredictStream(*pb.PredictOptions, chan string) error
	Load(*pb.ModelOptions) error
	Free() error
	Embeddings(*pb.PredictOptions) ([]float32, error)
	GenerateImage(*pb.GenerateImageRequest) error
	GenerateVideo(*pb.GenerateVideoRequest) error
	Detect(*pb.DetectOptions) (pb.DetectResponse, error)
	Depth(*pb.DepthRequest) (pb.DepthResponse, error)
	FaceVerify(*pb.FaceVerifyRequest) (pb.FaceVerifyResponse, error)
	FaceAnalyze(*pb.FaceAnalyzeRequest) (pb.FaceAnalyzeResponse, error)
	VoiceVerify(*pb.VoiceVerifyRequest) (pb.VoiceVerifyResponse, error)
	VoiceAnalyze(*pb.VoiceAnalyzeRequest) (pb.VoiceAnalyzeResponse, error)
	VoiceEmbed(*pb.VoiceEmbedRequest) (pb.VoiceEmbedResponse, error)
	AudioTranscription(context.Context, *pb.TranscriptRequest) (pb.TranscriptResult, error)
	AudioTranscriptionStream(context.Context, *pb.TranscriptRequest, chan *pb.TranscriptStreamResponse) error
	TTS(*pb.TTSRequest) error
	TTSStream(*pb.TTSRequest, chan []byte) error
	SoundGeneration(*pb.SoundGenerationRequest) error
	TokenizeString(*pb.PredictOptions) (pb.TokenizationResponse, error)
	Detokenize(*pb.DetokenizeRequest) (pb.DetokenizeResponse, error)
	Status() (pb.StatusResponse, error)

	StoresSet(*pb.StoresSetOptions) error
	StoresDelete(*pb.StoresDeleteOptions) error
	StoresGet(*pb.StoresGetOptions) (pb.StoresGetResult, error)
	StoresFind(*pb.StoresFindOptions) (pb.StoresFindResult, error)

	VAD(*pb.VADRequest) (pb.VADResponse, error)
	Diarize(*pb.DiarizeRequest) (pb.DiarizeResponse, error)

	AudioEncode(*pb.AudioEncodeRequest) (*pb.AudioEncodeResult, error)
	AudioDecode(*pb.AudioDecodeRequest) (*pb.AudioDecodeResult, error)

	AudioTransform(*pb.AudioTransformRequest) (*pb.AudioTransformResult, error)
	AudioTransformStream(in <-chan *pb.AudioTransformFrameRequest, out chan<- *pb.AudioTransformFrameResponse) error
	AudioToAudioStream(in <-chan *pb.AudioToAudioRequest, out chan<- *pb.AudioToAudioResponse) error

	// Forward proxies a raw HTTP request to an upstream provider for
	// passthrough-mode cloud-proxy backends. ctx is the gRPC stream
	// context — cancellation propagates to the upstream HTTP request
	// so client disconnect closes the upstream connection.
	Forward(ctx context.Context, in <-chan *pb.ForwardRequest, out chan<- *pb.ForwardReply) error

	ModelMetadata(*pb.ModelOptions) (*pb.ModelMetadataResponse, error)

	// Fine-tuning
	StartFineTune(*pb.FineTuneRequest) (*pb.FineTuneJobResult, error)
	FineTuneProgress(*pb.FineTuneProgressRequest, chan *pb.FineTuneProgressUpdate) error
	StopFineTune(*pb.FineTuneStopRequest) error
	ListCheckpoints(*pb.ListCheckpointsRequest) (*pb.ListCheckpointsResponse, error)
	ExportModel(*pb.ExportModelRequest) error

	// Quantization
	StartQuantization(*pb.QuantizationRequest) (*pb.QuantizationJobResult, error)
	QuantizationProgress(*pb.QuantizationProgressRequest, chan *pb.QuantizationProgressUpdate) error
	StopQuantization(*pb.QuantizationStopRequest) error
}

func newReply(s string) *pb.Reply {
	return &pb.Reply{Message: []byte(s)}
}

// AIModelRich is an optional extension to AIModel for backends that
// can produce a full *pb.Reply — including tool-call deltas and
// usage tokens — rather than just a content string. The gRPC server
// type-asserts and prefers the rich path when implemented; otherwise
// it wraps Predict's string return in a Reply.
//
// Cloud-proxy translate mode is the motivating use case: the upstream
// emits structured tool_calls that would be lost through the legacy
// (string, error) signature.
//
// PredictStreamRich contract: send replies into the channel and
// return when finished. Do NOT close the channel — the server closes
// it after the call returns. This is opposite to legacy PredictStream
// which expects the impl to defer close().
type AIModelRich interface {
	PredictRich(*pb.PredictOptions) (*pb.Reply, error)
	PredictStreamRich(*pb.PredictOptions, chan<- *pb.Reply) error
}
