// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// credCaps declares one credential slot on top of the tool definitions.
type credCaps struct{ capSet }

func (c credCaps) DeclaredCapabilities(ctx context.Context, id string, gen uint64) (contract.Capabilities, error) {
	caps, err := c.capSet.DeclaredCapabilities(ctx, id, gen)
	caps.Credentials = []contract.CredentialSlot{{ID: "token", Title: "Token", Kind: contract.CredentialKindToken, ResourceArgument: "repo"}}
	return caps, err
}

// envClient records the request of each read it gets.
type envClient struct {
	countingClient
	mu   sync.Mutex
	seen []contract.Request
}

func (c *envClient) Read(_ context.Context, e contract.Envelope) (contract.Result, error) {
	c.reads.Add(1)
	c.mu.Lock()
	c.seen = append(c.seen, e.Request)
	c.mu.Unlock()
	return contract.Result{Payload: json.RawMessage(`{"ok":true}`)}, nil
}

type fakeCredentials struct {
	allowed map[string]string // key -> id
	calls   []string
	err     error
}

func (f *fakeCredentials) Credential(_ context.Context, a contract.Actor, addonID, key string) (CredentialGrant, error) {
	f.calls = append(f.calls, a.UserID+"|"+addonID+"|"+key)
	if f.err != nil {
		return CredentialGrant{}, f.err
	}
	if key == "" {
		return CredentialGrant{Credential: "secret"}, nil
	}
	id, ok := f.allowed[key]
	if !ok {
		return CredentialGrant{}, &CredentialRefusal{Code: CodeResourceNotAllowed, Allowed: []string{"acme/widgets"}}
	}
	return CredentialGrant{Credential: "secret", ResourceKey: key, ResourceID: id}, nil
}

var _ = Describe("a read of an add-on that declares credentials", Label("tools"), func() {
	ctx := context.Background()
	defs := []contract.ToolDefinition{
		{Name: "read-file", Kind: "read", Description: "x", InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string"}},"required":["repo"],"additionalProperties":false}`)},
		{Name: "whoami", Kind: "read", Description: "x", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "list-repos", Kind: "read", Enumerates: true, Description: "x", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "page-settings", Kind: "read", UIOnly: true, Description: "x", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	}
	source := credCaps{capSet{toolSet: toolSet{gen: 1, tools: map[string][]string{"sample": {"read-file", "whoami", "list-repos", "page-settings"}}}, defs: defs}}
	var (
		g      *Gateway
		client *envClient
		creds  *fakeCredentials
	)
	BeforeEach(func() {
		client = &envClient{}
		creds = &fakeCredentials{allowed: map[string]string{"acme/widgets": "r-0123456789"}}
		g = &Gateway{Admission: &addonAdmitted{}, Resolver: strictResolver{}, Runtime: currentFixture{client}, Tools: source, Credentials: creds,
			Revalidate: func(context.Context, contract.Actor) error { return nil }}
	})
	read := func(g *Gateway, req contract.Request) (contract.Result, error) {
		req.AddonID, req.Generation, req.OperationID, req.Action = "sample", 1, "op", "read"
		return g.Dispatch(ctx, contract.Actor{UserID: "alice", CredentialID: "c", Mode: "named-user"}, req)
	}

	It("gives the add-on the credential and the host ID of an allowed resource", func() {
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"read-file","repo":"acme/widgets"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(client.seen).To(HaveLen(1))
		Expect(client.seen[0].Credential).To(Equal("secret"))
		Expect(client.seen[0].ResourceKey).To(Equal("acme/widgets"))
		Expect(client.seen[0].ResourceID).To(Equal("r-0123456789"))
		Expect(creds.calls).To(Equal([]string{"alice|sample|acme/widgets"}))
	})

	It("refuses a resource that is not on the list before the add-on is called", func() {
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"read-file","repo":"evil/repo"}`)})
		var refusal *CredentialRefusal
		Expect(errors.As(err, &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(CodeResourceNotAllowed))
		Expect(refusal.Allowed).To(Equal([]string{"acme/widgets"}))
		Expect(err).To(MatchError(ErrForbidden))
		Expect(client.reads.Load()).To(Equal(int32(0)))
	})

	It("refuses a call without the resource argument", func() {
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"read-file"}`)})
		Expect(err).To(HaveOccurred())
		Expect(client.reads.Load()).To(Equal(int32(0)))
	})

	It("gives a tool without a resource argument the credential and no resource", func() {
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"whoami"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(client.seen[0].Credential).To(Equal("secret"))
		Expect(client.seen[0].ResourceKey).To(BeEmpty())
	})

	It("asks the source for the whole reach (*) for a tool that enumerates, and refuses it when the list is not *", func() {
		creds.allowed["*"] = "r-aaaaaaaaaa"
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"list-repos"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(creds.calls).To(Equal([]string{"alice|sample|*"}))
		Expect(client.seen[0].ResourceKey).To(Equal("*"))
		delete(creds.allowed, "*")
		_, err = read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"list-repos"}`)})
		var refusal *CredentialRefusal
		Expect(errors.As(err, &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(CodeResourceNotAllowed))
		Expect(client.reads.Load()).To(Equal(int32(1)))
	})

	It("gives a tool of the add-on's own page no credential and asks the source for none", func() {
		creds.err = errors.New("store down")
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"page-settings"}`), Credential: "stolen"})
		Expect(err).NotTo(HaveOccurred())
		Expect(client.seen).To(HaveLen(1))
		Expect(client.seen[0].Credential).To(BeEmpty())
		Expect(client.seen[0].ResourceKey).To(BeEmpty())
		Expect(creds.calls).To(BeEmpty())
	})

	It("drops a credential, a key or a resource that the caller put in the request", func() {
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"whoami"}`), Credential: "stolen", ResourceKey: "other/repo"})
		Expect(err).NotTo(HaveOccurred())
		Expect(client.seen[0].Credential).To(Equal("secret"))
		Expect(client.seen[0].ResourceKey).To(BeEmpty())
		_, err = read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"whoami"}`), ResourceID: "r-ffffffffff"})
		Expect(err).To(MatchError(ErrForbidden))
		Expect(client.reads.Load()).To(Equal(int32(1)))
	})

	It("fails closed when the owner has no credential source", func() {
		g.Credentials = nil
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"whoami"}`)})
		Expect(err).To(MatchError(ErrUnavailable))
		Expect(client.reads.Load()).To(Equal(int32(0)))
	})

	It("reports an outage of the source as unavailable", func() {
		creds.err = errors.New("store down")
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"whoami"}`)})
		Expect(err).To(MatchError(ErrUnavailable))
	})

	It("denies a stale generation as a denial, never as an outage of the owner", func() {
		g.Resolver = resolved{}
		_, err := g.Dispatch(ctx, contract.Actor{UserID: "alice", CredentialID: "c", Mode: "named-user"},
			contract.Request{AddonID: "sample", ResourceID: "r-0123456789", Generation: 7, OperationID: "op", Action: "read", Arguments: json.RawMessage(`{"q":"plain"}`)})
		Expect(err).To(MatchError(ErrForbidden))
		Expect(err).NotTo(MatchError(ErrUnavailable))
		Expect(client.reads.Load()).To(Equal(int32(0)))
	})

	It("does not touch an add-on that declares no credentials", func() {
		plain := capSet{toolSet: toolSet{gen: 1, tools: map[string][]string{"sample": {"whoami"}}}, defs: defs[1:]}
		g.Tools = plain
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"whoami"}`), Credential: "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(creds.calls).To(BeEmpty())
		Expect(client.seen[0].Credential).To(BeEmpty())
	})
})
