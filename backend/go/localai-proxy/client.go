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

	req, err := p.newRequest(ctx, cfg, path, bytes.NewReader(payload))
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
	cfg, err := p.config()
	if err != nil {
		return err
	}
	var file *os.File
	if fileField != "" {
		// Open before contacting the upstream so a bad path is reported as a
		// request error, not as a failure of the remote host.
		if file, err = os.Open(filePath); err != nil {
			return status.Errorf(codes.InvalidArgument, "localai-proxy: open %s: %v", filePath, err)
		}
		defer func() { _ = file.Close() }()
	}
	ctx, cancel := withTimeout(ctx, cfg)
	defer cancel()

	// Stream the form through a pipe so large audio files are not buffered
	// in memory. The transport closes pr when the request ends, which
	// unblocks the writer on every error path.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		pw.CloseWithError(writeMultipart(mw, fields, fileField, file))
	}()

	req, err := p.newRequest(ctx, cfg, path, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return p.do(req, path, out)
}

func writeMultipart(mw *multipart.Writer, fields map[string]string, fileField string, file *os.File) error {
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return err
		}
	}
	if file != nil {
		part, err := mw.CreateFormFile(fileField, filepath.Base(file.Name()))
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
	req, err := p.newRequest(ctx, cfg, path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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

func (p *LocalAIProxy) newRequest(ctx context.Context, cfg *proxyConfig, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.base+path, body)
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
// trip a healthy target over a client error). 501 is the upstream saying it
// cannot serve this kind of request, which failover treats as a capability
// gap, like our own Unimplemented methods.
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
