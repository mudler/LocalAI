package mitm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/mudler/LocalAI/core/services/routing/pii"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// startPIITestRig is the same shape as startMITMTestRig but plugs
// in the production PII handler instead of the passthrough fixture.
// The "host" the client thinks it's reaching is forced to
// api.anthropic.com so the request shape classifier matches.
func startPIITestRig(upstream http.Handler) (*http.Client, string, *fakeStore, func()) {
	// Upstream fake — plays the role of api.anthropic.com.
	ts := httptest.NewTLSServer(upstream)
	upstreamCertPool := x509.NewCertPool()
	upstreamCertPool.AddCert(ts.Certificate())
	upstreamURL, _ := url.Parse(ts.URL)

	// Compiled patterns required for the redactor to actually fire
	// (DefaultPatterns alone returns Pattern structs without regex).
	patterns, err := pii.Compile(pii.DefaultPatterns())
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	redactor := pii.NewRedactor(patterns)
	store := &fakeStore{}

	ca, err := NewInMemoryCA()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())

	// DialHost remaps the upstream dial target to the httptest
	// fake while leaving the classifier-facing host
	// ("api.anthropic.com") untouched. ServerName=example.com is
	// what httptest.NewTLSServer issues its cert for.
	upstreamHost := upstreamURL.Host
	prodHandler := NewPIIHandler(PIIHandlerOptions{
		Redactor:   redactor,
		EventStore: store,
		UpstreamTLS: &tls.Config{
			RootCAs:    upstreamCertPool,
			ServerName: "example.com",
		},
		DialHost: func(_ string) string { return upstreamHost },
	})

	srv, err := NewServer(Config{
		Addr:           "127.0.0.1:0",
		CA:             ca,
		InterceptHosts: []string{"api.anthropic.com"},
		Handler:        prodHandler,
		EventStore:     store,
	})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, srv.Start()).To(Succeed())

	clientPool := x509.NewCertPool()
	clientPool.AddCert(ca.Cert())
	proxyURL, _ := url.Parse("http://" + srv.Addr())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: clientPool},
		},
	}

	cleanup := func() {
		srv.Stop()
		ts.Close()
	}
	// We point requests at api.anthropic.com so classifyRequestShape
	// matches; the wrappedHandler retargets to the upstream fake.
	return client, "https://api.anthropic.com", store, cleanup
}

type fakeStore struct{ events []pii.PIIEvent }

func (s *fakeStore) Record(_ context.Context, ev pii.PIIEvent) error {
	s.events = append(s.events, ev)
	return nil
}

func (s *fakeStore) List(_ context.Context, _ pii.ListQuery) ([]pii.PIIEvent, error) {
	return s.events, nil
}

func (s *fakeStore) Count(_ context.Context) (int, error) { return len(s.events), nil }
func (s *fakeStore) Close() error                         { return nil }

func (s *fakeStore) recorded() int { return len(s.events) }

var _ = Describe("PIIHandler", func() {
	It("redacts request email", func() {
		var receivedBody []byte
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"msg_x","content":[{"type":"text","text":"ok"}]}`)
		})

		client, base, store, cleanup := startPIITestRig(upstream)
		defer cleanup()

		body := `{"model":"claude-3-5-sonnet","max_tokens":100,"messages":[{"role":"user","content":"my email is alice@example.com please reply"}]}`
		resp, err := client.Post(base+"/v1/messages", "application/json", strings.NewReader(body))
		Expect(err).NotTo(HaveOccurred(), "client.Post")
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(200))

		Expect(string(receivedBody)).NotTo(ContainSubstring("alice@example.com"), "upstream received unredacted body")
		Expect(string(receivedBody)).To(ContainSubstring("[REDACTED:email]"), "upstream did not see redaction marker")
		Expect(store.recorded()).NotTo(BeZero(), "no PIIEvent recorded for the email match")
	})

	It("refuses to follow an upstream redirect", func() {
		// A 3xx from the upstream would otherwise be followed, replaying
		// the request (and its provider API key, e.g. Anthropic's
		// x-api-key which Go does NOT strip on cross-host redirects) to
		// the Location host. The refused redirect surfaces as a 502.
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://evil.example.com/steal", http.StatusFound)
		})

		client, base, _, cleanup := startPIITestRig(upstream)
		defer cleanup()

		body := `{"model":"claude-3-5-sonnet","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`
		resp, err := client.Post(base+"/v1/messages", "application/json", strings.NewReader(body))
		Expect(err).NotTo(HaveOccurred(), "client.Post")
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusBadGateway), "refused redirect must surface as 502, not be followed")
	})

	It("blocks api key in request", func() {
		upstreamCalled := false
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upstreamCalled = true
			w.WriteHeader(200)
		})

		client, base, _, cleanup := startPIITestRig(upstream)
		defer cleanup()

		body := `{"model":"claude-3-5-sonnet","max_tokens":100,"messages":[{"role":"user","content":"my key is sk-abcdefghijklmnopqrstuvwxyz1234"}]}`
		resp, err := client.Post(base+"/v1/messages", "application/json", strings.NewReader(body))
		Expect(err).NotTo(HaveOccurred(), "client.Post")
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(400), "api_key_prefix has Block default")
		Expect(upstreamCalled).To(BeFalse(), "upstream was called despite block — proxy should short-circuit")
		body2, _ := io.ReadAll(resp.Body)
		Expect(string(body2)).To(ContainSubstring("pii_blocked"))
	})

	It("streaming redaction", func() {
		// Anthropic-shape SSE; "alice@" + "example.com" splits the
		// email across chunks so the StreamFilter has to buffer.
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			flusher := w.(http.Flusher)
			chunks := []string{
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"contact me at alice@"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"example.com any time"}}`,
				`{"type":"message_stop"}`,
			}
			for _, c := range chunks {
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", "content_block_delta", c)
				flusher.Flush()
			}
		})

		client, base, _, cleanup := startPIITestRig(upstream)
		defer cleanup()

		body := `{"model":"claude-3-5-sonnet","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
		resp, err := client.Post(base+"/v1/messages", "application/json", strings.NewReader(body))
		Expect(err).NotTo(HaveOccurred(), "Post")
		defer func() { _ = resp.Body.Close() }()
		out, _ := io.ReadAll(resp.Body)
		outStr := string(out)
		Expect(outStr).NotTo(ContainSubstring("alice@example.com"), "email leaked through MITM stream")
		Expect(outStr).To(ContainSubstring("[REDACTED:email]"), "redaction marker missing from MITM stream")
	})

	It("non-chat path passes through", func() {
		// A path the classifier doesn't recognise (e.g. an OAuth
		// callback) must forward the body verbatim, no PII parsing.
		var receivedBody []byte
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		})

		client, base, _, cleanup := startPIITestRig(upstream)
		defer cleanup()

		body := `{"email":"alice@example.com"}`
		resp, err := client.Post(base+"/oauth/callback", "application/json", strings.NewReader(body))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(string(receivedBody)).To(Equal(body), "body forwarded with mutation")
	})
})

var _ = Describe("redactRequest", func() {
	It("handles anthropic shape", func() {
		patterns, _ := pii.Compile(pii.DefaultPatterns())
		r := pii.NewRedactor(patterns)
		body := []byte(`{"model":"claude","max_tokens":10,"messages":[{"role":"user","content":"reach me at bob@example.org"}]}`)

		d := &piiDispatcher{redactor: r, patternAction: map[string]pii.Action{}}
		out, blocked, err := d.redactRequest(body, shapeAnthropicMessages, "corr-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(blocked).To(BeFalse(), "email is mask, not block — blocked should be false")
		var parsed map[string]any
		Expect(json.Unmarshal(out, &parsed)).To(Succeed())
		msgs := parsed["messages"].([]any)
		first := msgs[0].(map[string]any)
		content, _ := first["content"].(string)
		Expect(content).NotTo(ContainSubstring("bob@example.org"), "redaction did not run")
	})
})

var _ = Describe("Proxy events", func() {
	It("emits connect and traffic events", func() {
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"msg_x","content":[{"type":"text","text":"ok"}]}`)
		})

		client, base, store, cleanup := startPIITestRig(upstream)
		defer cleanup()

		body := `{"model":"claude-3-5-sonnet","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`
		resp, err := client.Post(base+"/v1/messages", "application/json", strings.NewReader(body))
		Expect(err).NotTo(HaveOccurred(), "client.Post")
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)

		var connect, traffic *pii.PIIEvent
		for i := range store.events {
			ev := &store.events[i]
			switch ev.ResolvedKind() {
			case pii.KindProxyConnect:
				connect = ev
			case pii.KindProxyTraffic:
				traffic = ev
			}
		}

		Expect(connect).NotTo(BeNil(), "no proxy_connect event recorded")
		Expect(connect.Host).To(Equal("api.anthropic.com"))
		Expect(connect.Intercepted).NotTo(BeNil())
		Expect(*connect.Intercepted).To(BeTrue(), "connect.Intercepted should be true for an allowlisted host")

		Expect(traffic).NotTo(BeNil(), "no proxy_traffic event recorded")
		Expect(traffic.Host).To(Equal("api.anthropic.com"))
		Expect(traffic.BytesSent).To(BeNumerically(">", 0))
		Expect(traffic.BytesReceived).To(BeNumerically(">", 0))
		Expect(traffic.StatusCode).To(Equal(200))
	})

	It("tunneled host emits connect event only", func() {
		// A non-allowlisted CONNECT must record a proxy_connect with
		// Intercepted=false and NOT a proxy_traffic event (tunneled
		// bytes never reach the dispatcher).
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, "passthrough")
		})
		ts := httptest.NewTLSServer(upstream)
		defer ts.Close()
		upstreamURL, _ := url.Parse(ts.URL)
		upstreamHost, _, _ := net.SplitHostPort(upstreamURL.Host)

		ca, _ := NewInMemoryCA()
		store := &fakeStore{}
		srv, err := NewServer(Config{
			Addr:           "127.0.0.1:0",
			CA:             ca,
			InterceptHosts: []string{"some-other-host"},
			Handler:        func(w http.ResponseWriter, r *http.Request, h string) {},
			EventStore:     store,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(srv.Start()).To(Succeed())
		defer srv.Stop()

		upstreamCertPool := x509.NewCertPool()
		upstreamCertPool.AddCert(ts.Certificate())
		proxyURL, _ := url.Parse("http://" + srv.Addr())
		client := &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
				TLSClientConfig: &tls.Config{
					RootCAs:    upstreamCertPool,
					ServerName: upstreamHost,
				},
			},
		}
		resp, err := client.Get(ts.URL)
		Expect(err).NotTo(HaveOccurred(), "Get through tunnel")
		_ = resp.Body.Close()

		var connect *pii.PIIEvent
		for i := range store.events {
			ev := &store.events[i]
			Expect(ev.ResolvedKind()).NotTo(Equal(pii.KindProxyTraffic), "unexpected proxy_traffic event for tunneled host: %+v", ev)
			if ev.ResolvedKind() == pii.KindProxyConnect {
				connect = ev
			}
		}
		Expect(connect).NotTo(BeNil(), "no proxy_connect event recorded for tunneled host")
		Expect(connect.Intercepted).NotTo(BeNil())
		Expect(*connect.Intercepted).To(BeFalse(), "connect.Intercepted should be false (tunneled)")
		Expect(connect.Host).NotTo(BeEmpty())
	})
})

var _ = Describe("classifyRequestShape", func() {
	cases := []struct {
		host string
		path string
		want requestShape
	}{
		{"api.anthropic.com", "/v1/messages", shapeAnthropicMessages},
		{"api.openai.com", "/v1/chat/completions", shapeOpenAIChat},
		{"api.anthropic.com", "/v1/oauth/token", shapeUnknown},
		{"api.openai.com", "/v1/embeddings", shapeUnknown},
		{"example.com", "/v1/messages", shapeUnknown},
	}
	for _, c := range cases {
		It(fmt.Sprintf("classifies (%q, %q)", c.host, c.path), func() {
			Expect(classifyRequestShape(c.host, c.path)).To(Equal(c.want))
		})
	}
})
