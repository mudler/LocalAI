package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TimestralCpp implements the Forecast RPC of the Backend gRPC service over
// libtimestral.so. The C handle is not reentrant: the server serializes calls
// through base.SingleThread, and mu repeats that guard for Forecast and Free so
// the handle is safe even when the type is used without the server.
type TimestralCpp struct {
	base.SingleThread

	mu     sync.Mutex
	ctxPtr uintptr
	opts   loadOptions
	levels []float64 // quantile levels of the loaded model, ascending
}

func (t *TimestralCpp) Load(opts *pb.ModelOptions) error {
	model := opts.ModelFile
	if model == "" {
		model = opts.ModelPath
	}
	if !filepath.IsAbs(model) && opts.ModelPath != "" && model != opts.ModelPath {
		model = filepath.Join(opts.ModelPath, model)
	}
	if model == "" {
		return fmt.Errorf("timestral-cpp: ModelFile is required")
	}
	if st, err := os.Stat(model); err != nil || st.IsDir() {
		return fmt.Errorf("timestral-cpp: model file %q is not readable", model)
	}

	if v := CppAbiVersion(); v != abiVersionRequired {
		return fmt.Errorf("timestral-cpp: libtimestral ABI version %d, this backend needs %d", v, abiVersionRequired)
	}

	threads := opts.Threads
	if threads <= 0 {
		threads = defaultThreads
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.freeLocked()

	// last_error(NULL) is thread-local, and a goroutine may move to another OS
	// thread between two calls into C. Pin the thread across the load and the
	// error read.
	runtime.LockOSThread()
	ctx := CppLoad(model, threads)
	if ctx == 0 {
		msg := CppLastError(0)
		runtime.UnlockOSThread()
		return fmt.Errorf("timestral-cpp: load %q failed: %s", filepath.Base(model), msg)
	}
	runtime.UnlockOSThread()

	n := int(CppNumQuantiles(ctx))
	if n <= 0 {
		msg := CppLastError(ctx)
		CppFree(ctx)
		return fmt.Errorf("timestral-cpp: model reports no quantile levels: %s", msg)
	}
	levels := make([]float64, n)
	for i := range levels {
		levels[i] = CppQuantileLevel(ctx, int32(i))
	}

	t.ctxPtr = ctx
	t.levels = levels
	t.opts = parseOptions(opts.Options)
	xlog.Info("timestral-cpp: model loaded", "model", filepath.Base(model), "threads", threads,
		"quantile_levels", len(levels), "abi", abiVersionRequired)
	return nil
}

func (t *TimestralCpp) Free() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.freeLocked()
	return nil
}

func (t *TimestralCpp) freeLocked() {
	if t.ctxPtr != 0 {
		CppFree(t.ctxPtr)
		t.ctxPtr = 0
	}
	t.levels = nil
}

// Forecast groups the series by shape, runs one ABI call per group and returns
// the results in the order the series were sent.
func (t *TimestralCpp) Forecast(req *pb.ForecastRequest) (pb.ForecastResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ctxPtr == 0 {
		return pb.ForecastResponse{}, status.Error(codes.FailedPrecondition, "timestral-cpp: no model loaded")
	}
	if req.GetPredictionLength() < 1 {
		return pb.ForecastResponse{}, invalidArgument("prediction_length must be at least 1")
	}
	if req.GetMaxContext() < 0 {
		return pb.ForecastResponse{}, invalidArgument("max_context must not be negative")
	}
	h := req.GetPredictionLength()
	if err := validateSeries(req.GetSeries(), h, t.opts); err != nil {
		return pb.ForecastResponse{}, err
	}
	sel, levels, err := selectQuantiles(req.GetQuantiles(), t.opts.defaultQuantiles, t.levels)
	if err != nil {
		return pb.ForecastResponse{}, err
	}

	maxContext := req.GetMaxContext()
	if maxContext == 0 {
		maxContext = t.opts.maxContext
	}

	results := make([]*pb.ForecastSeriesResult, len(req.GetSeries()))
	for _, g := range planGroups(req.GetSeries()) {
		out, err := t.forecastGroup(req, g, h, maxContext)
		if err != nil {
			return pb.ForecastResponse{}, err
		}
		for i, r := range reshape(out, req.GetSeries(), g, int(h), len(t.levels), sel) {
			results[i] = r
		}
	}

	return pb.ForecastResponse{PredictionLength: h, QuantileLevels: levels, Series: results}, nil
}

// flagValue maps an optional bool to the ABI flag: -1 model default, 0 or 1.
func flagValue(b *bool) int32 {
	switch {
	case b == nil:
		return -1
	case *b:
		return 1
	default:
		return 0
	}
}

func floatPtr(s []float32) unsafe.Pointer {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Pointer(&s[0])
}

func doublePtr(s []float64) unsafe.Pointer {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Pointer(&s[0])
}

// forecastGroup runs one ABI call and returns a Go copy of the [B,V,H,Q]
// output. The C buffer is freed before it returns.
func (t *TimestralCpp) forecastGroup(req *pb.ForecastRequest, g group, h, maxContext int32) ([]float32, error) {
	b := buildBatch(req.GetSeries(), g)
	cr := forecastRequest{
		context:       floatPtr(b.context),
		B:             int32(len(g.idx)),
		V:             g.shape.V,
		T:             g.shape.T,
		predictionLen: h,
		pastCov:       floatPtr(b.pastCov),
		cPast:         g.shape.cPast,
		knownCov:      floatPtr(b.knownCov),
		cKnown:        g.shape.cKnown,
		catPast:       doublePtr(b.catPast),
		ccPast:        g.shape.ccPast,
		catKnown:      doublePtr(b.catKnown),
		ccKnown:       g.shape.ccKnown,
		symmetricAvg:  flagValue(req.SymmetricAveraging),
		clipNonneg:    flagValue(req.ClipNonnegative),
		maxContext:    maxContext,
	}

	var q int32
	ptr := CppForecast(t.ctxPtr, unsafe.Pointer(&cr), unsafe.Pointer(&q))
	// The buffers behind cr must outlive the call: they are only reachable from
	// the C side while it runs.
	runtime.KeepAlive(b)
	runtime.KeepAlive(&cr)
	if ptr == 0 {
		// The ABI rejects what it cannot serve (for example covariates the model
		// has no input for), so the message is about the request.
		return nil, invalidArgument("forecast failed: %s", CppLastError(t.ctxPtr))
	}
	defer CppFreeBuffer(ptr)

	if int(q) != len(t.levels) {
		return nil, status.Errorf(codes.Internal, "timestral-cpp: library returned %d quantiles, model reports %d", q, len(t.levels))
	}
	n := len(g.idx) * int(g.shape.V) * int(h) * int(q)
	src := unsafe.Slice((*float32)(unsafe.Pointer(ptr)), n) //nolint:govet // C-owned malloc'd buffer, not Go-GC memory; freed by CppFreeBuffer above
	out := make([]float32, n)
	copy(out, src)
	return out, nil
}
