package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

const (
	backendName = "localai-proxy"

	// realtimePipelineOption names the upstream realtime pipeline that serves
	// live transcription sessions (options: ["realtime_pipeline:<name>"]).
	realtimePipelineOption = "realtime_pipeline:"
)

// LocalAIProxy serves backend methods by calling a remote LocalAI's REST API.
// base.SingleThread is not embedded: every call is an independent HTTP
// request, so serialising them would only add latency.
type LocalAIProxy struct {
	base.Base

	cfg    atomic.Pointer[proxyConfig]
	client *http.Client
}

type proxyConfig struct {
	base             string // upstream base URL without a trailing slash
	upstreamModel    string // model name sent upstream
	apiKey           string
	realtimePipeline string
	timeout          time.Duration // per-request limit for non-streaming calls; 0 = none
}

func NewLocalAIProxy() *LocalAIProxy {
	// httpclient.New refuses redirects: the upstream is one configured
	// LocalAI, so a 3xx means misconfiguration or a hijacked host, and
	// following it would replay the bearer key to an unvetted host. It also
	// sets no body deadline, so long SSE streams are not cut short.
	return &LocalAIProxy{client: httpclient.New()}
}

// Load refuses a model without proxy options so greedy backend probing,
// which tries every installed backend on a model file, never selects it.
func (p *LocalAIProxy) Load(opts *pb.ModelOptions) error {
	po := opts.GetProxy()
	if po == nil {
		return errors.New("localai-proxy: Load requires proxy options (proxy.upstream_url)")
	}
	raw := po.GetUpstreamUrl()
	if raw == "" {
		return errors.New("localai-proxy: proxy.upstream_url is required")
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("localai-proxy: proxy.upstream_url %q invalid: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("localai-proxy: proxy.upstream_url %q must be an http(s) URL with a host", raw)
	}

	// There is no translate mode: the upstream always speaks LocalAI's API.
	if po.GetMode() != "" || po.GetProvider() != "" {
		xlog.Warn("localai-proxy: proxy.mode and proxy.provider are ignored",
			"mode", po.GetMode(), "provider", po.GetProvider())
	}

	key, err := resolveAPIKey(po.GetApiKeyEnv(), po.GetApiKeyFile())
	if err != nil {
		return err
	}

	model := po.GetUpstreamModel()
	if model == "" {
		model = opts.GetModel()
	}
	if model == "" {
		xlog.Warn("localai-proxy: no upstream model name; set proxy.upstream_model")
	}

	var pipeline string
	for _, o := range opts.GetOptions() {
		if v, ok := strings.CutPrefix(o, realtimePipelineOption); ok {
			pipeline = strings.TrimSpace(v)
		}
	}

	var timeout time.Duration
	if s := po.GetRequestTimeoutSeconds(); s > 0 {
		timeout = time.Duration(s) * time.Second
	}

	p.cfg.Store(&proxyConfig{
		base:             strings.TrimRight(raw, "/"),
		upstreamModel:    model,
		apiKey:           key,
		realtimePipeline: pipeline,
		timeout:          timeout,
	})
	xlog.Info("localai-proxy: ready", "upstream", raw, "upstream_model", model,
		"has_key", key != "", "realtime_pipeline", pipeline)
	return nil
}

// config returns the loaded configuration, or the typed not-loaded error so
// callers see FailedPrecondition instead of a nil dereference.
func (p *LocalAIProxy) config() (*proxyConfig, error) {
	cfg := p.cfg.Load()
	if cfg == nil {
		return nil, grpcerrors.ModelNotLoaded(backendName)
	}
	return cfg, nil
}

// model returns the model name to send upstream. The configured name wins so
// every method targets the same upstream model; req (a model named by the
// request itself) is only a fallback for configs that resolved no name.
func (p *LocalAIProxy) model(req string) string {
	if cfg := p.cfg.Load(); cfg != nil && cfg.upstreamModel != "" {
		return cfg.upstreamModel
	}
	return req
}

// resolveAPIKey mirrors config.ProxyConfig.ResolveAPIKey (and cloud-proxy's
// copy). Duplicated so the backend binary does not depend on core's layout.
func resolveAPIKey(envName, filePath string) (string, error) {
	if envName != "" {
		v := os.Getenv(envName)
		if v == "" {
			return "", fmt.Errorf("localai-proxy: api_key_env %q is unset", envName)
		}
		return v, nil
	}
	if filePath != "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("localai-proxy: read api_key_file %q: %w", filePath, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return "", nil
}

// unimplemented is the error for methods LocalAI's REST API cannot serve.
// Failover reads gRPC Unimplemented as a capability gap and moves to the next
// target without marking this one unhealthy.
func unimplemented(method string) error {
	return status.Errorf(codes.Unimplemented, "localai-proxy: %s has no upstream counterpart", method)
}

func (p *LocalAIProxy) AudioEncode(*pb.AudioEncodeRequest) (*pb.AudioEncodeResult, error) {
	return nil, unimplemented("AudioEncode")
}

func (p *LocalAIProxy) AudioDecode(*pb.AudioDecodeRequest) (*pb.AudioDecodeResult, error) {
	return nil, unimplemented("AudioDecode")
}

// AudioToAudioStream closes out because the gRPC server drains it until
// closed; leaving it open would hang the call.
func (p *LocalAIProxy) AudioToAudioStream(_ <-chan *pb.AudioToAudioRequest, out chan<- *pb.AudioToAudioResponse) error {
	close(out)
	return unimplemented("AudioToAudioStream")
}

func (p *LocalAIProxy) TokenClassify(context.Context, *pb.TokenClassifyRequest) (*pb.TokenClassifyResponse, error) {
	return nil, unimplemented("TokenClassify")
}

func (p *LocalAIProxy) ModelMetadata(*pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
	return nil, unimplemented("ModelMetadata")
}

func (p *LocalAIProxy) StartFineTune(*pb.FineTuneRequest) (*pb.FineTuneJobResult, error) {
	return nil, unimplemented("StartFineTune")
}

// FineTuneProgress closes the channel: the gRPC server waits for it to close
// before returning, and base.Base leaves it open.
func (p *LocalAIProxy) FineTuneProgress(_ *pb.FineTuneProgressRequest, updates chan *pb.FineTuneProgressUpdate) error {
	close(updates)
	return unimplemented("FineTuneProgress")
}

func (p *LocalAIProxy) StopFineTune(*pb.FineTuneStopRequest) error {
	return unimplemented("StopFineTune")
}

func (p *LocalAIProxy) ListCheckpoints(*pb.ListCheckpointsRequest) (*pb.ListCheckpointsResponse, error) {
	return nil, unimplemented("ListCheckpoints")
}

func (p *LocalAIProxy) ExportModel(*pb.ExportModelRequest) error {
	return unimplemented("ExportModel")
}

func (p *LocalAIProxy) StartQuantization(*pb.QuantizationRequest) (*pb.QuantizationJobResult, error) {
	return nil, unimplemented("StartQuantization")
}

// QuantizationProgress closes the channel for the same reason as
// FineTuneProgress.
func (p *LocalAIProxy) QuantizationProgress(_ *pb.QuantizationProgressRequest, updates chan *pb.QuantizationProgressUpdate) error {
	close(updates)
	return unimplemented("QuantizationProgress")
}

func (p *LocalAIProxy) StopQuantization(*pb.QuantizationStopRequest) error {
	return unimplemented("StopQuantization")
}
