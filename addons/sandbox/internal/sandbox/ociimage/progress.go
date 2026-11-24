package ociimage

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// Progress counts the bytes a pull has read from the registry and the bytes
// it expects, so a caller can show how far an image is. Put one in the context
// of Prepare with WithProgress and read it from any goroutine. The total is
// the sum of the compressed layer sizes of the manifest; the count is the
// compressed bytes read, so the two are the same unit. The image is
// converted while it streams in, so the count is the progress of the whole
// preparation.
type Progress struct {
	done, total atomic.Int64
}

// Fraction is the part of the image that was read, from 0 to 1. It is 0
// until the total is known and never above 1.
func (p *Progress) Fraction() float64 {
	t := p.total.Load()
	if t <= 0 {
		return 0
	}
	f := float64(p.done.Load()) / float64(t)
	if f > 1 {
		return 1
	}
	return f
}

// Bytes returns the bytes read and the bytes expected.
func (p *Progress) Bytes() (done, total int64) { return p.done.Load(), p.total.Load() }

type progressKey struct{}

// WithProgress returns a context whose pulls report to p.
func WithProgress(ctx context.Context, p *Progress) context.Context {
	return context.WithValue(ctx, progressKey{}, p)
}

func progressFrom(ctx context.Context) *Progress {
	p, _ := ctx.Value(progressKey{}).(*Progress)
	return p
}

// countingTransport counts the bodies of the answers that are not
// manifests or token requests: those are the layers, also when a registry
// redirects a layer to a content host.
type countingTransport struct {
	base http.RoundTripper
	p    *Progress
}

func (t countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil || resp == nil || resp.Body == nil || resp.StatusCode/100 != 2 {
		return resp, err
	}
	path := r.URL.Path
	if strings.Contains(path, "/manifests/") || strings.Contains(path, "/token") || strings.HasSuffix(path, "/v2/") {
		return resp, err
	}
	resp.Body = &countingBody{ReadCloser: resp.Body, p: t.p}
	return resp, err
}

type countingBody struct {
	io.ReadCloser
	p *Progress
}

func (b *countingBody) Read(buf []byte) (int, error) {
	n, err := b.ReadCloser.Read(buf)
	b.p.done.Add(int64(n))
	return n, err
}
