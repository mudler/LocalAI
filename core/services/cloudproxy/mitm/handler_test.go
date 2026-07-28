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

// substringDetector is a deterministic pii.NERDetector for tests: it
// reports an entity for every occurrence of each configured substring,
// with byte offsets into the scanned text. Lets the MITM tests drive
// request redaction without a real token-classification backend.
type substringDetector struct{ groups map[string]string } // substring -> entity group

func (d substringDetector) Detect(_ context.Context, text string) ([]pii.NEREntity, error) {
	var out []pii.NEREntity
	for sub, group := range d.groups {
		for idx := 0; ; {
			i := strings.Index(text[idx:], sub)
			if i < 0 {
				break
			}
			start := idx + i
			out = append(out, pii.NEREntity{Group: group, Start: start, End: start + len(sub), Score: 1})
			idx = start + len(sub)
		}
	}
	return out, nil
}

// testDetectorCfg flags emails (mask) and a known secret token (block).
func testDetectorCfg() pii.NERConfig {
	return pii.NERConfig{
		Detector: substringDetector{groups: map[string]string{
			"alice@example.com":                 "EMAIL",
			"bob@example.org":                   "EMAIL",
			"sk-abcdefghijklmnopqrstuvwxyz1234": "PASSWORD",
		}},
		EntityActions: map[string]pii.Action{"EMAIL": pii.ActionMask, "PASSWORD": pii.ActionBlock},
	}
}

// startPIITestRig plugs the production PII handler into a CONNECT proxy,
// with the upstream playing the role of api.anthropic.com. Request
// bodies bound for api.anthropic.com run through the NER detector above.
func startPIITestRig(upstream http.Handler) (*http.Client, string, *fakeStore, func()) {
	ts := httptest.NewTLSServer(upstream)
	upstreamCertPool := x509.NewCertPool()
	upstreamCertPool.AddCert(ts.Certificate())
	upstreamURL, _ := url.Parse(ts.URL)
	store := &fakeStore{}

	ca, err := NewInMemoryCA()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())

	upstreamHost := upstreamURL.Host
	prodHandler := NewPIIHandler(PIIHandlerOptions{
		DetectorsByHost: map[string][]pii.NERConfig{
			"api.anthropic.com": {testDetectorCfg()},
		},
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
	It("redacts request email via NER", func() {
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
		Expect(string(receivedBody)).To(ContainSubstring("[REDACTED:ner:EMAIL]"), "upstream did not see redaction marker")
		Expect(store.recorded()).NotTo(BeZero(), "no PIIEvent recorded for the email match")
	})

	It("refuses to follow an upstream redirect", func() {
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

	It("blocks a detected secret in the request", func() {
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
		Expect(resp.StatusCode).To(Equal(400), "PASSWORD entity action is block")
		Expect(upstreamCalled).To(BeFalse(), "upstream was called despite block — proxy should short-circuit")
		body2, _ := io.ReadAll(resp.Body)
		Expect(string(body2)).To(ContainSubstring("pii_blocked"))
	})

	It("non-chat path passes through", func() {
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
		body := []byte(`{"model":"claude","max_tokens":10,"messages":[{"role":"user","content":"reach me at bob@example.org"}]}`)

		d := &piiDispatcher{}
		out, blocked, err := d.redactRequest(context.Background(), body, shapeAnthropicMessages, []pii.NERConfig{testDetectorCfg()}, "corr-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(blocked).To(BeFalse(), "EMAIL is mask, not block — blocked should be false")
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
