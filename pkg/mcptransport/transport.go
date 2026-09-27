// SPDX-License-Identifier: MIT

// Package mcptransport connects remote MCP servers using Streamable HTTP or legacy SSE.
package mcptransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Connect prefers Streamable HTTP and retries with legacy SSE only when the
// initial POST is rejected with 400, 404, or 405. The timeout covers both
// handshakes; an established session lives until it is closed or ctx is canceled.
func Connect(ctx context.Context, client *mcp.Client, endpoint string, httpClient *http.Client, timeout time.Duration) (*mcp.ClientSession, error) {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := deadline.Err(); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = httpclient.New()
	}
	// Copy the client so its credentials, redirect policy and other settings are
	// retained without mutating a client that other sessions may share.
	streamClient := *httpClient
	base := streamClient.Transport
	if base == nil {
		base = httpclient.HardenedTransport()
	}
	status := &initializeStatus{base: base}
	streamClient.Transport = status
	session, err := connect(ctx, deadline, client, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &streamClient}, &streamClient)
	if err == nil {
		return session, nil
	}
	if deadline.Err() != nil {
		return nil, deadline.Err()
	}
	switch status.code.Load() {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed:
		origin, parseErr := url.Parse(endpoint)
		if parseErr != nil {
			return nil, parseErr
		}
		sseClient := *httpClient
		// SSE servers advertise a separate POST endpoint. Reject foreign origins
		// before a credential-injecting transport can see the request.
		sseClient.Transport = &sameOriginTransport{base: base, origin: origin}
		return connect(ctx, deadline, client, &mcp.SSEClientTransport{Endpoint: endpoint, HTTPClient: &sseClient}, &sseClient)
	default:
		return nil, err
	}
}

// The SDK sends initialize as the first POST. Keep its status, rather than
// allowing the initialized notification or a background GET to overwrite it.
type initializeStatus struct {
	base http.RoundTripper
	seen atomic.Bool
	code atomic.Int32
}

func (t *initializeStatus) RoundTrip(r *http.Request) (*http.Response, error) {
	initial := r.Method == http.MethodPost && t.seen.CompareAndSwap(false, true)
	response, err := t.base.RoundTrip(r)
	if initial && err == nil {
		t.code.Store(int32(response.StatusCode))
	}
	return response, err
}

type sameOriginTransport struct {
	base   http.RoundTripper
	origin *url.URL
}

func (t *sameOriginTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.EqualFold(r.URL.Scheme, t.origin.Scheme) || !strings.EqualFold(r.URL.Host, t.origin.Host) {
		return nil, errors.New("MCP SSE endpoint must use the configured server's origin")
	}
	return t.base.RoundTrip(r)
}

// Keep the SDK's concrete connection intact: its private sessionUpdated method
// is needed to set Streamable HTTP protocol headers and start its event stream.
type attemptTransport struct {
	mcp.Transport
	cleanup func()
}

func (t *attemptTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	t.cleanup = func() { stop(); _ = connection.Close() }
	return connection, nil
}

func connect(ctx, deadline context.Context, client *mcp.Client, transport mcp.Transport, httpClient *http.Client) (*mcp.ClientSession, error) {
	if err := deadline.Err(); err != nil {
		return nil, err
	}
	attemptCtx, cancel := context.WithCancel(ctx)
	httpClient.Transport = &attemptRequests{base: httpClient.Transport, ctx: attemptCtx}
	attempt := &attemptTransport{Transport: transport}
	type result struct {
		session *mcp.ClientSession
		err     error
	}
	done := make(chan result)
	go func() {
		session, err := client.Connect(attemptCtx, attempt, nil)
		cleanup := func() {
			cancel()
			if attempt.cleanup != nil {
				attempt.cleanup()
			}
		}
		if err != nil {
			cleanup()
		}
		select {
		case done <- result{session, err}:
			if session != nil {
				// Release cancellation hooks when callers close a successful session.
				_ = session.Wait()
				cleanup()
			}
		case <-deadline.Done():
			// An unbuffered handoff prevents a late successful session being orphaned.
			cleanup()
			if session != nil {
				_ = session.Close()
			}
		}
	}()
	select {
	case result := <-done:
		if err := deadline.Err(); err != nil {
			cancel()
			return nil, err
		}
		return result.session, result.err
	case <-deadline.Done():
		cancel()
		return nil, deadline.Err()
	}
}

// The SDK detaches cleanup requests and cancellation notifications from the
// handshake context. Bind those requests back to the attempt lifetime too, so
// an unreachable server cannot keep a failed handshake goroutine alive.
type attemptRequests struct {
	base http.RoundTripper
	ctx  context.Context
}

func (t *attemptRequests) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	response, err := t.base.RoundTrip(r.Clone(ctx))
	cleanup := func() { stop(); cancel() }
	if err != nil {
		cleanup()
		return response, err
	}
	response.Body = &attemptBody{ReadCloser: response.Body, cleanup: cleanup}
	return response, nil
}

type attemptBody struct {
	io.ReadCloser
	cleanup func()
}

func (b *attemptBody) Close() error {
	defer b.cleanup()
	return b.ReadCloser.Close()
}
