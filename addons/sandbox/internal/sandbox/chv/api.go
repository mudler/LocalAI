package chv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// api is a client of the Cloud Hypervisor REST API on a Unix socket. The
// server answers 204 (or 200) on success and a JSON array of messages on an
// error.
type api struct {
	c *http.Client
}

func newAPI(sock string) *api {
	return &api{c: &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}},
	}}
}

func (a *api) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost/api/v1/"+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		var msgs []string
		if json.Unmarshal(out, &msgs) == nil && len(msgs) > 0 {
			return nil, fmt.Errorf("chv: %s %s: %d %v", method, path, resp.StatusCode, msgs)
		}
		return nil, fmt.Errorf("chv: %s %s: %d %s", method, path, resp.StatusCode, bytes.TrimSpace(out))
	}
	return out, nil
}

// put calls a PUT endpoint: vm.pause, vm.resume, vm.shutdown, vmm.shutdown,
// vm.snapshot {"destination_url"}, vm.restore {"source_url"}.
func (a *api) put(ctx context.Context, path string, body any) error {
	_, err := a.do(ctx, http.MethodPut, path, body)
	return err
}

// ping returns the VMM version, for example "54.0.0".
func (a *api) ping(ctx context.Context) (string, error) {
	out, err := a.do(ctx, http.MethodGet, "vmm.ping", nil)
	if err != nil {
		return "", err
	}
	var r struct {
		Version string `json:"version"`
	}
	return r.Version, json.Unmarshal(out, &r)
}

// state returns the VM state: Created, Running, Shutdown, Paused or
// BreakPoint.
func (a *api) state(ctx context.Context) (string, error) {
	out, err := a.do(ctx, http.MethodGet, "vm.info", nil)
	if err != nil {
		return "", err
	}
	var r struct {
		State string `json:"state"`
	}
	return r.State, json.Unmarshal(out, &r)
}

type snapshotConfig struct {
	DestinationURL string `json:"destination_url"`
}

type restoreConfig struct {
	SourceURL string `json:"source_url"`
}
