// SPDX-License-Identifier: MIT
package hostclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func startupFor(endpoint string) protocol.Startup {
	return protocol.Startup{HostEndpoint: endpoint, HostCredential: "secret", Limits: contract.DefaultLimits()}
}

var _ = Describe("host client", func() {
	It("sends the credential and the body", func() {
		var gotCred string
		var gotReq contract.HostRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotCred = r.Header.Get("X-Addon-Host-Credential")
			_ = json.NewDecoder(r.Body).Decode(&gotReq)
			_ = json.NewEncoder(w).Encode(contract.HostResponse{Result: json.RawMessage(`{"id":"j1"}`)})
		}))
		DeferCleanup(srv.Close)
		c := New(startupFor(srv.URL))
		Expect(c).NotTo(BeNil(), "no client for a loopback endpoint")
		var out struct{ ID string }
		Expect(c.Do(context.Background(), "op1", "finetune.get", map[string]string{"jobId": "j1"}, &out)).To(Succeed())
		Expect(gotCred).To(Equal("secret"))
		Expect(gotReq.OperationID).To(Equal("op1"))
		Expect(gotReq.Op).To(Equal("finetune.get"))
		Expect(string(gotReq.Arguments)).To(Equal(`{"jobId":"j1"}`))
		Expect(out.ID).To(Equal("j1"))
	})

	It("turns an error answer into a host error", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(contract.HostResponse{Error: &contract.HostError{Code: "scope", Message: "no"}})
		}))
		DeferCleanup(srv.Close)
		err := New(startupFor(srv.URL)).Do(context.Background(), "op1", "finetune.start", nil, nil)
		var he *contract.HostError
		Expect(errors.As(err, &he)).To(BeTrue(), "got %v", err)
		Expect(he.Code).To(Equal("scope"))
	})

	It("gives a nil client for a startup without an endpoint", func() {
		c := New(protocol.Startup{})
		Expect(c).To(BeNil())
		Expect(c.Do(context.Background(), "op1", "finetune.get", nil, nil)).To(MatchError(ErrNoHostOps))
	})

	DescribeTable("gives a nil client for a non-loopback endpoint",
		func(endpoint string) {
			Expect(New(startupFor(endpoint))).To(BeNil())
		},
		Entry(nil, "http://10.0.0.1:80"),
		Entry(nil, "http://example.com:80"),
		Entry(nil, "https://127.0.0.1:80"),
		Entry(nil, "http://127.0.0.1"),
		Entry(nil, "http://127.0.0.1:80/x"),
		Entry(nil, "http://localhost:80"),
		Entry(nil, "http://127.0.0.1:0"),
		Entry(nil, "http://user@127.0.0.1:80"),
		Entry(nil, "http://[::1]:80"),
	)
})
