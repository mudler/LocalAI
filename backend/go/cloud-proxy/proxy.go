package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Mirror of core/config.Proxy{Mode,Provider}* — backends don't
// import core to keep the boundary clean.
const (
	modePassthrough = "passthrough"
	modeTranslate   = "translate"

	providerOpenAI    = "openai"
	providerAnthropic = "anthropic"
)

// CloudProxy is the LocalAI backend that proxies model traffic to a
// configured upstream HTTP provider. Concurrency: base.SingleThread is
// NOT embedded — forward calls are independent and HTTP transport is
// goroutine-safe, so multiple Forward streams can run in parallel.
// Locking would serialise requests to a chat provider for no benefit.
type CloudProxy struct {
	base.Base

	cfg    atomic.Pointer[proxyConfig]
	client *http.Client
}

type proxyConfig struct {
	upstreamURL   string
	mode          string
	provider      string
	upstreamModel string
	localModel    string // ModelOptions.Model — fallback when upstream_model is unset
	apiKey        string // resolved at Load time
}

func NewCloudProxy() *CloudProxy {
	// httpclient.New refuses redirects outright: the proxy talks to a
	// single configured upstream API (OpenAI/Anthropic/...) that answers
	// directly, so a 3xx means misconfiguration, a hijacked upstream, or
	// DNS trickery — never normal operation. Following it would replay the
	// request, including the operator's x-api-key (which Go does NOT strip
	// on cross-host redirects), to an unvetted host and leak the key
	// (GHSA-3mj3-57v2-4636). It also imposes no body deadline, so streaming
	// SSE responses that legitimately last minutes are not truncated.
	return &CloudProxy{client: httpclient.New()}
}

func (c *CloudProxy) Load(opts *pb.ModelOptions) error {
	po := opts.GetProxy()
	if po == nil {
		return errors.New("cloud-proxy: Load requires ProxyOptions to be set")
	}
	if po.GetUpstreamUrl() == "" {
		return errors.New("cloud-proxy: upstream_url is required")
	}
	if _, err := url.ParseRequestURI(po.GetUpstreamUrl()); err != nil {
		return fmt.Errorf("cloud-proxy: upstream_url %q invalid: %w", po.GetUpstreamUrl(), err)
	}

	mode := po.GetMode()
	if mode == "" {
		mode = modePassthrough
	}
	switch mode {
	case modePassthrough:
	case modeTranslate:
		switch po.GetProvider() {
		case providerOpenAI:
			// implemented in provider_openai.go
		case providerAnthropic:
			// implemented in provider_anthropic.go
		default:
			return fmt.Errorf("cloud-proxy: translate mode requires provider in {%s, %s}, got %q",
				providerOpenAI, providerAnthropic, po.GetProvider())
		}
	default:
		return fmt.Errorf("cloud-proxy: unknown mode %q", mode)
	}

	key, err := resolveAPIKey(po.GetApiKeyEnv(), po.GetApiKeyFile())
	if err != nil {
		return err
	}

	c.cfg.Store(&proxyConfig{
		upstreamURL:   po.GetUpstreamUrl(),
		mode:          mode,
		provider:      po.GetProvider(),
		upstreamModel: po.GetUpstreamModel(),
		localModel:    opts.GetModel(),
		apiKey:        key,
	})
	xlog.Info("cloud-proxy: ready",
		"upstream", po.GetUpstreamUrl(),
		"mode", mode,
		"provider", po.GetProvider(),
		"has_key", key != "")
	return nil
}

// resolveAPIKey mirrors config.ProxyConfig.ResolveAPIKey. Duplicated
// (a few lines) rather than importing core/config from a backend
// binary — keeps backends independent of core's package layout.
// Mutual-exclusion is enforced upstream in core/config.Validate.
func resolveAPIKey(envName, filePath string) (string, error) {
	if envName != "" {
		v := os.Getenv(envName)
		if v == "" {
			return "", fmt.Errorf("cloud-proxy: api_key_env %q is unset", envName)
		}
		return v, nil
	}
	if filePath != "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("cloud-proxy: read api_key_file %q: %w", filePath, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return "", nil
}

// PredictRich is the non-streaming translate path. Returns a fully-
// populated *pb.Reply: content, tool-call deltas (ChatDeltas), and
// usage tokens. Implements the optional grpc.AIModelRich interface;
// the gRPC server prefers this path over Predict when present so
// tool calls survive the round-trip. Passthrough mode rejects
// PredictRich — callers must use Forward.
func (c *CloudProxy) PredictRich(opts *pb.PredictOptions) (reply *pb.Reply, err error) {
	cfg := c.cfg.Load()
	if cfg == nil {
		return nil, errors.New("cloud-proxy: model not loaded")
	}
	if cfg.mode != modeTranslate {
		return nil, fmt.Errorf("cloud-proxy: Predict only valid in translate mode (have %s)", cfg.mode)
	}
	xlog.Info("cloud-proxy: predict", "provider", cfg.provider, "upstream", cfg.upstreamURL, "upstream_model", cfg.upstreamModel)
	defer func() {
		if err != nil {
			xlog.Warn("cloud-proxy: predict failed", "provider", cfg.provider, "error", err)
		}
	}()
	ctx := context.Background()
	switch cfg.provider {
	case providerOpenAI:
		return c.predictOpenAIRich(ctx, cfg, opts)
	case providerAnthropic:
		return c.predictAnthropicRich(ctx, cfg, opts)
	default:
		return nil, fmt.Errorf("cloud-proxy: predict not implemented for provider %q", cfg.provider)
	}
}

// PredictStreamRich is the rich streaming counterpart of PredictRich.
// Each emitted Reply carries either a content delta, tool-call deltas,
// or usage tokens (the final upstream frame). base.Base.PredictStream
// is bypassed when AIModelRich is implemented, so the channel is
// closed by the gRPC server pump.
func (c *CloudProxy) PredictStreamRich(opts *pb.PredictOptions, results chan<- *pb.Reply) (err error) {
	cfg := c.cfg.Load()
	if cfg == nil {
		return errors.New("cloud-proxy: model not loaded")
	}
	if cfg.mode != modeTranslate {
		return fmt.Errorf("cloud-proxy: PredictStream only valid in translate mode (have %s)", cfg.mode)
	}
	xlog.Info("cloud-proxy: predict-stream", "provider", cfg.provider, "upstream", cfg.upstreamURL, "upstream_model", cfg.upstreamModel)
	defer func() {
		if err != nil {
			xlog.Warn("cloud-proxy: predict-stream failed", "provider", cfg.provider, "error", err)
		}
	}()
	ctx := context.Background()
	switch cfg.provider {
	case providerOpenAI:
		return c.predictOpenAIStreamRich(ctx, cfg, opts, results)
	case providerAnthropic:
		return c.predictAnthropicStreamRich(ctx, cfg, opts, results)
	default:
		return fmt.Errorf("cloud-proxy: predictStream not implemented for provider %q", cfg.provider)
	}
}

// Predict is the legacy (string, error) AIModel signature. Used only
// if a caller goes through the non-rich path (it shouldn't, since
// server.go prefers PredictRich). Provided so the AIModel interface
// is satisfied for backends that haven't opted into the rich variant.
func (c *CloudProxy) Predict(opts *pb.PredictOptions) (string, error) {
	reply, err := c.PredictRich(opts)
	if err != nil {
		return "", err
	}
	return string(reply.GetMessage()), nil
}

// PredictStream is the legacy chan-string streaming path. Adapts the
// rich stream by extracting only content text — tool-call-only chunks
// (no Message bytes) and usage-only chunks are silently dropped, since
// the legacy chan-string contract cannot represent them. Consumers
// that need tool calls must call PredictStreamRich directly.
func (c *CloudProxy) PredictStream(opts *pb.PredictOptions, results chan string) error {
	defer close(results)
	richCh := make(chan *pb.Reply)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.PredictStreamRich(opts, richCh)
		close(richCh)
	}()
	for reply := range richCh {
		if msg := reply.GetMessage(); len(msg) > 0 {
			results <- string(msg)
		}
	}
	return <-errCh
}

// sendReply pushes one Reply onto a stream channel honouring ctx
// cancellation. Returns false on cancel so the caller can exit with
// ctx.Err(). Used by both translate-mode providers.
func sendReply(ctx context.Context, results chan<- *pb.Reply, reply *pb.Reply) bool {
	select {
	case results <- reply:
		return true
	case <-ctx.Done():
		return false
	}
}

// newToolCallDelta is a small constructor for the cross-provider
// tool-call delta shape. Centralised so the int32 cast and the four
// fields stay consistent across the OpenAI / Anthropic translators.
// Empty name/args are valid — Anthropic streaming announces the call
// with id+name then sends arguments incrementally; OpenAI's reverse
// pattern (args without name) also lands here.
func newToolCallDelta(index int, id, name, args string) *pb.ToolCallDelta {
	return &pb.ToolCallDelta{
		Index:     int32(index),
		Id:        id,
		Name:      name,
		Arguments: args,
	}
}

// Forward shovels bytes between a Forward gRPC stream and an upstream
// HTTP request. First request message carries path/method/headers and
// the initial body chunk; subsequent messages append body chunks. The
// first reply carries upstream status + response headers; subsequent
// replies stream body chunks until the upstream connection closes.
// Cancellation of ctx (the gRPC stream context) closes the upstream
// connection.
func (c *CloudProxy) Forward(ctx context.Context, in <-chan *pb.ForwardRequest, out chan<- *pb.ForwardReply) error {
	defer close(out)

	cfg := c.cfg.Load()
	if cfg == nil {
		return errors.New("cloud-proxy: model not loaded")
	}
	if cfg.mode != modePassthrough {
		return fmt.Errorf("cloud-proxy: Forward only valid in passthrough mode (have %s)", cfg.mode)
	}

	first, ok := <-in
	if !ok {
		return errors.New("cloud-proxy: Forward stream closed before first request")
	}

	// Honour the per-request path only when the configured upstream_url
	// has no path of its own — gallery convention is to put the
	// canonical path in upstream_url.
	fullURL, err := composeURL(cfg.upstreamURL, first.GetPath())
	if err != nil {
		return err
	}

	method := first.GetMethod()
	if method == "" {
		method = http.MethodPost
	}

	// Pipe the body in from the gRPC stream so the HTTP request can
	// start before the client finishes sending. The pipe-reader is
	// closed via CloseWithError on the error paths so the writer
	// goroutine doesn't block forever.
	pr, pw := io.Pipe()

	go func() {
		var writeErr error
		defer func() { _ = pw.CloseWithError(writeErr) }()
		if len(first.GetBodyChunk()) > 0 {
			if _, writeErr = pw.Write(first.GetBodyChunk()); writeErr != nil {
				return
			}
		}
		for req := range in {
			if len(req.GetBodyChunk()) == 0 {
				continue
			}
			if _, writeErr = pw.Write(req.GetBodyChunk()); writeErr != nil {
				return
			}
		}
	}()

	req, err := http.NewRequestWithContext(ctx, method, fullURL, pr)
	if err != nil {
		_ = pr.CloseWithError(err) // unblocks the body-pump's pw.Write
		return fmt.Errorf("cloud-proxy: build request: %w", err)
	}

	// Apply caller-supplied headers, then override with the
	// authorization header derived from the resolved key. Caller-
	// supplied Authorization is always replaced — operators may not
	// know the backend's auth scheme, and silently leaking through a
	// client Authorization header to a different upstream would
	// confuse the upstream and could leak credentials.
	for _, h := range first.GetHeaders() {
		if h == nil || h.GetName() == "" {
			continue
		}
		// Strip hop-by-hop headers that aren't meaningful to the
		// upstream (Host is set by the http client from the URL;
		// Content-Length is computed from the body).
		if isHopByHopHeader(h.GetName()) {
			continue
		}
		req.Header.Add(h.GetName(), h.GetValue())
	}
	if cfg.apiKey != "" {
		applyAuthHeader(req, cfg.provider, cfg.apiKey)
	}

	xlog.Info("cloud-proxy: forward", "method", method, "url", fullURL, "provider", cfg.provider)
	resp, err := c.client.Do(req)
	if err != nil {
		xlog.Warn("cloud-proxy: forward upstream failed", "url", fullURL, "error", err)
		return fmt.Errorf("cloud-proxy: upstream request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	logFn := xlog.Info
	if resp.StatusCode >= 400 {
		logFn = xlog.Warn
	}
	logFn("cloud-proxy: forward response", "url", fullURL, "status", resp.StatusCode)

	// First reply: status + response headers, no body.
	headers := make([]*pb.ForwardHeader, 0, len(resp.Header))
	for k, vs := range resp.Header {
		for _, v := range vs {
			headers = append(headers, &pb.ForwardHeader{Name: k, Value: v})
		}
	}
	out <- &pb.ForwardReply{Status: int32(resp.StatusCode), Headers: headers}

	// Subsequent replies: body chunks. Use a fixed 8KB buffer — small
	// enough that SSE token frames flush promptly, large enough that
	// long chunked-transfer bodies aren't death by a thousand reads.
	buf := make([]byte, 8*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			out <- &pb.ForwardReply{BodyChunk: chunk}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			return fmt.Errorf("cloud-proxy: upstream body read: %w", rerr)
		}
	}
}

// composeURL combines the configured upstream URL with the per-request
// path. The upstream URL typically already includes the canonical path
// (e.g. https://api.openai.com/v1/chat/completions) so the per-request
// path is ignored in that case. When upstream_url is a bare host
// (https://api.openai.com), the request path is appended.
func composeURL(upstream, reqPath string) (string, error) {
	u, err := url.Parse(upstream)
	if err != nil {
		return "", fmt.Errorf("cloud-proxy: parse upstream_url %q: %w", upstream, err)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = reqPath
	}
	return u.String(), nil
}

// applyAuthHeader writes the appropriate authorization header for the
// provider. OpenAI/Anthropic/most providers use Bearer; Anthropic
// historically uses x-api-key + anthropic-version, but accepts Bearer
// too via the OpenAI-compatible path. Default to Bearer when provider
// is empty (passthrough mode where the operator doesn't claim a
// provider).
func applyAuthHeader(req *http.Request, provider, key string) {
	switch provider {
	case providerAnthropic:
		req.Header.Set("x-api-key", key)
		if req.Header.Get("anthropic-version") == "" {
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

// isHopByHopHeader returns true for headers that should not be
// forwarded from the client request to the upstream (RFC 7230 §6.1
// hop-by-hop list, plus a few that the http.Client sets itself).
func isHopByHopHeader(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "proxy-connection", "keep-alive", "transfer-encoding",
		"te", "trailer", "upgrade", "host", "content-length":
		return true
	}
	return false
}
