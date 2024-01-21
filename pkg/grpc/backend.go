package grpc

import (
	"context"
	"github.com/go-skynet/LocalAI/api/schema"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
)

var embeds = map[string]*embedBackend{}

func Provide(addr string, llm LLM) {
	embeds[addr] = &embedBackend{s: &server{llm: llm}}
}

func NewClient(address string, parallel bool, wd WatchDog, enableWatchDog bool) Backend {
	if bc, ok := embeds[address]; ok {
		return bc
	}
	return NewGrpcClient(address, parallel, wd, enableWatchDog)
}

func NewGrpcClient(address string, parallel bool, wd WatchDog, enableWatchDog bool) Backend {
	if !enableWatchDog {
		wd = nil
	}
	return &Client{
		address:  address,
		parallel: parallel,
		wd:       wd,
	}
}

type Backend interface {
	IsBusy() bool
	HealthCheck(ctx context.Context) (bool, error)
	Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.EmbeddingResult, error)
	Predict(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.Reply, error)
	LoadModel(ctx context.Context, in *pb.ModelOptions, opts ...grpc.CallOption) (*pb.Result, error)
	PredictStream(ctx context.Context, in *pb.PredictOptions, f func(s []byte), opts ...grpc.CallOption) error
	GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...grpc.CallOption) (*pb.Result, error)
	TTS(ctx context.Context, in *pb.TTSRequest, opts ...grpc.CallOption) (*pb.Result, error)
	AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...grpc.CallOption) (*schema.Result, error)
	TokenizeString(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.TokenizationResponse, error)
	Status(ctx context.Context) (*pb.StatusResponse, error)
}
