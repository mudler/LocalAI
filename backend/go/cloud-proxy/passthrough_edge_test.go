package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("composeURL", func() {
	// Upstream URL convention: gallery configs put the canonical path
	// in upstream_url, so per-request Path is ignored. A bare-host
	// upstream_url accepts the per-request path.
	DescribeTable("path resolution",
		func(upstream, reqPath, want string) {
			got, err := composeURL(upstream, reqPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("full path wins", "https://api.openai.com/v1/chat/completions", "/v1/something-else", "https://api.openai.com/v1/chat/completions"),
		Entry("bare host accepts path", "https://api.openai.com", "/v1/chat/completions", "https://api.openai.com/v1/chat/completions"),
		Entry("root slash treated as bare", "https://api.openai.com/", "/v1/chat/completions", "https://api.openai.com/v1/chat/completions"),
		Entry("bare host + empty path", "https://api.openai.com", "", "https://api.openai.com"),
	)

	It("returns an error on invalid upstream URL", func() {
		_, err := composeURL("://garbage", "")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("applyAuthHeader", func() {
	It("sets x-api-key and anthropic-version for Anthropic, no Authorization", func() {
		req, _ := http.NewRequest("POST", "https://example.com", nil)
		applyAuthHeader(req, providerAnthropic, "ant-key")
		Expect(req.Header.Get("x-api-key")).To(Equal("ant-key"))
		Expect(req.Header.Get("anthropic-version")).NotTo(BeEmpty())
		Expect(req.Header.Get("Authorization")).To(BeEmpty(), "Authorization must not leak on Anthropic backend")
	})

	It("sets Bearer Authorization for OpenAI, no x-api-key", func() {
		req, _ := http.NewRequest("POST", "https://example.com", nil)
		applyAuthHeader(req, providerOpenAI, "sk-key")
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer sk-key"))
		Expect(req.Header.Get("x-api-key")).To(BeEmpty(), "x-api-key must not leak on OpenAI backend")
	})

	It("defaults to Bearer when provider is empty", func() {
		// Passthrough mode often has provider == "" because the operator
		// doesn't claim a specific upstream wire format. Most providers
		// (including OpenAI-compatible ones) accept Bearer, so default to it.
		req, _ := http.NewRequest("POST", "https://example.com", nil)
		applyAuthHeader(req, "", "some-key")
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer some-key"))
	})

	It("preserves an existing anthropic-version header", func() {
		// If the client supplied anthropic-version (rare but legitimate
		// for an upstream pinned to a specific date), the proxy must not
		// clobber it.
		req, _ := http.NewRequest("POST", "https://example.com", nil)
		req.Header.Set("anthropic-version", "2024-10-01")
		applyAuthHeader(req, providerAnthropic, "k")
		Expect(req.Header.Get("anthropic-version")).To(Equal("2024-10-01"))
	})
})

var _ = Describe("isHopByHopHeader", func() {
	DescribeTable("hop-by-hop classification",
		func(header string, want bool) {
			Expect(isHopByHopHeader(header)).To(Equal(want))
		},
		Entry("Connection is hop-by-hop", "Connection", true),
		Entry("Keep-Alive is hop-by-hop", "Keep-Alive", true),
		Entry("Proxy-Connection is hop-by-hop", "Proxy-Connection", true),
		Entry("Transfer-Encoding is hop-by-hop", "Transfer-Encoding", true),
		Entry("TE is hop-by-hop", "TE", true),
		Entry("Trailer is hop-by-hop", "Trailer", true),
		Entry("Upgrade is hop-by-hop", "Upgrade", true),
		Entry("Host is hop-by-hop", "Host", true),
		Entry("Content-Length is hop-by-hop", "Content-Length", true),
		// Case-insensitive — RFC 7230 doesn't constrain header case.
		Entry("lowercase connection is hop-by-hop", "connection", true),
		Entry("uppercase HOST is hop-by-hop", "HOST", true),
		// Non hop-by-hop — must NOT be stripped.
		Entry("Authorization is end-to-end", "Authorization", false),
		Entry("Content-Type is end-to-end", "Content-Type", false),
		Entry("Accept is end-to-end", "Accept", false),
		Entry("X-Custom is end-to-end", "X-Custom", false),
	)
})

var _ = Describe("Forward", func() {
	It("strips hop-by-hop and Connection headers before upstream, preserves custom headers", func() {
		gotConnection := make(chan string, 1)
		gotXCustom := make(chan string, 1)
		gotHost := make(chan string, 1)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotConnection <- r.Header.Get("Connection")
			gotXCustom <- r.Header.Get("X-Custom")
			gotHost <- r.Header.Get("Host")
			w.WriteHeader(http.StatusOK)
		}))
		defer upstream.Close()

		cp := NewCloudProxy()
		Expect(cp.Load(&pb.ModelOptions{
			Proxy: &pb.ProxyOptions{
				UpstreamUrl: upstream.URL,
				Mode:        modePassthrough,
			},
		})).To(Succeed())

		addr := "test://forward-hopbyhop"
		grpc.Provide(addr, cp)
		c := grpc.NewClient(addr, true, nil, false)
		stream, err := c.Forward(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(stream.Send(&pb.ForwardRequest{
			Path:   "/v1/chat/completions",
			Method: "POST",
			Headers: []*pb.ForwardHeader{
				{Name: "Connection", Value: "keep-alive"},
				{Name: "Host", Value: "spoofed.example.com"},
				{Name: "X-Custom", Value: "preserved"},
			},
		})).To(Succeed())
		Expect(stream.CloseSend()).To(Succeed())
		_, _ = stream.Recv()
		for {
			if _, err := stream.Recv(); errors.Is(err, io.EOF) || err != nil {
				break
			}
		}

		Expect(<-gotConnection).To(BeEmpty(), "Connection must not leak to upstream")
		Expect(<-gotHost).NotTo(Equal("spoofed.example.com"), "Host header must not be spoofed through")
		Expect(<-gotXCustom).To(Equal("preserved"), "X-Custom header must survive")
	})

	It("replaces caller-supplied Authorization with the configured key", func() {
		// The proxy must overwrite a client-supplied Authorization header
		// so a downstream caller can't smuggle stale or wrong credentials.
		gotAuth := make(chan string, 1)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth <- r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		}))
		defer upstream.Close()

		GinkgoT().Setenv("CLOUD_PROXY_AUTH_REPLACE_KEY", "sk-real")

		cp := NewCloudProxy()
		Expect(cp.Load(&pb.ModelOptions{
			Proxy: &pb.ProxyOptions{
				UpstreamUrl: upstream.URL,
				Mode:        modePassthrough,
				ApiKeyEnv:   "CLOUD_PROXY_AUTH_REPLACE_KEY",
			},
		})).To(Succeed())

		addr := "test://forward-replaces-auth"
		grpc.Provide(addr, cp)
		c := grpc.NewClient(addr, true, nil, false)
		stream, err := c.Forward(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(stream.Send(&pb.ForwardRequest{
			Path:   "/v1/chat/completions",
			Method: "POST",
			Headers: []*pb.ForwardHeader{
				// Client-supplied Authorization with the wrong scheme / key.
				{Name: "Authorization", Value: "Basic Zm9vOmJhcg=="},
			},
		})).To(Succeed())
		Expect(stream.CloseSend()).To(Succeed())
		_, _ = stream.Recv()
		for {
			if _, err := stream.Recv(); errors.Is(err, io.EOF) || err != nil {
				break
			}
		}

		Expect(<-gotAuth).To(Equal("Bearer sk-real"), "caller-supplied Basic header must be replaced")
	})

	It("refuses to follow upstream redirects and never leaks the key to the redirect target", func() {
		// A 3xx from the configured upstream means misconfiguration or a
		// hijacked/spoofed host. Following it would replay the request —
		// and the injected API key — to the Location host. Anthropic's
		// x-api-key is NOT stripped by Go on cross-host redirects, so this
		// would be a credential leak. The proxy must refuse the redirect.
		sinkHit := make(chan string, 1)
		sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sinkHit <- r.Header.Get("x-api-key")
			w.WriteHeader(http.StatusOK)
		}))
		defer sink.Close()

		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, sink.URL, http.StatusFound)
		}))
		defer redirector.Close()

		GinkgoT().Setenv("CLOUD_PROXY_REDIRECT_KEY", "ant-secret")

		cp := NewCloudProxy()
		Expect(cp.Load(&pb.ModelOptions{
			Proxy: &pb.ProxyOptions{
				UpstreamUrl: redirector.URL,
				Mode:        modePassthrough,
				Provider:    providerAnthropic,
				ApiKeyEnv:   "CLOUD_PROXY_REDIRECT_KEY",
			},
		})).To(Succeed())

		addr := "test://forward-no-redirect"
		grpc.Provide(addr, cp)
		c := grpc.NewClient(addr, true, nil, false)
		stream, err := c.Forward(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(stream.Send(&pb.ForwardRequest{
			Path:   "/v1/messages",
			Method: "POST",
		})).To(Succeed())
		Expect(stream.CloseSend()).To(Succeed())

		// Drain the stream; a refused redirect surfaces as a non-EOF error.
		var streamErr error
		for {
			if _, err := stream.Recv(); err != nil {
				if !errors.Is(err, io.EOF) {
					streamErr = err
				}
				break
			}
		}
		Expect(streamErr).To(HaveOccurred(), "refused redirect must surface as an error")
		Expect(sinkHit).NotTo(Receive(), "the redirect target must never be contacted")
	})

	It("handles concurrent calls without interference", func() {
		// CloudProxy explicitly omits base.SingleThread — independent
		// Forward streams must not block each other or leak state.
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		}))
		defer upstream.Close()

		cp := NewCloudProxy()
		Expect(cp.Load(&pb.ModelOptions{
			Proxy: &pb.ProxyOptions{
				UpstreamUrl: upstream.URL,
				Mode:        modePassthrough,
			},
		})).To(Succeed())
		addr := "test://forward-concurrent"
		grpc.Provide(addr, cp)
		c := grpc.NewClient(addr, true, nil, false)

		const N = 8
		var wg sync.WaitGroup
		errs := make(chan error, N)
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				stream, err := c.Forward(context.Background())
				if err != nil {
					errs <- err
					return
				}
				payload := "request-" + string(rune('A'+idx))
				if err := stream.Send(&pb.ForwardRequest{
					Path:      "/v1/chat/completions",
					Method:    "POST",
					BodyChunk: []byte(payload),
				}); err != nil {
					errs <- err
					return
				}
				_ = stream.CloseSend()
				_, _ = stream.Recv()
				var body []byte
				for {
					r, err := stream.Recv()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						errs <- err
						return
					}
					body = append(body, r.GetBodyChunk()...)
				}
				if string(body) != payload {
					errs <- &echoMismatch{want: payload, got: string(body)}
				}
			}(i)
		}
		wg.Wait()
		close(errs)
		var collected []error
		for err := range errs {
			collected = append(collected, err)
		}
		Expect(collected).To(BeEmpty(), "no concurrent Forward call should fail")
	})
})

type echoMismatch struct{ want, got string }

func (e *echoMismatch) Error() string {
	return "echo mismatch: want " + strconv.Quote(e.want) + " got " + strconv.Quote(e.got)
}
