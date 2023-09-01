package model

import (
	"context"

	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
)

type Options struct {
	backendString string
	model         string
	threads       uint32
	assetDir      string
	context       context.Context

	gRPCOptions *pb.ModelOptions

	externalBackends map[string]string

	grpcAttempts        int
	grpcAttemptsDelay   int
	singleActiveBackend bool
}

type Option func(*Options)

func WithExternalBackend(name string, uri string) Option {
	return func(o *Options) {
		if o.externalBackends == nil {
			o.externalBackends = make(map[string]string)
		}
		o.externalBackends[name] = uri
	}
}

func WithGRPCAttempts(attempts int) Option {
	return func(o *Options) {
		o.grpcAttempts = attempts
	}
}

func WithGRPCAttemptsDelay(delay int) Option {
	return func(o *Options) {
		o.grpcAttemptsDelay = delay
	}
}

func WithBackendString(backend string) Option {
	return func(o *Options) {
		o.backendString = backend
	}
}

func WithModel(modelFile string) Option {
	return func(o *Options) {
		o.model = modelFile
	}
}

func WithLoadGRPCLoadModelOpts(opts *pb.ModelOptions) Option {
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

func WithSingleActiveBackend() Option {
	return func(o *Options) {
		o.singleActiveBackend = true
	}
}

func NewOptions(opts ...Option) *Options {
	o := &Options{
		gRPCOptions:       &pb.ModelOptions{},
		context:           context.Background(),
		grpcAttempts:      20,
		grpcAttemptsDelay: 2,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}
