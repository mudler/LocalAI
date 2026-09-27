package middleware

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/failover"
)

const (
	HeaderServedModel = "X-LocalAI-Served-Model"
	HeaderFailover    = "X-LocalAI-Failover"
)

// MaxFailoverReplayBody caps the request body kept for a retry. A larger body
// is still served, by one target only.
const MaxFailoverReplayBody = 32 << 20

type failoverState struct {
	attempt *failover.Attempt
}

// SetFailoverManager enables failover chains. Without it, a request for a
// chain fails with 503.
func (re *RequestExtractor) SetFailoverManager(m *failover.Manager) { re.failover = m }

// resolveFailover returns the config of the target that should serve this
// attempt. The first attempt plans the chain; retries reuse the plan.
func (re *RequestExtractor) resolveFailover(c echo.Context, requested string, chain *config.ModelConfig) (*config.ModelConfig, error) {
	st, _ := c.Get(ContextKeyFailoverAttempt).(*failoverState)
	if st == nil || st.attempt.Chain() != chain.Name {
		if re.failover == nil {
			return nil, fmt.Errorf("model %q is a failover chain, but failover is not running", chain.Name)
		}
		att, err := re.failover.Plan(chain.Name)
		if err != nil {
			return nil, err
		}
		st = &failoverState{attempt: att}
		c.Set(ContextKeyFailoverAttempt, st)
	}
	for {
		cfg, err := re.loadFailoverTarget(st.attempt.Target())
		if err == nil && cfg.IsDisabled() {
			// Disabled on purpose, not broken: move on without a trip.
			if st.attempt.Skip() {
				continue
			}
			c.Set(ContextKeyFailoverAttempt, nil)
			return nil, fmt.Errorf("failover chain %q: target %q is disabled", chain.Name, cfg.Name)
		}
		if err == nil {
			failover.PrepareTarget(cfg) // cfg is a copy
			c.Set(ContextKeyRequestedModel, requested)
			c.Set(ContextKeyServedModel, cfg.Name)
			setFailoverHeaders(c.Response().Header(), st.attempt)
			return cfg, nil
		}
		if !st.attempt.Fail(err) {
			// Clear the state so the retry wrapper sends this 503 as is.
			c.Set(ContextKeyFailoverAttempt, nil)
			return nil, err
		}
	}
}

func (re *RequestExtractor) loadFailoverTarget(name string) (*config.ModelConfig, error) {
	cfg, err := re.modelConfigLoader.LoadModelConfigFileByNameDefaultOptions(name, re.applicationConfig)
	if err != nil {
		return nil, err
	}
	resolved, _, err := re.modelConfigLoader.ResolveAlias(cfg)
	return resolved, err
}

func setFailoverHeaders(h http.Header, att *failover.Attempt) {
	h.Set(HeaderServedModel, att.Target())
	switch {
	case att.Degraded():
		h.Set(HeaderFailover, "degraded")
	case att.Target() != att.Primary():
		h.Set(HeaderFailover, "fallback")
	default:
		h.Del(HeaderFailover)
	}
}

// failoverRetry runs h again on the next target while the response is not
// committed. h is SetModelAndConfig's body plus the rest of the chain, so
// every attempt binds the request again from the replayed body.
func (re *RequestExtractor) failoverRetry(h echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Without chains there is nothing to retry, so plain installations
		// pay neither for the body copy nor for the writer.
		if !re.failover.HasChains() {
			return h(c)
		}
		req := c.Request()
		src := req.Body
		if src == nil {
			src = http.NoBody
		}
		rec := &replayBody{src: src, limit: MaxFailoverReplayBody}
		req.Body = rec
		// A default-model middleware may have parsed a multipart form before
		// this point, draining the body. That form stays valid for every
		// attempt; a form parsed during an attempt is dropped and parsed again
		// from the replayed body.
		entryMultipart, entryForm, entryPostForm := req.MultipartForm, req.Form, req.PostForm
		resp := c.Response()
		orig := resp.Writer
		baseHeader := resp.Header().Clone()
		defer func() { resp.Writer = orig }()
		active := func() bool {
			st, _ := c.Get(ContextKeyFailoverAttempt).(*failoverState)
			return st != nil
		}
		tracing := re.applicationConfig != nil && re.applicationConfig.EnableTracing
		for {
			w := &failoverWriter{ResponseWriter: orig, active: active}
			resp.Writer = w
			c.Set(ContextKeyAdmissionRejected, nil)
			err := h(c)
			st, _ := c.Get(ContextKeyFailoverAttempt).(*failoverState)
			if st == nil {
				w.release()
				return err
			}
			att := st.attempt
			status := w.held
			if err == nil && status == 0 {
				// A 4xx says nothing about the target's health.
				if w.status < http.StatusBadRequest {
					att.Succeed()
				}
				return nil
			}
			rejected, _ := c.Get(ContextKeyAdmissionRejected).(bool)
			// A handler that wrote its 501 itself instead of returning it
			// reports the same gap.
			gap := (failover.IsCapabilityGap(err) || status == http.StatusNotImplemented) && !w.committed
			if (rejected && !w.committed) || gap {
				// The target is at capacity, or cannot serve this kind of
				// request at all: spill to the next target without counting
				// a failure. Neither says anything about the target's health.
				if !rec.replayable() || !att.Skip() {
					w.release()
					return err
				}
			} else {
				cause := attemptError(err, status, w.body.Bytes())
				retryable := req.Context().Err() == nil && failover.IsRetryable(err, status)
				if !retryable || w.committed || !rec.replayable() {
					if retryable {
						att.Report(cause)
					}
					w.release()
					return err
				}
				failover.RecordAttemptTrace(tracing, att.Chain(), att.Target(), cause)
				if !att.Fail(cause) {
					w.release()
					return err
				}
			}
			// Temp files of a form parsed during this attempt would otherwise
			// outlive the request: the server only cleans up the last form.
			if mf := c.Request().MultipartForm; mf != nil && mf != entryMultipart {
				_ = mf.RemoveAll()
			}
			req.Body = rec.replay()
			req.MultipartForm, req.Form, req.PostForm = entryMultipart, entryForm, entryPostForm
			// Middleware after this one may have replaced the request; the
			// next attempt starts again from the request as it arrived here.
			c.SetRequest(req)
			resetResponse(resp, baseHeader)
		}
	}
}

// stopFailoverRecording releases the recorded body of a request whose model
// turned out not to be a chain: it will never be replayed.
func stopFailoverRecording(c echo.Context) {
	if rb, ok := c.Request().Body.(*replayBody); ok {
		rb.stop()
	}
}

func attemptError(err error, status int, body []byte) error {
	if err != nil {
		return err
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return fmt.Errorf("HTTP %d: %s", status, msg)
}

func resetResponse(resp *echo.Response, base http.Header) {
	h := resp.Header()
	for k := range h {
		delete(h, k)
	}
	for k, v := range base {
		h[k] = slices.Clone(v)
	}
	resp.Committed = false
	resp.Status = http.StatusOK
	resp.Size = 0
}

// failoverWriter holds back an error response (status >= 500) of a chain
// request until the handler returns, so the retry can drop it.
type failoverWriter struct {
	http.ResponseWriter
	active    func() bool
	held      int
	body      bytes.Buffer
	committed bool
	// status is the code sent to the client, 0 until one is sent.
	status int
}

func (w *failoverWriter) WriteHeader(code int) {
	if w.held != 0 {
		return
	}
	if !w.committed && code >= 500 && w.active() {
		w.held = code
		return
	}
	w.committed = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *failoverWriter) Write(b []byte) (int, error) {
	if w.held != 0 {
		return w.body.Write(b)
	}
	w.committed = true
	return w.ResponseWriter.Write(b)
}

// FlushError and Hijack go through a ResponseController so the capabilities of
// wrapped writers further down stay reachable, as they were before this writer
// was inserted.
func (w *failoverWriter) FlushError() error {
	w.release()
	w.committed = true
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *failoverWriter) Flush() { _ = w.FlushError() }

func (w *failoverWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.committed = true
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *failoverWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// release sends a held error response to the client.
func (w *failoverWriter) release() {
	if w.held == 0 {
		return
	}
	code := w.held
	w.held = 0
	w.committed = true
	w.ResponseWriter.WriteHeader(code)
	_, _ = w.ResponseWriter.Write(w.body.Bytes())
	w.body.Reset()
}

// replayBody records what the handler reads, up to limit, so the body can be
// sent again to the next target. Decoders often stop at the end of the value
// without reading to EOF, so a replay is the recorded bytes followed by
// whatever the previous attempt left unread.
type replayBody struct {
	src      io.ReadCloser
	buf      bytes.Buffer
	limit    int
	overflow bool
	stopped  bool
}

func (r *replayBody) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 && !r.overflow && !r.stopped {
		if r.buf.Len()+n > r.limit {
			r.overflow = true
			// A new buffer, not Reset: Reset keeps the memory.
			r.buf = bytes.Buffer{}
		} else {
			r.buf.Write(p[:n])
		}
	}
	return n, err
}

func (r *replayBody) Close() error { return r.src.Close() }

// replayable reports whether everything read so far was kept.
func (r *replayBody) replayable() bool { return !r.overflow && !r.stopped }

// stop ends recording and frees what was kept.
func (r *replayBody) stop() {
	r.stopped = true
	r.buf = bytes.Buffer{}
}

// replay rewinds to the start of the body and keeps recording, so a third
// attempt can replay too.
func (r *replayBody) replay() io.ReadCloser {
	data := bytes.Clone(r.buf.Bytes())
	rest := r.src
	r.src = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(data), rest), rest}
	r.buf.Reset()
	return r
}
