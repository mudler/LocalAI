package model

import (
	"context"

	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
)

type ModelOptions struct {
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
	parallelRequests    bool
}

type Option func(*ModelOptions)

var EnableParallelRequests = func(o *ModelOptions) {
	o.parallelRequests = true
}

func WithExternalBackend(name string, uri string) Option {
	return func(o *ModelOptions) {
		if o.externalBackends == nil {
			o.externalBackends = make(map[string]string)
		}
		o.externalBackends[name] = uri
	}
}

// Currently, LocalAI isn't ready for backends to be yanked out from under it - so this is a little overcomplicated to allow non-overwriting updates
func WithExternalBackends(backends map[string]string, overwrite bool) Option {
	return func(o *ModelOptions) {
		if backends == nil {
			return
		}
		if o.externalBackends == nil {
			o.externalBackends = backends
			return
		}
		for name, url := range backends {
			_, exists := o.externalBackends[name]
			if !exists || overwrite {
				o.externalBackends[name] = url
			}
		}
	}
}

func WithGRPCAttempts(attempts int) Option {
	return func(o *ModelOptions) {
		o.grpcAttempts = attempts
	}
}

func WithGRPCAttemptsDelay(delay int) Option {
	return func(o *ModelOptions) {
		o.grpcAttemptsDelay = delay
	}
}

func WithBackendString(backend string) Option {
	return func(o *ModelOptions) {
		o.backendString = backend
	}
}

func WithModel(modelFile string) Option {
	return func(o *ModelOptions) {
		o.model = modelFile
	}
}

func WithLoadGRPCLoadModelOpts(opts *pb.ModelOptions) Option {
	return func(o *ModelOptions) {
		o.gRPCOptions = opts
	}
}

func WithThreads(threads uint32) Option {
	return func(o *ModelOptions) {
		o.threads = threads
	}
}

func WithAssetDir(assetDir string) Option {
	return func(o *ModelOptions) {
		o.assetDir = assetDir
	}
}

func WithContext(ctx context.Context) Option {
	return func(o *ModelOptions) {
		o.context = ctx
	}
}

func WithSingleActiveBackend() Option {
	return func(o *ModelOptions) {
		o.singleActiveBackend = true
	}
}

func NewOptions(opts ...Option) *ModelOptions {
	o := &ModelOptions{
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
