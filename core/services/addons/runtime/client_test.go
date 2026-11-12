//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

func envelope(scope string) contract.Envelope {
	return contract.Envelope{Request: contract.Request{AddonID: "sample", Generation: 1, OperationID: "operation", Action: "read", Arguments: json.RawMessage(`{"actor":"not-authority"}`)}, Authority: contract.Authority{Actor: contract.Actor{UserID: "alice", CredentialID: "key", Mode: "named-user"}, Scope: scope, ExpiresAt: time.Now().Add(time.Minute)}}
}

var _ = Describe("Authenticated runtime client", func() {
	It("authenticates every request and separates trusted authority from model arguments", func() {
		s := supervisor()
		i, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).NotTo(HaveOccurred())
		req, e := http.NewRequest("POST", i.process.client.endpoint+"/read", nil)
		Expect(e).NotTo(HaveOccurred())
		resp, e := httpclient.New().Do(req)
		Expect(e).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(401))
		_ = resp.Body.Close()
		r, e := i.Client.Read(context.Background(), envelope("read"))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(r.Payload)).To(ContainSubstring("alice"))
		bad := envelope("read")
		bad.Request.Generation = 2
		_, e = i.Client.Read(context.Background(), bad)
		Expect(e).To(HaveOccurred())
		bad = envelope("apply")
		_, e = i.Client.Read(context.Background(), bad)
		Expect(e).To(HaveOccurred())
		p, e := i.Client.Preview(context.Background(), envelope("preview"))
		Expect(e).NotTo(HaveOccurred())
		receipt, e := i.Client.Apply(context.Background(), envelope("apply"), p.Revision)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
		receipt, e = i.Client.Receipt(context.Background(), "operation")
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
	})
	It("bounds output streams cancellation deadlines and stop admission", func() {
		s := supervisor()
		// Process startup and successful stream headers are setup, not the
		// deadline under test. Allow the same startup budget as host-op specs.
		s.config.ReadyTimeout = 2 * time.Second
		s.config.Limits.ReadTimeout = 2 * time.Second
		i, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).NotTo(HaveOccurred())
		stream, e := i.Client.OpenOutput(context.Background(), envelope("output"), "file")
		Expect(e).NotTo(HaveOccurred())
		b, e := io.ReadAll(stream)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal("fixture\n"))
		Expect(stream.Close()).To(Succeed())
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		_, e = i.Client.Read(canceled, envelope("read"))
		Expect(e).To(HaveOccurred())
		slow := envelope("read")
		slow.Request.Action = "delay"
		client := i.Client.(*Client)
		client.limits.ReadTimeout = 300 * time.Millisecond
		start := time.Now()
		_, e = i.Client.Read(context.Background(), slow)
		Expect(e).To(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))
		client.limits.ReadTimeout = 2 * time.Second
		stream, e = i.Client.OpenOutput(context.Background(), envelope("output"), "slow")
		Expect(e).NotTo(HaveOccurred())
		start = time.Now()
		Expect(s.Stop(context.Background(), i)).To(Succeed())
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))
		_, e = i.Client.Read(context.Background(), envelope("read"))
		Expect(e).To(HaveOccurred())
		_ = stream.Close()
	})
})

var _ = Describe("Refusals of the add-on", func() {
	It("tells a 409 conflict of the add-on from an outage", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/apply" {
				http.Error(w, "conflict", http.StatusConflict)
				return
			}
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		c, e := newClient(Registration{AddonID: "sample", Generation: 1, Endpoint: srv.URL}, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(e).NotTo(HaveOccurred())
		c.accept = true
		_, e = c.Apply(context.Background(), envelope("apply"), contract.Revision{Schema: 1})
		Expect(errors.Is(e, ErrConflict)).To(BeTrue())
		Expect(errors.Is(e, ErrRuntime)).To(BeTrue())
		_, e = c.Read(context.Background(), envelope("read"))
		Expect(e).To(MatchError(ErrRuntime))
		Expect(errors.Is(e, ErrConflict)).To(BeFalse())
	})
})

var _ = Describe("Invalid-request refusals of the add-on", func() {
	refusing := func(status int, body string) *Client {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, body, status)
		}))
		DeferCleanup(srv.Close)
		c, e := newClient(Registration{AddonID: "sample", Generation: 1, Endpoint: srv.URL}, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(e).NotTo(HaveOccurred())
		c.accept = true
		return c
	}
	DescribeTable("types a 400 or 422 answer as a refusal, never as an outage",
		func(status int) {
			c := refusing(status, "invalid request")
			for name, call := range map[string]func() error{
				"read":    func() error { _, e := c.Read(context.Background(), envelope("read")); return e },
				"preview": func() error { _, e := c.Preview(context.Background(), envelope("preview")); return e },
				"apply": func() error {
					_, e := c.Apply(context.Background(), envelope("apply"), contract.Revision{Schema: 1})
					return e
				},
				"output": func() error { _, e := c.OpenOutput(context.Background(), envelope("output"), "h"); return e },
			} {
				e := call()
				Expect(errors.Is(e, ErrRefused)).To(BeTrue(), name)
				Expect(errors.Is(e, contract.ErrRefused)).To(BeTrue(), name)
				Expect(errors.Is(e, ErrRuntime)).To(BeFalse(), name)
				Expect(errors.Is(e, ErrConflict)).To(BeFalse(), name)
			}
		},
		Entry("an HTTP 400 answer", http.StatusBadRequest),
		Entry("an HTTP 422 answer", http.StatusUnprocessableEntity),
	)
	It("keeps a bounded, printable reason and nothing else of the body", func() {
		c := refusing(http.StatusBadRequest, "secret-looking "+strings.Repeat("x", 4096)+"\x00\n")
		_, e := c.Read(context.Background(), envelope("read"))
		var r *Refusal
		Expect(errors.As(e, &r)).To(BeTrue())
		Expect(r.Status).To(Equal(http.StatusBadRequest))
		Expect(len(r.Reason)).To(BeNumerically("<=", 64))
		Expect(r.Reason).NotTo(ContainSubstring("\x00"))
	})
	It("leaves 503, 500 and 404 as outages", func() {
		for _, status := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError, http.StatusNotFound} {
			c := refusing(status, "no")
			_, e := c.Read(context.Background(), envelope("read"))
			Expect(errors.Is(e, ErrRefused)).To(BeFalse(), http.StatusText(status))
			Expect(e).To(MatchError(ErrRuntime))
		}
	})
})

var _ = Describe("Refusals that come from the service behind the add-on", func() {
	answering := func(status int, headers map[string]string) *Client {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range headers {
				w.Header().Set(k, v)
			}
			http.Error(w, "the token has no permission for this", status)
		}))
		DeferCleanup(srv.Close)
		c, e := newClient(Registration{AddonID: "sample", Generation: 1, Endpoint: srv.URL}, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(e).NotTo(HaveOccurred())
		c.accept = true
		return c
	}
	refusal := func(c *Client) *Refusal {
		_, e := c.Read(context.Background(), envelope("read"))
		var r *Refusal
		Expect(errors.As(e, &r)).To(BeTrue(), "%v", e)
		Expect(errors.Is(e, contract.ErrRefused)).To(BeTrue())
		return r
	}
	It("carries the status of the service and the sentence of the add-on", func() {
		r := refusal(answering(http.StatusBadRequest, map[string]string{
			contract.HeaderUpstreamStatus: "403",
			contract.HeaderUpstreamHint:   "The token cannot create issues in this repository (403). Give it Issues: write.",
		}))
		Expect(r.Upstream).To(Equal(403))
		Expect(r.Hint).To(Equal("The token cannot create issues in this repository (403). Give it Issues: write."))
		Expect(r.Reason).To(Equal("the token has no permission for this"))
	})
	DescribeTable("ignores a status that is not one a service answers",
		func(status string) {
			r := refusal(answering(http.StatusBadRequest, map[string]string{contract.HeaderUpstreamStatus: status, contract.HeaderUpstreamHint: "x"}))
			Expect(r.Upstream).To(BeZero())
			Expect(r.Hint).To(BeEmpty(), "a hint with no status is not kept")
		},
		Entry("success", "200"), Entry("below 400", "399"), Entry("above 599", "600"), Entry("not a number", "forbidden"), Entry("empty", ""),
	)
	It("keeps a refusal with no header as it was", func() {
		r := refusal(answering(http.StatusUnprocessableEntity, nil))
		Expect(r.Upstream).To(BeZero())
		Expect(r.Status).To(Equal(http.StatusUnprocessableEntity))
	})
	It("bounds the hint to printable ASCII", func() {
		r := refusal(answering(http.StatusBadRequest, map[string]string{
			contract.HeaderUpstreamStatus: "404",
			contract.HeaderUpstreamHint:   strings.Repeat("ab  c ", 200),
		}))
		Expect(len(r.Hint)).To(BeNumerically("<=", contract.MaxUpstreamHint))
		Expect(r.Hint).NotTo(ContainSubstring("  "))
	})
})

var _ = Describe("Denials of the add-on", func() {
	denying := func(body string) *Client {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, body, http.StatusForbidden)
		}))
		DeferCleanup(srv.Close)
		c, e := newClient(Registration{AddonID: "sample", Generation: 1, Endpoint: srv.URL}, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(e).NotTo(HaveOccurred())
		c.accept = true
		return c
	}
	It("types the add-on's own 'forbidden' answer as a denial of the person, not as an outage", func() {
		c := denying("forbidden")
		for name, call := range map[string]func() error{
			"read":    func() error { _, e := c.Read(context.Background(), envelope("read")); return e },
			"preview": func() error { _, e := c.Preview(context.Background(), envelope("preview")); return e },
			"apply": func() error {
				_, e := c.Apply(context.Background(), envelope("apply"), contract.Revision{Schema: 1})
				return e
			},
		} {
			e := call()
			Expect(errors.Is(e, ErrDenied)).To(BeTrue(), name)
			Expect(errors.Is(e, contract.ErrDenied)).To(BeTrue(), name)
			Expect(errors.Is(e, ErrRuntime)).To(BeFalse(), name)
		}
	})
	It("keeps a 403 about the envelope itself an outage: it is a host fault, not a permission", func() {
		_, e := denying("invalid authority").Read(context.Background(), envelope("read"))
		Expect(e).To(MatchError(ErrRuntime))
		Expect(errors.Is(e, ErrDenied)).To(BeFalse())
	})
})

var _ = Describe("Rejected credentials", func() {
	answering := func(status int, body string) *Client {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, body, status)
		}))
		DeferCleanup(srv.Close)
		c, e := newClient(Registration{AddonID: "sample", Generation: 1, Endpoint: srv.URL}, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(e).NotTo(HaveOccurred())
		c.accept = true
		return c
	}
	It("types a 401 credential_rejected answer, never as an outage", func() {
		c := answering(http.StatusUnauthorized, "credential_rejected")
		for name, call := range map[string]func() error{
			"read":    func() error { _, e := c.Read(context.Background(), envelope("read")); return e },
			"preview": func() error { _, e := c.Preview(context.Background(), envelope("preview")); return e },
			"apply": func() error {
				_, e := c.Apply(context.Background(), envelope("apply"), contract.Revision{Schema: 1})
				return e
			},
			"output": func() error { _, e := c.OpenOutput(context.Background(), envelope("output"), "h"); return e },
		} {
			e := call()
			Expect(errors.Is(e, ErrCredentialRejected)).To(BeTrue(), name)
			Expect(errors.Is(e, contract.ErrCredentialRejected)).To(BeTrue(), name)
			Expect(errors.Is(e, ErrRuntime)).To(BeFalse(), name)
			Expect(errors.Is(e, ErrDenied)).To(BeFalse(), name)
		}
	})
	It("keeps a 401 with another reason, or none, an outage", func() {
		for _, body := range []string{"unauthorized", "invalid bearer", "", "credential_rejected_by_proxy"} {
			_, e := answering(http.StatusUnauthorized, body).Read(context.Background(), envelope("read"))
			Expect(e).To(MatchError(ErrRuntime), body)
			Expect(errors.Is(e, ErrCredentialRejected)).To(BeFalse(), body)
		}
	})
	It("does not read the reason on another status as a rejection", func() {
		for _, status := range []int{http.StatusForbidden, http.StatusBadRequest, http.StatusInternalServerError} {
			_, e := answering(status, "credential_rejected").Read(context.Background(), envelope("read"))
			Expect(errors.Is(e, ErrCredentialRejected)).To(BeFalse(), http.StatusText(status))
		}
	})
})

var _ = Describe("Rate limits of the service behind the add-on", func() {
	limited := func(status int, body, wait string) *Client {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if wait != "" {
				w.Header().Set("Retry-After", wait)
			}
			http.Error(w, body, status)
		}))
		DeferCleanup(srv.Close)
		c, e := newClient(Registration{AddonID: "sample", Generation: 1, Endpoint: srv.URL}, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(e).NotTo(HaveOccurred())
		c.accept = true
		return c
	}
	It("types the add-on's own 429 answer with its wait, never as an outage", func() {
		_, e := limited(http.StatusTooManyRequests, "rate_limited", "75").Read(context.Background(), envelope("read"))
		var rl *contract.ErrRateLimited
		Expect(errors.As(e, &rl)).To(BeTrue())
		Expect(rl.RetryAfter).To(Equal(75))
		Expect(errors.Is(e, ErrRuntime)).To(BeFalse())
	})
	It("keeps the wait between one second and the bound", func() {
		for wait, want := range map[string]int{"": 1, "0": 1, "abc": 1, "999999": contract.MaxRetryAfter} {
			_, e := limited(http.StatusTooManyRequests, "rate_limited", wait).Read(context.Background(), envelope("read"))
			var rl *contract.ErrRateLimited
			Expect(errors.As(e, &rl)).To(BeTrue(), wait)
			Expect(rl.RetryAfter).To(Equal(want), wait)
		}
	})
	It("leaves a 429 with another body an outage: only the add-on's own word counts", func() {
		_, e := limited(http.StatusTooManyRequests, "slow down", "5").Read(context.Background(), envelope("read"))
		Expect(e).To(MatchError(ErrRuntime))
	})
})

var _ = Describe("Transport limits", func() {
	It("rejects nonloopback endpoints and bounds oversized streams and concurrency", func() {
		s := supervisor()
		s.config.Limits.ArtifactBytes = 16
		i, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).NotTo(HaveOccurred())
		reg := i.process.registration
		reg.Endpoint = "http://192.0.2.1:80"
		_, e = newClient(reg, i.process.client.credential, s.config.Limits)
		Expect(e).To(HaveOccurred())
		stream, e := i.Client.OpenOutput(context.Background(), envelope("output"), "oversized")
		if e == nil {
			b, readErr := io.ReadAll(stream)
			Expect(readErr).To(HaveOccurred())
			Expect(len(b)).To(BeNumerically("<=", 16))
			_ = stream.Close()
		}
		streams := []io.ReadCloser{}
		for n := int64(0); n < s.config.Limits.Concurrency; n++ {
			stream, e = i.Client.OpenOutput(context.Background(), envelope("output"), "slow")
			Expect(e).NotTo(HaveOccurred())
			streams = append(streams, stream)
		}
		_, e = i.Client.Read(context.Background(), envelope("read"))
		Expect(e).To(HaveOccurred())
		for _, stream := range streams {
			Expect(stream.Close()).To(Succeed())
		}
		expired := envelope("read")
		expired.Authority.ExpiresAt = time.Now().Add(-time.Second)
		_, e = i.Client.Read(context.Background(), expired)
		Expect(e).To(HaveOccurred())
	})
})

var _ = Describe("Requests that never reach the wire", func() {
	// Model rules R34 (not sent, retryable) and R39 (only a send that may have
	// happened is unknown).
	client := func(url string, tune ...func(*contract.Limits)) *Client {
		l := contract.DefaultLimits()
		for _, t := range tune {
			t(&l)
		}
		c, e := newClient(Registration{AddonID: "sample", Generation: 1, Endpoint: url}, strings.Repeat("a", 64), l)
		Expect(e).NotTo(HaveOccurred())
		c.accept = true
		return c
	}
	serving := func(h http.HandlerFunc) string {
		srv := httptest.NewServer(h)
		DeferCleanup(srv.Close)
		return srv.URL
	}
	apply := func(c *Client) error {
		_, e := c.Apply(context.Background(), envelope("apply"), contract.Revision{Schema: 1})
		return e
	}
	ok := func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unreachable", http.StatusTeapot) }

	It("types a withdrawn client, a full slot table, a bad envelope and a missing schema as not sent", func() {
		c := client(serving(ok))
		c.withdraw()
		Expect(apply(c)).To(MatchError(ErrNotSent))

		c = client(serving(ok), func(l *contract.Limits) { l.Concurrency = 1 })
		done, e := c.admit()
		Expect(e).NotTo(HaveOccurred())
		defer done()
		Expect(apply(c)).To(MatchError(ErrNotSent))

		c = client(serving(ok))
		_, e = c.Apply(context.Background(), envelope("read"), contract.Revision{Schema: 1})
		Expect(e).To(MatchError(ErrNotSent))
		_, e = c.Apply(context.Background(), envelope("apply"), contract.Revision{})
		Expect(e).To(MatchError(ErrNotSent))
	})
	It("keeps a not-sent error an ErrRuntime for callers that do not tell them apart", func() {
		c := client(serving(ok))
		c.withdraw()
		Expect(errors.Is(apply(c), ErrRuntime)).To(BeTrue())
	})
	It("types a refused connection as not sent", func() {
		srv := httptest.NewServer(http.HandlerFunc(ok))
		url := srv.URL
		srv.Close()
		Expect(apply(client(url))).To(MatchError(ErrNotSent))
	})
	It("never types a possibly-sent request as not sent", func() {
		// The server reads the request and drops the connection without an answer.
		lost := client(serving(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			conn, _, e := w.(http.Hijacker).Hijack()
			Expect(e).NotTo(HaveOccurred())
			_ = conn.Close()
		}))
		e := apply(lost)
		Expect(e).To(MatchError(ErrRuntime))
		Expect(errors.Is(e, ErrNotSent)).To(BeFalse())
		// An answer is never not-sent, whatever its status.
		for _, status := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError} {
			answering := client(serving(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", status) }))
			e = apply(answering)
			Expect(e).To(MatchError(ErrRuntime))
			Expect(errors.Is(e, ErrNotSent)).To(BeFalse())
		}
		conflicting := client(serving(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", http.StatusConflict) }))
		e = apply(conflicting)
		Expect(errors.Is(e, ErrConflict)).To(BeTrue())
		Expect(errors.Is(e, ErrNotSent)).To(BeFalse())
	})
})

var _ = Describe("Action calls", func() {
	actEnvelope := func() contract.Envelope {
		e := envelope("act")
		e.Request.Action = "act"
		e.Request.Arguments = json.RawMessage(`{"tool":"run","command":"echo hi"}`)
		return e
	}
	withActions := contract.Capabilities{Service: true, Tools: []string{"run"}, Actions: true,
		ToolDefinitions: []contract.ToolDefinition{{Name: "run", Description: "Run a command.", Kind: contract.ToolKindAction,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"additionalProperties":false}`)}}}

	It("reaches an add-on that declares actions, as the actor of the envelope, under the act scope", func() {
		s := supervisor()
		i, e := s.Start(context.Background(), fixture("normal", withActions), 1)
		Expect(e).NotTo(HaveOccurred())
		r, e := i.Client.(contract.ActRuntime).Act(context.Background(), actEnvelope())
		Expect(e).NotTo(HaveOccurred())
		Expect(string(r.Payload)).To(And(ContainSubstring(`"actor":"alice"`), ContainSubstring(`"scope":"act"`), ContainSubstring("echo hi")))
	})
	It("never sends a call that is not an act call or has another scope", func() {
		s := supervisor()
		// This spec tests admission, not the speed of starting a child.
		s.config.ReadyTimeout = 2 * time.Second
		i, e := s.Start(context.Background(), fixture("normal", withActions), 1)
		Expect(e).NotTo(HaveOccurred())
		bad := actEnvelope()
		bad.Authority.Scope = "read"
		_, e = i.Client.(contract.ActRuntime).Act(context.Background(), bad)
		Expect(e).To(MatchError(ErrNotSent))
		bad = actEnvelope()
		bad.Request.Action = "read"
		_, e = i.Client.(contract.ActRuntime).Act(context.Background(), bad)
		Expect(e).To(MatchError(ErrNotSent))
	})
	It("never sends an act call to an add-on that did not declare actions", func() {
		s := supervisor()
		i, e := s.Start(context.Background(), fixture("normal", contract.Capabilities{Service: true}), 1)
		Expect(e).NotTo(HaveOccurred())
		_, e = i.Client.(contract.ActRuntime).Act(context.Background(), actEnvelope())
		Expect(e).To(MatchError(ErrNotSent))
	})
	It("stops an act call when the caller gives up", func() {
		s := supervisor()
		i, e := s.Start(context.Background(), fixture("normal", withActions), 1)
		Expect(e).NotTo(HaveOccurred())
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		env := actEnvelope()
		env.Request.Arguments = json.RawMessage(`{"tool":"run","slow":true}`)
		start := time.Now()
		_, e = i.Client.(contract.ActRuntime).Act(ctx, env)
		Expect(e).To(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
	})
})

var _ = Describe("The chat binding of a call", func() {
	// recording is an add-on that keeps the body of each call and what the
	// host recorded for it while it ran.
	type seen struct {
		body    []byte
		binding hostops.Binding
		live    bool
	}
	actEnvelope := func() contract.Envelope {
		e := envelope("act")
		e.Request.Action = "act"
		e.Request.Arguments = json.RawMessage(`{"tool":"run"}`)
		return e
	}
	recording := func() (*Client, *[]seen) {
		var calls []seen
		var c *Client
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			var env contract.Envelope
			if r.URL.Path == "/read" || r.URL.Path == "/act" {
				_ = json.Unmarshal(b, &env)
			}
			_, binding, live := c.inflight.LookupBound(env.Request.AddonID, env.Request.OperationID)
			calls = append(calls, seen{body: b, binding: binding, live: live})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"payload":{"ok":true}}`))
		}))
		DeferCleanup(srv.Close)
		var e error
		c, e = newClient(Registration{AddonID: "sample", Generation: 1, Endpoint: srv.URL, Capabilities: contract.Capabilities{Service: true, Actions: true}}, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(e).NotTo(HaveOccurred())
		c.accept = true
		c.inflight = hostops.NewInFlight()
		return c, &calls
	}

	It("records the chat with the envelope of a read and of an action while it runs", func() {
		c, calls := recording()
		ctx := hostops.WithBinding(context.Background(), hostops.Binding{Principal: "alice", ChatID: "c1"})
		_, e := c.Read(ctx, envelope("read"))
		Expect(e).NotTo(HaveOccurred())
		_, e = c.Act(ctx, actEnvelope())
		Expect(e).NotTo(HaveOccurred())
		Expect(*calls).To(HaveLen(2))
		for _, s := range *calls {
			Expect(s.live).To(BeTrue())
			Expect(s.binding).To(Equal(hostops.Binding{Principal: "alice", ChatID: "c1"}))
		}
		_, e = c.Read(context.Background(), envelope("read"))
		Expect(e).NotTo(HaveOccurred())
		Expect((*calls)[2].binding).To(Equal(hostops.Binding{}), "a call that no chat made has no binding")
		_, _, live := c.inflight.LookupBound("sample", "operation")
		Expect(live).To(BeFalse(), "the record ends with the call")
	})

	It("sends the add-on an envelope without a new field", func() {
		c, calls := recording()
		env := envelope("read")
		_, e := c.Read(context.Background(), env)
		Expect(e).NotTo(HaveOccurred())
		_, e = c.Read(hostops.WithBinding(context.Background(), hostops.Binding{Principal: "alice", ChatID: "c1"}), env)
		Expect(e).NotTo(HaveOccurred())
		Expect(*calls).To(HaveLen(2))
		unbound, bound := (*calls)[0].body, (*calls)[1].body
		Expect(bound).To(Equal(unbound), "the binding stays on the host")
		Expect(string(bound)).NotTo(ContainSubstring("c1"))
		Expect(string(bound)).NotTo(ContainSubstring("chatId"))
		// An installed add-on decodes the envelope with unknown fields
		// refused: what it receives still decodes.
		var strict contract.Envelope
		Expect(strict.UnmarshalJSON(bound)).To(Succeed())
		Expect(strict.Request.OperationID).To(Equal(env.Request.OperationID))
	})
})
