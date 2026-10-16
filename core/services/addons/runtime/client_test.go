//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"errors"
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
		start := time.Now()
		_, e = i.Client.Read(context.Background(), slow)
		Expect(e).To(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))
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
		for _, status := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError, http.StatusNotFound, http.StatusForbidden} {
			c := refusing(status, "no")
			_, e := c.Read(context.Background(), envelope("read"))
			Expect(errors.Is(e, ErrRefused)).To(BeFalse(), http.StatusText(status))
			Expect(e).To(MatchError(ErrRuntime))
		}
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
