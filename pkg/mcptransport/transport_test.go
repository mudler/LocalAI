// SPDX-License-Identifier: MIT
package mcptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/pkg/httpclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo text"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct {
		Text string `json:"text"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args.Text}}}, nil, nil
	})
	return server
}
func testClient() *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
}
func checkTool(session *mcp.ClientSession) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tools, err := session.ListTools(ctx, nil)
	Expect(err).NotTo(HaveOccurred())
	Expect(tools.Tools).To(HaveLen(1))
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "hello"}})
	Expect(err).NotTo(HaveOccurred())
	Expect(result.Content).To(Equal([]mcp.Content{&mcp.TextContent{Text: "hello"}}))
}

var _ = Describe("Remote MCP transport selection", func() {
	DescribeTable("connects to Streamable HTTP without a probe session", func(stateless bool) {
		var initializations atomic.Int32
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return testServer() }, &mcp.StreamableHTTPOptions{Stateless: stateless})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.Header.Get("Mcp-Protocol-Version") == "" {
				initializations.Add(1)
			}
			handler.ServeHTTP(w, r)
		}))
		defer server.Close()
		session, err := Connect(context.Background(), testClient(), server.URL, httpclient.New(), time.Second)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = session.Close() }()
		checkTool(session)
		Expect(initializations.Load()).To(Equal(int32(1)))
	}, Entry("stateful", false), Entry("stateless", true))

	DescribeTable("falls back for a rejected initialize POST and preserves bearer credentials", func(status int) {
		var gets, posts, rejected atomic.Int32
		handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return testServer() }, nil)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer secret" {
				rejected.Add(1)
				http.Error(w, "unauthorized", 401)
				return
			}
			if r.Method == http.MethodPost && r.URL.RawQuery == "" {
				http.Error(w, "use SSE", status)
				return
			}
			if r.Method == http.MethodGet {
				gets.Add(1)
				// The SDK advertises this same-origin path for subsequent messages.
				r.URL.Path = "/messages"
			} else {
				if r.URL.Path != "/messages" {
					http.NotFound(w, r)
					return
				}
				posts.Add(1)
			}
			handler.ServeHTTP(w, r)
		}))
		defer server.Close()
		base := httpclient.HardenedTransport()
		hc := httpclient.New(httpclient.WithTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			clone := r.Clone(r.Context())
			clone.Header.Set("Authorization", "Bearer secret")
			return base.RoundTrip(clone)
		})))
		session, err := Connect(context.Background(), testClient(), server.URL, hc, time.Second)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = session.Close() }()
		checkTool(session)
		Expect(gets.Load()).To(Equal(int32(1)))
		Expect(posts.Load()).To(BeNumerically(">=", 4))
		Expect(rejected.Load()).To(BeZero())
	}, Entry("400", 400), Entry("404", 404), Entry("405", 405))

	DescribeTable("does not fall back on other HTTP errors or follow redirects", func(status int) {
		var gets, redirected atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
		defer target.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				gets.Add(1)
			}
			w.Header().Set("Location", target.URL)
			http.Error(w, "failure", status)
		}))
		defer server.Close()
		session, err := Connect(context.Background(), testClient(), server.URL, httpclient.New(), time.Second)
		Expect(err).To(HaveOccurred())
		Expect(session).To(BeNil())
		Expect(gets.Load()).To(BeZero())
		Expect(redirected.Load()).To(BeZero())
	}, Entry("401", 401), Entry("403", 403), Entry("500", 500), Entry("302", 302), Entry("307", 307))

	It("does not mistake a rejected initialized notification for rejected initialization", func() {
		var gets atomic.Int32
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return testServer() }, &mcp.StreamableHTTPOptions{Stateless: true})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				gets.Add(1)
			}
			if r.Method == http.MethodPost && r.Header.Get("Mcp-Protocol-Version") != "" {
				http.Error(w, "rejected notification", 405)
				return
			}
			handler.ServeHTTP(w, r)
		}))
		defer server.Close()
		session, err := Connect(context.Background(), testClient(), server.URL, httpclient.New(), time.Second)
		Expect(err).To(HaveOccurred())
		Expect(session).To(BeNil())
		Expect(gets.Load()).To(Equal(int32(1)))
	})

	DescribeTable("keeps a connected session alive beyond the discovery timeout", func(legacy bool) {
		var handler http.Handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return testServer() }, nil)
		if legacy {
			handler = mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return testServer() }, nil)
		}
		server := httptest.NewServer(handler)
		defer server.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		session, err := Connect(ctx, testClient(), server.URL, httpclient.New(), 100*time.Millisecond)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = session.Close() }()
		time.Sleep(150 * time.Millisecond)
		checkTool(session)
		cancel()
		done := make(chan struct{})
		go func() { _ = session.Wait(); close(done) }()
		Eventually(done).WithTimeout(time.Second).Should(BeClosed())
	}, Entry("Streamable HTTP", false), Entry("legacy SSE", true))

	DescribeTable("bounds stalled setup and cancels its HTTP requests", func(legacy bool, cancelParent bool) {
		entered := make(chan struct{}, 1)
		canceled := make(chan struct{}, 8)
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if legacy && r.Method == http.MethodPost {
				http.Error(w, "use SSE", 405)
				return
			}
			// Consume initialize so the server can observe the peer disconnecting.
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
				canceled <- struct{}{}
			case <-release:
			}
		}))
		defer server.Close()
		defer close(release)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if cancelParent {
			go func() { <-entered; cancel() }()
		}
		start := time.Now()
		timeout := 100 * time.Millisecond
		if cancelParent {
			timeout = 5 * time.Second
		}
		session, err := Connect(ctx, testClient(), server.URL, httpclient.New(), timeout)
		Expect(session).To(BeNil())
		if cancelParent {
			Expect(err).To(MatchError(context.Canceled))
		} else {
			Expect(err).To(MatchError(context.DeadlineExceeded))
		}
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))
		Eventually(canceled).WithTimeout(time.Second).Should(Receive())
	}, Entry("Streamable timeout", false, false), Entry("SSE timeout", true, false), Entry("Streamable parent cancellation", false, true), Entry("SSE parent cancellation", true, true))

	It("shares one timeout across negotiation attempts", func() {
		var gets atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				time.Sleep(150 * time.Millisecond)
				http.Error(w, "SSE", 405)
				return
			}
			gets.Add(1)
			<-r.Context().Done()
		}))
		defer server.Close()
		start := time.Now()
		_, err := Connect(context.Background(), testClient(), server.URL, httpclient.New(), 250*time.Millisecond)
		Expect(err).To(MatchError(context.DeadlineExceeded))
		Expect(time.Since(start)).To(BeNumerically("<", 350*time.Millisecond))
		Expect(gets.Load()).To(Equal(int32(1)))
	})

	It("does not fall back on a malformed successful initialize response", func() {
		var gets atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				gets.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32603, "message": "bad initialization"}})
		}))
		defer server.Close()
		session, err := Connect(context.Background(), testClient(), server.URL, httpclient.New(), time.Second)
		Expect(err).To(HaveOccurred())
		Expect(session).To(BeNil())
		Expect(gets.Load()).To(BeZero())
	})
	It("rejects a foreign SSE message endpoint before attaching credentials", func() {
		var leaked atomic.Int32
		foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1); http.Error(w, "unexpected", 500) }))
		defer foreign.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "SSE", 405)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/messages\n\n", foreign.URL)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		defer server.Close()
		base := httpclient.HardenedTransport()
		hc := httpclient.New(httpclient.WithTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r.Header.Set("Authorization", "Bearer secret")
			return base.RoundTrip(r)
		})))
		session, err := Connect(context.Background(), testClient(), server.URL, hc, time.Second)
		Expect(session).To(BeNil())
		Expect(err).To(HaveOccurred())
		Expect(leaked.Load()).To(BeZero())
	})

	It("closes a session that completes after the caller times out", func() {
		initialized := make(chan struct{})
		release := make(chan struct{})
		exited := make(chan struct{})
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return testServer() }, nil)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				defer close(exited)
			}
			handler.ServeHTTP(w, r)
		}))
		defer server.Close()
		base := httpclient.HardenedTransport()
		hc := httpclient.New(httpclient.WithTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			response, err := base.RoundTrip(r)
			if r.Method == http.MethodPost && r.Header.Get("Mcp-Protocol-Version") != "" && err == nil {
				close(initialized)
				// Deliberately return late even after cancellation, as a custom transport may.
				<-release
			}
			return response, err
		})))
		client := testClient()
		session, err := Connect(context.Background(), client, server.URL, hc, 100*time.Millisecond)
		close(release)
		Expect(session).To(BeNil())
		Expect(err).To(MatchError(context.DeadlineExceeded))
		Expect(initialized).To(BeClosed())
		Eventually(exited).WithTimeout(time.Second).Should(BeClosed())
	})

})
