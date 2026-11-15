package model

import (
	"context"
	"maps"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/protobuf/proto"
)

type Options struct {
	backendString  string
	model          string
	modelFile      string
	modelID        string
	configRevision string
	context        context.Context

	gRPCOptions *pb.ModelOptions

	// gRPCOptionsFinalizer runs on a copy of gRPCOptions right before a real
	// load in this process, never on a cache hit and never in distributed
	// mode (the router completes the options when it sends a load). It lets
	// the caller add a value that is costly to fetch or must not sit in the
	// options for long, such as a credential.
	gRPCOptionsFinalizer func(context.Context, *pb.ModelOptions) error

	externalBackends map[string]string

	grpcAttempts      int
	grpcAttemptsDelay int
	parallelRequests  bool

	// modelSizeBytes is the estimated total weight size in bytes, pre-computed
	// by the caller using the vram estimation scaffolding.  When non-zero it is
	// registered with the watchdog so size-aware eviction can rank models.
	modelSizeBytes int64

	// envVars contains model-specific environment variables to pass to the backend process
	envVars map[string]string
}

// WithConfigRevision binds a load to the semantic revision of the resolved
// model configuration. The value is copied into Options and therefore remains
// stable even if the configuration loader refreshes while a load is running.
func WithConfigRevision(revision string) Option {
	return func(o *Options) { o.configRevision = revision }
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

// WithLoadGRPCOptionsFinalizer sets a function that completes the load options
// just before a real load. If it fails, the load fails with its error.
func WithLoadGRPCOptionsFinalizer(fn func(context.Context, *pb.ModelOptions) error) Option {
	return func(o *Options) {
		o.gRPCOptionsFinalizer = fn
	}
}

// finalized returns the options to use for one real load: o itself when there
// is no finalizer, else a copy that holds a finalized clone of the load
// options.
func (o *Options) finalized() (*Options, error) {
	if o.gRPCOptionsFinalizer == nil || o.gRPCOptions == nil {
		return o, nil
	}
	cp := *o
	cp.gRPCOptions = proto.Clone(o.gRPCOptions).(*pb.ModelOptions)
	if err := o.gRPCOptionsFinalizer(o.context, cp.gRPCOptions); err != nil {
		return nil, err
	}
	return &cp, nil
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

func WithEnvVars(envVars map[string]string) Option {
	snapshot := maps.Clone(envVars)
	return func(o *Options) {
		o.envVars = maps.Clone(snapshot)
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
