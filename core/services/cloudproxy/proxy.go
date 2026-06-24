// Package cloudproxy stitches the cloud-proxy gRPC backend to the
// HTTP edge: model rewrite and body shaping. The outbound HTTP request
// itself lives inside the cloud-proxy backend binary
// (backend/go/cloud-proxy), not here — this package is the core-side
// glue. PII redaction runs request-side (the NER middleware + MITM
// input path); response/output is forwarded unmodified.
package cloudproxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
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

// forwardStream relays the upstream SSE response to the client,
// flushing per read so events arrive in real time. Response/output PII
// redaction is out of scope for now, so the stream is forwarded
// unmodified.
func forwardStream(c echo.Context, body io.Reader) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	buf := make([]byte, 32*1024)
	for {
		n, rErr := body.Read(buf)
		if n > 0 {
			if _, wErr := c.Response().Writer.Write(buf[:n]); wErr != nil {
				return nil
			}
			c.Response().Flush()
		}
		if rErr != nil {
			if rErr != io.EOF {
				xlog.Debug("cloudproxy: stream read error", "error", rErr)
			}
			return nil
		}
	}
}
