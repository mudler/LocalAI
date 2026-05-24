// Package cloudproxy stitches the cloud-proxy gRPC backend to the
// HTTP edge: model rewrite, body shaping, and SSE-aware PII filtering
// on the response. The outbound HTTP request itself lives inside the
// cloud-proxy backend binary (backend/go/cloud-proxy), not here — this
// package is the core-side glue.
package cloudproxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/cloudproxy/ssewire"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/xlog"
)

func rewriteModel(body []byte, upstreamModel string) ([]byte, error) {
	if upstreamModel == "" {
		return body, nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("cloudproxy: parse request body: %w", err)
	}
	m["model"] = upstreamModel
	return json.Marshal(m)
}

func streaming(body []byte) bool {
	var probe struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	return probe.Stream
}

// passthroughError emits the upstream's error response unchanged.
func passthroughError(c echo.Context, statusCode int, contentType string, body io.Reader) error {
	const maxErrBody = 1 << 20
	buf, _ := io.ReadAll(io.LimitReader(body, maxErrBody))
	if contentType != "" {
		c.Response().Header().Set("Content-Type", contentType)
	}
	c.Response().WriteHeader(statusCode)
	_, _ = c.Response().Writer.Write(buf)
	return nil
}

func forwardBuffered(c echo.Context, statusCode int, contentType string, body io.Reader) error {
	if contentType != "" {
		c.Response().Header().Set("Content-Type", contentType)
	}
	c.Response().WriteHeader(statusCode)
	_, err := io.Copy(c.Response().Writer, body)
	return err
}

// forwardStream applies SSE-aware PII rewriting as the response flows
// to the client. provider selects the dialect (openai vs anthropic);
// it comes from cfg.Proxy.Provider on the cloud-proxy backend.
func forwardStream(c echo.Context, body io.Reader, provider string, filter *pii.StreamFilter) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	emit := func(line string) error {
		_, err := fmt.Fprint(c.Response().Writer, line)
		if err != nil {
			return err
		}
		c.Response().Flush()
		return nil
	}

	flushResidual := func() {
		if filter == nil {
			return
		}
		residual := filter.Drain()
		if residual == "" {
			return
		}
		if line := ssewire.SynthResidualEvent(ssewire.Provider(provider), residual); line != "" {
			_ = emit(line)
		}
	}

	prov := ssewire.Provider(provider)
	scanner := ssewire.NewScanner(body)
	for scanner.Scan() {
		ev := scanner.Event()
		if ssewire.IsTerminalMarker(ev.DataLine, prov) {
			flushResidual()
			_ = emit(ev.Raw)
			continue
		}
		out := ev.Raw
		if filter != nil && ev.DataLine != "" {
			rewritten, drop := ssewire.RewritePayload(ev.DataLine, prov, filter)
			if drop {
				continue
			}
			if rewritten != ev.DataLine {
				// strings.Replace with n=1 touches only the data line,
				// preserving any "event:"/"id:" preamble.
				out = strings.Replace(ev.Raw, ev.DataLine, rewritten, 1)
			}
		}
		if err := emit(out); err != nil {
			return nil
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		xlog.Debug("cloudproxy: stream read error", "error", err)
	}
	flushResidual()
	return nil
}
