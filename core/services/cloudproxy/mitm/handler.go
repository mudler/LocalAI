package mitm

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/xlog"
	"golang.org/x/net/http2"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/core/services/routing/piiadapter"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// PIIHandlerOptions configures NewPIIHandler.
type PIIHandlerOptions struct {
	// DetectorsByHost maps an intercepted host (lower-cased) to the NER
	// detector configs that should scan request bodies bound for it. The
	// configs are resolved at listener-start from each host's owning
	// model's pii.detectors + the detector models' pii_detection policy
	// (a model-config edit needs a MITM restart, as hosts already do). A
	// host absent from the map (or with an empty slice) is forwarded
	// unredacted. Detector errors at request time fail closed.
	DetectorsByHost map[string][]pii.NERConfig

	// EventStore receives PIIEvent rows. nil discards events.
	EventStore pii.EventStore

	// UpstreamTLS overrides the tls.Config used when dialing the
	// real upstream. Defaults to a system-trust HTTPS client.
	UpstreamTLS *tls.Config

	// CorrelationIDHeader names the request header carrying a
	// caller-supplied correlation ID. Defaults to "X-Correlation-ID".
	CorrelationIDHeader string

	// DialHost optionally remaps the host used for the outbound
	// upstream URL. Identity by default; tests inject a httptest
	// listener address.
	DialHost func(host string) string
}

func NewPIIHandler(opts PIIHandlerOptions) InterceptHandler {
	tlsCfg := opts.UpstreamTLS
	if tlsCfg == nil {
		tlsCfg = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	} else if len(tlsCfg.NextProtos) == 0 {
		tlsCfg.NextProtos = []string{"h2", "http/1.1"}
	}
	transport := &http.Transport{
		TLSClientConfig:   tlsCfg,
		ForceAttemptHTTP2: true,
	}
	if err := http2.ConfigureTransport(transport); err != nil {
		xlog.Debug("mitm: http2.ConfigureTransport failed", "error", err)
	}

	corrHeader := opts.CorrelationIDHeader
	if corrHeader == "" {
		corrHeader = "X-Correlation-ID"
	}

	dialHost := opts.DialHost
	if dialHost == nil {
		dialHost = func(h string) string { return h }
	}

	detectorsByHost := make(map[string][]pii.NERConfig, len(opts.DetectorsByHost))
	for h, cfgs := range opts.DetectorsByHost {
		detectorsByHost[strings.ToLower(strings.TrimSpace(h))] = cfgs
	}

	d := &piiDispatcher{
		// Refuse redirects: the MITM client forwards to the real
		// upstream over TLS, and a 3xx means the upstream (or something
		// impersonating it) is trying to bounce the request elsewhere.
		// Following it would replay caller headers — including provider
		// API keys such as Anthropic's x-api-key, which Go does NOT
		// strip on cross-host redirects — to an unvetted host. Surface
		// it as an error (handled as a 502) instead.
		client:          httpclient.New(httpclient.WithTransport(transport)),
		detectorsByHost: detectorsByHost,
		store:           opts.EventStore,
		corrHeader:      corrHeader,
		dialHost:        dialHost,
	}
	return d.serve
}

type piiDispatcher struct {
	client          *http.Client
	detectorsByHost map[string][]pii.NERConfig
	store           pii.EventStore
	corrHeader      string
	dialHost        func(host string) string
	eventSeq        atomic.Uint64
}

func (d *piiDispatcher) serve(w http.ResponseWriter, r *http.Request, host string) {
	start := time.Now()
	cw := &countingResponseWriter{ResponseWriter: w}
	w = cw

	var (
		correlationID string
		bytesSent     int64
	)
	defer func() {
		d.recordTrafficEvent(host, correlationID, bytesSent, cw.bytes, cw.status, start)
	}()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "mitm: read body: "+err.Error(), http.StatusBadGateway)
		return
	}
	_ = r.Body.Close()

	correlationID = r.Header.Get(d.corrHeader)
	if correlationID == "" {
		correlationID = r.Header.Get("x-request-id")
	}

	shape := classifyRequestShape(host, r.URL.Path)
	cfgs := d.detectorsByHost[strings.ToLower(host)]
	if len(cfgs) > 0 && shape != shapeUnknown {
		redacted, blocked, err := d.redactRequest(r.Context(), body, shape, cfgs, correlationID)
		switch {
		case err != nil:
			// Fail closed: a detector outage must not silently forward the
			// request unredacted — the operator configured this host's
			// model with detectors precisely to catch this PII.
			xlog.Error("mitm: NER redaction failed; blocking request (fail-closed)", "host", host, "path", r.URL.Path, "error", err)
			writePIIBlocked(w, correlationID)
			return
		case blocked:
			writePIIBlocked(w, correlationID)
			return
		default:
			body = redacted
		}
	}

	upstreamURL := "https://" + d.dialHost(host) + r.URL.RequestURI()
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "mitm: build upstream request: "+err.Error(), http.StatusBadGateway)
		return
	}
	upstreamReq.Header = cloneHopByHopFiltered(r.Header)
	upstreamReq.ContentLength = int64(len(body))
	upstreamReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	bytesSent = int64(len(body))

	resp, err := d.client.Do(upstreamReq)
	if err != nil {
		http.Error(w, "mitm: upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for k, vs := range resp.Header {
		if isHopByHop(k) || strings.EqualFold(k, "Transfer-Encoding") || strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Response/output redaction is out of scope for now — the MITM proxy
	// only scans request bodies (input). SSE responses pass through
	// unmodified.
	contentType := resp.Header.Get("Content-Type")
	if isSSE(contentType) {
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 32*1024)
		for {
			n, rErr := resp.Body.Read(buf)
			if n > 0 {
				if _, wErr := w.Write(buf[:n]); wErr != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if rErr != nil {
				return
			}
		}
	}

	_, _ = io.Copy(w, resp.Body)
}

type requestShape int

const (
	shapeUnknown requestShape = iota
	shapeOpenAIChat
	shapeAnthropicMessages
)

func classifyRequestShape(host, path string) requestShape {
	host = strings.ToLower(host)
	switch {
	case host == "api.openai.com" && strings.HasSuffix(path, "/v1/chat/completions"):
		return shapeOpenAIChat
	case host == "api.anthropic.com" && strings.HasSuffix(path, "/v1/messages"):
		return shapeAnthropicMessages
	}
	return shapeUnknown
}

func (d *piiDispatcher) redactRequest(ctx context.Context, body []byte, shape requestShape, cfgs []pii.NERConfig, correlationID string) ([]byte, bool, error) {
	var parsed any
	var adapter pii.Adapter
	switch shape {
	case shapeOpenAIChat:
		req := &schema.OpenAIRequest{}
		if err := json.Unmarshal(body, req); err != nil {
			return nil, false, fmt.Errorf("parse openai: %w", err)
		}
		parsed = req
		adapter = piiadapter.OpenAI()
	case shapeAnthropicMessages:
		req := &schema.AnthropicRequest{}
		if err := json.Unmarshal(body, req); err != nil {
			return nil, false, fmt.Errorf("parse anthropic: %w", err)
		}
		parsed = req
		adapter = piiadapter.Anthropic()
	default:
		return body, false, nil
	}

	texts := adapter.Scan(parsed)
	if len(texts) == 0 {
		return body, false, nil
	}

	updates := make([]pii.ScannedText, 0, len(texts))
	blocked := false
	for _, st := range texts {
		if st.Text == "" {
			continue
		}
		res, err := pii.RedactNER(ctx, st.Text, cfgs)
		if err != nil {
			return nil, false, fmt.Errorf("ner detect: %w", err)
		}
		if len(res.Spans) == 0 {
			continue
		}
		d.recordEvents(res.Spans, correlationID)
		if res.Blocked {
			blocked = true
		}
		updates = append(updates, pii.ScannedText{Index: st.Index, Text: res.Redacted})
	}

	if len(updates) > 0 {
		adapter.Apply(parsed, updates)
	}

	out, err := json.Marshal(parsed)
	if err != nil {
		return nil, false, fmt.Errorf("re-marshal: %w", err)
	}
	return out, blocked, nil
}

func (d *piiDispatcher) recordEvents(spans []pii.Span, correlationID string) {
	if d.store == nil {
		return
	}
	for _, span := range spans {
		ev := pii.PIIEvent{
			ID:            fmt.Sprintf("mitm_%s_%d", correlationID, d.eventSeq.Add(1)),
			Kind:          pii.KindPII,
			Origin:        pii.OriginProxy,
			CorrelationID: correlationID,
			Direction:     pii.DirectionIn,
			PatternID:     span.Pattern,
			ByteOffset:    span.Start,
			Length:        span.End - span.Start,
			HashPrefix:    span.HashPrefix,
			Action:        span.Action,
			CreatedAt:     time.Now(),
		}
		if err := d.store.Record(context.Background(), ev); err != nil {
			xlog.Debug("mitm: failed to record pii event", "error", err, "pattern", span.Pattern)
		}
	}
}

func writePIIBlocked(w http.ResponseWriter, correlationID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	resp := map[string]any{
		"error": map[string]string{
			"message": "request blocked by LocalAI MITM proxy (sensitive data detected)",
			"type":    "pii_blocked",
		},
		"correlation_id": correlationID,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func isSSE(contentType string) bool {
	return strings.HasPrefix(strings.TrimSpace(contentType), "text/event-stream")
}

// hopByHopHeaders are not forwarded by the proxy (RFC 7230 §6.1).
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailers":            {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func isHopByHop(name string) bool {
	_, ok := hopByHopHeaders[http.CanonicalHeaderKey(name)]
	return ok
}

// countingResponseWriter wraps an http.ResponseWriter to track the
// total bytes written downstream and the status code. It implements
// http.Flusher because the SSE paths flush per event; without that
// the assertion `w.(http.Flusher)` would silently degrade to no-op.
type countingResponseWriter struct {
	http.ResponseWriter
	bytes  int64
	status int
}

func (w *countingResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *countingResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *countingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (d *piiDispatcher) recordTrafficEvent(host, correlationID string, sent, received int64, status int, start time.Time) {
	if d.store == nil {
		return
	}
	ev := pii.PIIEvent{
		ID:            fmt.Sprintf("proxy_traffic_%s_%d", correlationID, d.eventSeq.Add(1)),
		Kind:          pii.KindProxyTraffic,
		CorrelationID: correlationID,
		Host:          host,
		BytesSent:     sent,
		BytesReceived: received,
		StatusCode:    status,
		DurationMS:    time.Since(start).Milliseconds(),
		CreatedAt:     time.Now(),
	}
	if err := d.store.Record(context.Background(), ev); err != nil {
		xlog.Debug("mitm: failed to record proxy_traffic event", "error", err, "host", host)
	}
}

func cloneHopByHopFiltered(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for k, vs := range in {
		if isHopByHop(k) {
			continue
		}
		copied := make([]string, len(vs))
		copy(copied, vs)
		out[k] = copied
	}
	return out
}
