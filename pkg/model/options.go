package model

import (
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
)

type Options struct {
	backendString string
	modelFile     string
	threads       uint32
	assetDir      string

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

func WithLoadGRPCOpts(opts *pb.ModelOptions) Option {
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

func NewOptions(opts ...Option) *Options {
	o := &Options{}
	for _, opt := range opts {
		opt(o)
	}
	return o
}
