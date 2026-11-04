// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Host-only observation read", func() {
	makeClient := func(handler http.HandlerFunc) (*Client, *hostops.Broker) {
		srv := httptest.NewServer(handler)
		DeferCleanup(srv.Close)
		c, err := newClient(Registration{AddonID: "sample", Generation: 1, Incarnation: "inc", Endpoint: srv.URL}, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
		c.accept = true
		c.inflight = hostops.NewInFlight()
		b := hostops.New(c.inflight, func(string, string) bool { return true }, contract.DefaultLimits())
		return c, b
	}
	env := func() contract.Envelope {
		return contract.Envelope{Request: contract.Request{AddonID: "sample", Generation: 1, OperationID: "status", Action: "read", Arguments: json.RawMessage(`{"tool":"status"}`)}, Authority: contract.Authority{Actor: contract.Actor{UserID: "alice", CredentialID: "key", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Now().Add(time.Minute)}}
	}
	It("rejects output handles before they can reach storage or output-open", func() {
		var opens atomic.Int32
		c, _ := makeClient(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/output" {
				opens.Add(1)
			}
			_, err := io.WriteString(w, `{"payload":{"readiness":"available","isolation":"isolated"},"outputs":[{"handle":"evil","name":"evil.txt","mediaType":"text/plain","size":1}]}`)
			Expect(err).NotTo(HaveOccurred())
		})
		value, err := c.ReadObservation(context.Background(), env(), "q")
		Expect(err).To(HaveOccurred())
		Expect(value.Readiness).NotTo(Equal("available"))
		Expect(opens.Load()).To(BeZero())
		ordinary := env()
		ordinary.Request.OperationID = "ordinary"
		r, err := c.Read(context.Background(), ordinary)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Outputs).To(HaveLen(1))
	})
	It("settles callback denial as failure even if the addon returns a valid positive payload", func() {
		var callback http.Handler
		var code atomic.Int32
		c, b := makeClient(func(w http.ResponseWriter, r *http.Request) {
			req := httptest.NewRequest("POST", "/host", strings.NewReader(`{"operationId":"status","op":"connector.inbound","arguments":{}}`))
			req.Header.Set(contract.HostCredentialHeader, "secret")
			rec := httptest.NewRecorder()
			callback.ServeHTTP(rec, req)
			code.Store(int32(rec.Code))
			_, err := io.WriteString(w, `{"payload":{"readiness":"available","isolation":"isolated"}}`)
			Expect(err).NotTo(HaveOccurred())
		})
		var calls atomic.Int32
		Expect(b.Register(hostops.Spec{Op: "connector.inbound", Scope: hostops.ScopeInbound, Handler: func(context.Context, hostops.Call) (json.RawMessage, error) {
			calls.Add(1)
			return json.RawMessage(`{}`), nil
		}})).To(Succeed())
		callback = b.HandlerForRuntime("sample", "secret", hostops.NewStaticAuthority(map[string][]string{"sample": []string{"connector."}}), 1, "inc")
		_, err := c.ReadObservation(context.Background(), env(), "q")
		Expect(err).To(HaveOccurred())
		Expect(code.Load()).To(Equal(int32(403)))
		Expect(calls.Load()).To(BeZero())
		_, live := c.inflight.Lookup("sample", "status")
		Expect(live).To(BeFalse())
	})
	It("cleans up after cancellation and rejects repeat correlation", func() {
		entered := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		c, _ := makeClient(func(w http.ResponseWriter, r *http.Request) { once.Do(func() { close(entered) }); <-release })
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := c.ReadObservation(ctx, env(), "q"); done <- err }()
		<-entered
		cancel()
		Expect(<-done).To(HaveOccurred())
		close(release)
		_, live := c.inflight.Lookup("sample", "status")
		Expect(live).To(BeFalse())
		_, err := c.ReadObservation(context.Background(), env(), "q2")
		Expect(err).To(HaveOccurred())
	})
	It("keeps an ordinary concurrent read authorized while an observation is pending", func() {
		entered := make(chan struct{})
		release := make(chan struct{})
		var statusCode atomic.Int32
		var callback http.Handler
		c, b := makeClient(func(w http.ResponseWriter, r *http.Request) {
			var e contract.Envelope
			_ = json.NewDecoder(r.Body).Decode(&e)
			if e.Request.OperationID == "status" {
				close(entered)
				<-release
			} else {
				req := httptest.NewRequest("POST", "/host", strings.NewReader(`{"operationId":"ordinary","op":"workspace.export","arguments":{}}`))
				req.Header.Set(contract.HostCredentialHeader, "secret")
				rec := httptest.NewRecorder()
				callback.ServeHTTP(rec, req)
				statusCode.Store(int32(rec.Code))
			}
			_, err := io.WriteString(w, `{"payload":{"readiness":"available","isolation":"isolated"}}`)
			Expect(err).NotTo(HaveOccurred())
		})
		Expect(b.Register(hostops.Spec{Op: "workspace.export", Scope: "read", Feature: "chats", Handler: func(context.Context, hostops.Call) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }})).To(Succeed())
		callback = b.HandlerForRuntime("sample", "secret", hostops.NewStaticAuthority(map[string][]string{"sample": []string{"workspace."}}), 1, "inc")
		done := make(chan error, 1)
		go func() { _, err := c.ReadObservation(context.Background(), env(), "q"); done <- err }()
		<-entered
		ordinary := env()
		ordinary.Request.OperationID = "ordinary"
		_, err := c.Read(context.Background(), ordinary)
		close(release)
		Expect(err).NotTo(HaveOccurred())
		Expect(<-done).NotTo(HaveOccurred())
		Expect(statusCode.Load()).To(Equal(int32(200)))
	})
	DescribeTable("cleans up every settled path", func(mode string) {
		c, _ := makeClient(func(w http.ResponseWriter, r *http.Request) {
			switch mode {
			case "error":
				http.Error(w, "failed", 503)
			case "timeout":
				time.Sleep(50 * time.Millisecond)
			case "malformed":
				_, err := io.WriteString(w, `{"payload":{"artifact":"evil"}}`)
				Expect(err).NotTo(HaveOccurred())
			default:
				_, err := io.WriteString(w, `{"payload":{"readiness":"available","isolation":"isolated"}}`)
				Expect(err).NotTo(HaveOccurred())
			}
		})
		ctx := context.Background()
		if mode == "timeout" {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
			defer cancel()
		}
		_, err := c.ReadObservation(ctx, env(), "q")
		if mode == "success" {
			Expect(err).NotTo(HaveOccurred())
		} else {
			Expect(err).To(HaveOccurred())
		}
		_, live := c.inflight.Lookup("sample", "status")
		Expect(live).To(BeFalse())
	}, Entry("success", "success"), Entry("error", "error"), Entry("timeout", "timeout"), Entry("malformed/artifact payload", "malformed"))

	It("cancels an admitted observation when the runtime withdraws", func() {
		entered := make(chan struct{})
		release := make(chan struct{})
		c, _ := makeClient(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release })
		done := make(chan error, 1)
		go func() { _, err := c.ReadObservation(context.Background(), env(), "q"); done <- err }()
		<-entered
		c.withdraw()
		select {
		case err := <-done:
			Expect(err).To(HaveOccurred())
		case <-time.After(100 * time.Millisecond):
			close(release)
			Fail("withdrawal did not cancel observation")
			return
		}
		close(release)
		_, live := c.inflight.Lookup("sample", "status")
		Expect(live).To(BeFalse())
	})

})
