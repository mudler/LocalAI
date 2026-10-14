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
		resp.Body.Close()
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
		stream.Close()
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
			stream.Close()
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
