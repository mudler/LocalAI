package model

import (
	"context"

	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
)

type Options struct {
	backendString string
	modelFile     string
	threads       uint32
	assetDir      string
	context       context.Context

	gRPCOptions *pb.ModelOptions
}

type Option func(*Options)

func WithBackendString(backend string) Option {
	return func(o *Options) {
		o.backendString = backend
	}
}

func WithModelFile(modelFile string) Option {
	return func(o *Options) {
		o.modelFile = modelFile
	}
}

func WithLoadGRPCLLMModelOpts(opts *pb.ModelOptions) Option {
	return func(o *Options) {
		o.gRPCOptions = opts
	}
}

func WithThreads(threads uint32) Option {
	return func(o *Options) {
		o.threads = threads
	}
}

func WithAssetDir(assetDir string) Option {
	return func(o *Options) {
		o.assetDir = assetDir
	}
}

func WithContext(ctx context.Context) Option {
	return func(o *Options) {
		o.context = ctx
	}
}

func NewOptions(opts ...Option) *Options {
	o := &Options{
		gRPCOptions: &pb.ModelOptions{},
		context:     context.Background(),
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}
