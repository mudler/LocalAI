// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// rejectingClient rejects the credentials in reject, the way the runtime
// client reports the add-on's 401 credential_rejected, and records what it
// was given.
type rejectingClient struct {
	contract.Runtime
	mu     sync.Mutex
	reject map[string]bool
	seen   []string
}

func (c *rejectingClient) Read(_ context.Context, e contract.Envelope) (contract.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, e.Request.Credential)
	if c.reject[e.Request.Credential] {
		return contract.Result{}, addonruntime.ErrCredentialRejected
	}
	return contract.Result{Payload: json.RawMessage(`{"ok":true}`)}, nil
}

// renewingCredentials is an OAuth source: version n holds the token
// "token-n", and a renewal makes the next version.
type renewingCredentials struct {
	mu      sync.Mutex
	version int64
	renews  []string
	ended   bool
}

func (r *renewingCredentials) grant() CredentialGrant {
	return CredentialGrant{Credential: fmt.Sprintf("token-%d", r.version), Version: r.version, BindingID: "b1", BindingName: "Work"}
}

func (r *renewingCredentials) Credential(context.Context, contract.Actor, string, string) (CredentialGrant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return CredentialGrant{}, &CredentialRefusal{Code: CodeReconnectNeeded}
	}
	return r.grant(), nil
}

func (r *renewingCredentials) RenewCredential(_ context.Context, _ contract.Actor, _ string, rejected CredentialGrant, final bool) (CredentialGrant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renews = append(r.renews, fmt.Sprintf("%s|%d|%v", rejected.BindingID, rejected.Version, final))
	if final {
		r.ended = true
		return CredentialGrant{}, &CredentialRefusal{Code: CodeReconnectNeeded}
	}
	if rejected.Version == r.version {
		r.version++
	}
	return r.grant(), nil
}

var _ = Describe("a credential the add-on rejects", Label("tools"), func() {
	ctx := context.Background()
	defs := []contract.ToolDefinition{
		{Name: "whoami", Kind: "read", Description: "x", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	}
	source := credCaps{capSet{toolSet: toolSet{gen: 1, tools: map[string][]string{"providerA": {"whoami"}}}, defs: defs}}
	actor := contract.Actor{UserID: "alice", CredentialID: "c", Mode: "named-user"}
	req := contract.Request{AddonID: "providerA", Generation: 1, OperationID: "op", Action: "read", Arguments: json.RawMessage(`{"tool":"whoami"}`)}
	var (
		client *rejectingClient
		creds  *renewingCredentials
		owner  *Gateway
	)
	BeforeEach(func() {
		client = &rejectingClient{reject: map[string]bool{}}
		creds = &renewingCredentials{version: 1}
		owner = &Gateway{Admission: &addonAdmitted{}, Resolver: strictResolver{}, Runtime: currentFixture{client}, Tools: source, Credentials: creds,
			Revalidate: func(context.Context, contract.Actor) error { return nil }}
	})

	It("is renewed once and the call is retried with the new token", func() {
		client.reject["token-1"] = true
		_, err := owner.Dispatch(ctx, actor, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(client.seen).To(Equal([]string{"token-1", "token-2"}))
		Expect(creds.renews).To(Equal([]string{"b1|1|false"}))
	})

	It("ends the login after a second rejection and refuses with reconnect_needed", func() {
		client.reject["token-1"], client.reject["token-2"] = true, true
		_, err := owner.Dispatch(ctx, actor, req)
		var refusal *CredentialRefusal
		Expect(errors.As(err, &refusal)).To(BeTrue(), "%v", err)
		Expect(refusal.Code).To(Equal(CodeReconnectNeeded))
		Expect(client.seen).To(Equal([]string{"token-1", "token-2"}), "one retry, no more")
		Expect(creds.renews).To(Equal([]string{"b1|1|false", "b1|2|true"}))
	})

	It("stays a plain outage for a source that cannot renew", func() {
		client.reject["secret"] = true
		owner.Credentials = &fakeCredentials{}
		_, err := owner.Dispatch(ctx, actor, req)
		Expect(StatusFor(err)).To(Equal(http.StatusServiceUnavailable))
		Expect(client.seen).To(HaveLen(1))
	})

	It("is renewed on the owner, so a forwarding frontend gets the result or the reconnect_needed refusal", func() {
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(owner)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		client.reject["token-1"] = true
		res, err := cs["front-a"].Dispatch(ctx, actor, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(res.Payload)).To(ContainSubstring(`"ok"`))
		Expect(client.seen).To(Equal([]string{"token-1", "token-2"}))

		client.reject["token-2"], client.reject["token-3"] = true, true
		_, err = cs["front-b"].Dispatch(ctx, actor, req)
		var refusal *CredentialRefusal
		Expect(errors.As(err, &refusal)).To(BeTrue(), "%v", err)
		Expect(refusal.Code).To(Equal(CodeReconnectNeeded))
		_, err = cs["front-a"].Dispatch(ctx, actor, req)
		Expect(errors.As(err, &refusal)).To(BeTrue(), "an ended login is refused before the add-on is called")
		Expect(client.seen).To(HaveLen(4))
	})

	It("crosses the carrier as rate limited when the token endpoint asks to wait", func() {
		owner.Credentials = &fakeCredentials{err: &contract.ErrRateLimited{RetryAfter: 7}}
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(owner)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		for _, g := range []interface {
			Dispatch(context.Context, contract.Actor, contract.Request) (contract.Result, error)
		}{owner, cs["front-a"]} {
			_, err := g.Dispatch(ctx, actor, req)
			var rl *contract.ErrRateLimited
			Expect(errors.As(err, &rl)).To(BeTrue(), "%v", err)
			Expect(rl.RetryAfter).To(Equal(7))
		}
		Expect(client.seen).To(BeEmpty())
	})
})
