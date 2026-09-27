package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// maxErrorBody caps the upstream body quoted in an error. It keeps gRPC
// status messages small while leaving room for LocalAI's JSON error text,
// which failover scans for request errors such as context overflows.
const maxErrorBody = 500

// postJSON sends body as JSON to path and decodes a 2xx JSON reply into out
// (skipped when out is nil). The request_timeout_seconds limit applies.
func (p *LocalAIProxy) postJSON(ctx context.Context, path string, body, out any) error {
	cfg, err := p.config()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "localai-proxy: encode %s request: %v", path, err)
	}
	ctx, cancel := withTimeout(ctx, cfg)
	defer cancel()

	req, err := p.newRequest(ctx, cfg, http.MethodPost, path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return p.do(req, path, out)
}

// postMultipart sends fields and, when fileField is set, the local file at
// filePath as a multipart form to path, decoding a 2xx JSON reply into out
// (skipped when out is nil). Core hands audio and images to backends as local
// paths, and LocalAI's upload endpoints take them as multipart files. The
// request_timeout_seconds limit applies.
func (p *LocalAIProxy) postMultipart(ctx context.Context, path string, fields map[string]string, fileField, filePath string, out any) error {
	form := multipartForm{fields: url.Values{}}
	for k, v := range fields {
		form.fields.Set(k, v)
	}
	if fileField != "" {
		form.files = []formFile{{field: fileField, path: filePath}}
	}
	return p.postForm(ctx, path, form, out)
}

// postForm uploads form to path and decodes the 2xx JSON reply into out. The
// request_timeout_seconds limit applies.
func (p *LocalAIProxy) postForm(ctx context.Context, path string, form multipartForm, out any) error {
	cfg, err := p.config()
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx, cfg)
	defer cancel()
	req, err := p.newMultipartRequest(ctx, cfg, path, form)
	if err != nil {
		return err
	}
	return p.do(req, path, out)
}

// multipartForm is an upload: repeated fields (timestamp_granularities[]) need
// url.Values, and audio transforms send two files.
type multipartForm struct {
	fields url.Values
	files  []formFile
}

type formFile struct {
	field string
	path  string
}

// newMultipartRequest builds a POST whose body streams form through a pipe,
// so large audio files are not buffered in memory. The writer goroutine owns
// the opened files and closes them when it finishes; the transport closes the
// pipe when the request ends, which unblocks the writer on every error path.
func (p *LocalAIProxy) newMultipartRequest(ctx context.Context, cfg *proxyConfig, path string, form multipartForm) (*http.Request, error) {
	// Open before contacting the upstream so a bad path is reported as a
	// request error, not as a failure of the remote host.
	files := make([]*os.File, 0, len(form.files))
	closeAll := func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
	for _, ff := range form.files {
		f, err := os.Open(ff.path)
		if err != nil {
			closeAll()
			return nil, status.Errorf(codes.InvalidArgument, "localai-proxy: open %s: %v", ff.path, err)
		}
		files = append(files, f)
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer closeAll()
		pw.CloseWithError(writeMultipart(mw, form, files))
	}()

	req, err := p.newRequest(ctx, cfg, http.MethodPost, path, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req, nil
}

func writeMultipart(mw *multipart.Writer, form multipartForm, files []*os.File) error {
	for k, vs := range form.fields {
		for _, v := range vs {
			if err := mw.WriteField(k, v); err != nil {
				return err
			}
		}
	}
	for i, file := range files {
		part, err := mw.CreateFormFile(form.files[i].field, filepath.Base(file.Name()))
		if err != nil {
			return err
		}
		if _, err := io.Copy(part, file); err != nil {
			return err
		}
	}
	return mw.Close()
}

// postStream sends body as JSON to path and returns the open response of a
// 2xx reply; the caller must close its body. No request_timeout_seconds
// limit applies: streams legitimately outlast it, and ctx bounds them.
func (p *LocalAIProxy) postStream(ctx context.Context, path string, body any) (*http.Response, error) {
	cfg, err := p.config()
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "localai-proxy: encode %s request: %v", path, err)
	}
	req, err := p.newRequest(ctx, cfg, http.MethodPost, path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return p.doStream(req, path)
}

// postMultipartStream uploads form and returns the open response of a 2xx
// reply; the caller must close its body. Like postStream, only ctx bounds it.
func (p *LocalAIProxy) postMultipartStream(ctx context.Context, path string, form multipartForm) (*http.Response, error) {
	cfg, err := p.config()
	if err != nil {
		return nil, err
	}
	req, err := p.newMultipartRequest(ctx, cfg, path, form)
	if err != nil {
		return nil, err
	}
	return p.doStream(req, path)
}

// doStream runs req and returns the open response of a 2xx reply.
func (p *LocalAIProxy) doStream(req *http.Request, path string) (*http.Response, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, transportError(path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer func() { _ = resp.Body.Close() }()
		return nil, statusError(path, resp)
	}
	return resp, nil
}

func (p *LocalAIProxy) newRequest(ctx context.Context, cfg *proxyConfig, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, cfg.base+path, body)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "localai-proxy: build %s request: %v", path, err)
	}
	if cfg.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.apiKey)
	}
	return req, nil
}

// do runs req and decodes a 2xx JSON reply into out.
func (p *LocalAIProxy) do(req *http.Request, path string, out any) error {
	resp, err := p.client.Do(req)
	if err != nil {
		return transportError(path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return statusError(path, resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return transportError(path, ctxErr)
		}
		return status.Errorf(codes.Internal, "localai-proxy: decode %s response: %v", path, err)
	}
	return nil
}

func withTimeout(ctx context.Context, cfg *proxyConfig) (context.Context, context.CancelFunc) {
	if cfg.timeout > 0 {
		return context.WithTimeout(ctx, cfg.timeout)
	}
	return context.WithCancel(ctx)
}

// transportError maps a failed round trip to a gRPC status. A dead or
// unreachable upstream is Unavailable so failover moves to the next target.
func transportError(path string, err error) error {
	code := codes.Unavailable
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code = codes.DeadlineExceeded
	case errors.Is(err, context.Canceled):
		code = codes.Canceled
	}
	xlog.Warn("localai-proxy: upstream request failed", "path", path, "error", err)
	return status.Errorf(code, "localai-proxy: upstream %s: %v", path, err)
}

// statusError maps a non-2xx upstream reply to a gRPC status. 5xx means the
// upstream is unhealthy (Unavailable, so failover retries elsewhere); 4xx
// means the request itself is wrong (InvalidArgument, so failover does not
// trip a healthy target over a client error). 429 is the exception, see
// below. 501 is the upstream saying it cannot serve this kind of request,
// which failover treats as a capability gap, like our own Unimplemented
// methods.
func statusError(path string, resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody+1))
	msg := strings.TrimSpace(string(raw))
	if len(msg) > maxErrorBody {
		msg = msg[:maxErrorBody] + "..."
	}
	// gRPC refuses to send a status message that is not valid UTF-8, and the
	// cut above may split a rune.
	msg = strings.ToValidUTF8(msg, "")

	var code codes.Code
	switch {
	case resp.StatusCode == http.StatusNotImplemented:
		code = codes.Unimplemented
	case resp.StatusCode >= 500:
		code = codes.Unavailable
	case resp.StatusCode == http.StatusTooManyRequests:
		// Rate limited: the request is fine but the upstream is out of
		// capacity. Failover retries it on the next target and trips this
		// one, so traffic moves off it for a while.
		code = codes.ResourceExhausted
	case resp.StatusCode >= 400:
		code = codes.InvalidArgument
	default:
		// A 1xx/3xx here means a misbehaving upstream (redirects are refused
		// by the client), not a bad request.
		code = codes.Unavailable
	}
	xlog.Warn("localai-proxy: upstream error", "path", path, "status", resp.StatusCode)
	return status.Error(code, fmt.Sprintf("localai-proxy: upstream %s returned %d: %s", path, resp.StatusCode, msg))
}

// postJSONToFile sends body as JSON to path and writes a 2xx reply's body,
// which is audio rather than JSON, to dst. The request_timeout_seconds limit
// applies.
func (p *LocalAIProxy) postJSONToFile(ctx context.Context, path string, body any, dst string) error {
	cfg, err := p.config()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "localai-proxy: encode %s request: %v", path, err)
	}
	ctx, cancel := withTimeout(ctx, cfg)
	defer cancel()
	req, err := p.newRequest(ctx, cfg, http.MethodPost, path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = p.doToFile(req, path, dst)
	return err
}

// postMultipartToFile uploads form to path and writes a 2xx reply's body to
// dst, returning the reply headers for endpoints that describe extra outputs
// there. The request_timeout_seconds limit applies.
func (p *LocalAIProxy) postMultipartToFile(ctx context.Context, path string, form multipartForm, dst string) (http.Header, error) {
	cfg, err := p.config()
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx, cfg)
	defer cancel()
	req, err := p.newMultipartRequest(ctx, cfg, path, form)
	if err != nil {
		return nil, err
	}
	return p.doToFile(req, path, dst)
}

// getToFile downloads path to dst. The request_timeout_seconds limit applies.
func (p *LocalAIProxy) getToFile(ctx context.Context, path, dst string) error {
	cfg, err := p.config()
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx, cfg)
	defer cancel()
	req, err := p.newRequest(ctx, cfg, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	_, err = p.doToFile(req, path, dst)
	return err
}

// doToFile runs req and writes a 2xx body to dst. A failed copy removes dst:
// core serves whatever file it finds there, and a truncated recording must
// not pass for a finished one.
func (p *LocalAIProxy) doToFile(req *http.Request, path, dst string) (http.Header, error) {
	resp, err := p.doStream(req, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// #nosec G304 -- dst is the output path core chose for this call (generated content dir), never a caller-supplied path
	f, err := os.Create(filepath.Clean(dst))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "localai-proxy: create %s: %v", dst, err)
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dst)
		if copyErr != nil {
			if ctxErr := req.Context().Err(); ctxErr != nil {
				return nil, transportError(path, ctxErr)
			}
			return nil, transportError(path, copyErr)
		}
		return nil, status.Errorf(codes.Internal, "localai-proxy: write %s: %v", dst, closeErr)
	}
	return resp.Header, nil
}
