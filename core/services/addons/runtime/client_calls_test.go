//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("client route calls and the short call pool", func() {
	It("leaves slots for tool calls while slow client calls hold their own", func() {
		release := make(chan struct{})
		started := make(chan struct{}, 16)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/client" {
				started <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				_ = json.NewEncoder(w).Encode(contract.ClientResponse{Status: 204})
				return
			}
			_ = json.NewEncoder(w).Encode(contract.ConnectorResponse{})
		}))
		DeferCleanup(srv.Close)
		reg := Registration{AddonID: "clientdemo", Generation: 1, Incarnation: "inc", Protocol: 1, Endpoint: srv.URL,
			Capabilities: contract.Capabilities{
				Client:    &contract.ClientCapability{Routes: []contract.ClientRoute{{ID: "slow", Method: "GET", Path: "/slow"}}},
				Connector: &contract.ConnectorCapability{Kinds: []contract.ConnectorKind{{ID: "telegram", Title: "Telegram", Delivery: contract.DeliveryPull}}},
			}}
		c, err := newClient(reg, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
		c.accept = true

		actor := contract.Actor{UserID: "u", CredentialID: contract.ClientCredentialPrefix + "c1"}
		req := contract.ClientRequest{RouteID: "slow", Method: "GET", Path: "/slow", Client: contract.ClientIdentity{ID: "c1"}}
		results := make(chan error, 16)
		for range 6 {
			go func() {
				_, err := c.ClientCallAs(context.Background(), actor, req)
				results <- err
			}()
		}
		limit := c.clientCallLimit()
		Expect(limit).To(BeNumerically("<", int(c.limits.Concurrency)))
		for range limit {
			Eventually(started).Should(Receive())
		}
		// The calls past the client budget are refused at once.
		for range 6 - limit {
			Eventually(results).Should(Receive(MatchError(ErrNotSent)))
		}
		// A tool call of the add-on still has its slots.
		for range int(c.limits.Concurrency) {
			_, err := c.Connector(context.Background(), contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "telegram", Token: "t"})
			Expect(err).To(Succeed())
		}
		close(release)
		for range limit {
			Eventually(results).Should(Receive(BeNil()))
		}
		// The slots come back.
		_, err = c.ClientCallAs(context.Background(), actor, req)
		Expect(err).NotTo(HaveOccurred())
	})

	It("takes the client calls from the declared concurrency instead of adding to it", func() {
		reg := Registration{AddonID: "clientdemo", Generation: 1, Incarnation: "inc", Protocol: 1, Endpoint: "http://127.0.0.1:1",
			Capabilities: contract.Capabilities{Client: &contract.ClientCapability{Routes: []contract.ClientRoute{{ID: "r", Method: "GET", Path: "/r"}}}}}
		c, err := newClient(reg, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
		c.accept = true
		total := int(c.limits.Concurrency)

		var releases []func()
		for range total {
			release, err := c.admitIn(poolClient)
			if err != nil {
				break
			}
			releases = append(releases, release)
		}
		Expect(releases).To(HaveLen(c.clientCallLimit()), "client calls stop at half")
		// The rest of the budget is left to the other calls, and no more.
		for range total - len(releases) {
			release, err := c.admit()
			Expect(err).To(Succeed())
			releases = append(releases, release)
		}
		_, err = c.admit()
		Expect(err).To(MatchError(ErrNotSent), "the declared concurrency is the sum of both")
		for _, r := range releases {
			r()
		}
		Expect(c.active).To(BeZero())
		Expect(c.clientCalls).To(BeZero())

		// When the short calls fill the budget, a client call has no slot either.
		for range total {
			release, err := c.admit()
			Expect(err).To(Succeed())
			DeferCleanup(release)
		}
		_, err = c.admitIn(poolClient)
		Expect(err).To(MatchError(ErrNotSent))
	})
})

var _ = Describe("the authority of a client call", func() {
	// scopeOf makes one call on a route and returns the scope of the record
	// the host holds while the add-on serves it.
	scopeOf := func(method string) string {
		var c *Client
		scope := make(chan string, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req contract.ClientRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			env, ok := c.inflight.Lookup("clientdemo", req.OperationID)
			if !ok {
				scope <- "none"
			} else {
				scope <- env.Authority.Scope
			}
			_ = json.NewEncoder(w).Encode(contract.ClientResponse{Status: 204})
		}))
		DeferCleanup(srv.Close)
		reg := Registration{AddonID: "clientdemo", Generation: 1, Incarnation: "inc", Protocol: 1, Endpoint: srv.URL,
			Capabilities: contract.Capabilities{Client: &contract.ClientCapability{Routes: []contract.ClientRoute{{ID: "r", Method: method, Path: "/r"}}}}}
		var err error
		c, err = newClient(reg, strings.Repeat("a", 64), contract.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
		c.accept = true
		actor := contract.Actor{UserID: "u", CredentialID: contract.ClientCredentialPrefix + "c1"}
		_, err = c.ClientCallAs(context.Background(), actor, contract.ClientRequest{RouteID: "r", Method: method, Path: "/r", Client: contract.ClientIdentity{ID: "c1"}})
		Expect(err).NotTo(HaveOccurred())
		return <-scope
	}

	It("is read for a GET route and apply for a route that changes things, and never act", func() {
		Expect(scopeOf(http.MethodGet)).To(Equal("read"))
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			Expect(scopeOf(m)).To(Equal("apply"), m)
		}
	})

	It("builds the envelope of a stream with the read scope and refuses a caller that is not a client", func() {
		c := &Client{registration: Registration{AddonID: "clientdemo"}}
		actor := contract.Actor{UserID: "u", CredentialID: contract.ClientCredentialPrefix + "c1"}
		env, err := c.clientEnvelope(actor, "op", 0, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(env.Authority.Scope).To(Equal("read"))
		env, err = c.clientEnvelope(actor, "op", 0, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(env.Authority.Scope).To(Equal("apply"))
		_, err = c.clientEnvelope(contract.Actor{UserID: "u", CredentialID: "session"}, "op", 0, true)
		Expect(err).To(MatchError(ErrNotSent))
	})
})
