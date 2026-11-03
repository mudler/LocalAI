// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("exact observation client admission", func() {
	It("refuses missing wire tool discriminators before any read", func() {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			_, err := io.WriteString(w, `{"payload":{"readiness":"available","isolation":"isolated"}}`)
			Expect(err).NotTo(HaveOccurred())
		}))
		defer srv.Close()
		c, e := newClient(Registration{AddonID: "sample", Generation: 1, Incarnation: "old", Endpoint: srv.URL}, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(e).NotTo(HaveOccurred())
		c.accept = true
		env := contract.Envelope{Request: contract.Request{AddonID: "sample", Generation: 1, Action: "read", OperationID: "op", Arguments: json.RawMessage(`{}`)}, Authority: contract.Authority{Actor: contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Now().Add(time.Second)}}
		_, e = c.ReadObservation(context.Background(), env, "q")
		Expect(e).To(HaveOccurred())
		Expect(calls.Load()).To(BeZero())
	})
	It("serializes exact identity admission with withdrawal and never dispatches the replacement", func() {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			_, err := io.WriteString(w, `{"payload":{"readiness":"available","isolation":"isolated"}}`)
			Expect(err).NotTo(HaveOccurred())
		}))
		defer srv.Close()
		old, e := newClient(Registration{AddonID: "sample", Generation: 1, Incarnation: "old", Endpoint: srv.URL}, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(e).NotTo(HaveOccurred())
		old.accept = true
		replacement, e := newClient(Registration{AddonID: "sample", Generation: 1, Incarnation: "new", Endpoint: srv.URL}, strings.Repeat("b", 64), contract.DefaultLimits())
		Expect(e).NotTo(HaveOccurred())
		replacement.accept = true
		_, e = old.AdmitObservation(2, "old")
		Expect(e).To(HaveOccurred())
		_, e = replacement.AdmitObservation(1, "old")
		Expect(e).To(HaveOccurred())
		release, e := old.AdmitObservation(1, "old")
		Expect(e).NotTo(HaveOccurred())
		old.withdraw() // deterministic capture-to-dispatch restart boundary
		env := contract.Envelope{Request: contract.Request{AddonID: "sample", Generation: 1, Action: "read", OperationID: "op", Arguments: json.RawMessage(`{"tool":"health"}`)}, Authority: contract.Authority{Actor: contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Now().Add(time.Second)}}
		_, e = old.ReadObservation(context.Background(), env, "q")
		Expect(e).To(HaveOccurred())
		release()
		release()
		Expect(old.active).To(BeZero())
		Expect(calls.Load()).To(BeZero())
		_, e = old.AdmitObservation(1, "old")
		Expect(e).To(HaveOccurred())
	})
})
