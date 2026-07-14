package model

import (
	"context"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type Options struct {
	backendString string
	model         string
	modelFile     string
	modelID       string
	context       context.Context

	gRPCOptions *pb.ModelOptions

	externalBackends map[string]string

	grpcAttempts      int
	grpcAttemptsDelay int
	parallelRequests  bool

	// modelSizeBytes is the estimated total weight size in bytes, pre-computed
	// by the caller using the vram estimation scaffolding.  When non-zero it is
	// registered with the watchdog so size-aware eviction can rank models.
	modelSizeBytes int64
}

type Option func(*Options)

var EnableParallelRequests = func(o *Options) {
	o.parallelRequests = true
}

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

func WithDefaultBackendString(backend string) Option {
	return func(o *Options) {
		if o.backendString == "" {
			o.backendString = backend
		}
	}
}

func WithModel(modelFile string) Option {
	return func(o *Options) {
		o.model = modelFile
	}
}

func WithModelFile(modelFile string) Option {
	return func(o *Options) {
		o.modelFile = modelFile
	}
}

func WithLoadGRPCLoadModelOpts(opts *pb.ModelOptions) Option {
	return func(o *Options) {
		o.gRPCOptions = opts
	}
}

func WithContext(ctx context.Context) Option {
	return func(o *Options) {
		o.context = ctx
	}
}

func WithModelID(id string) Option {
	return func(o *Options) {
		o.modelID = id
	}
}

func WithModelSizeBytes(bytes int64) Option {
	return func(o *Options) {
		o.modelSizeBytes = bytes
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
