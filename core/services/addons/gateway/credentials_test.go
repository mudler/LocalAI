// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// ctxClient answers a read with the error of its context, as a client whose
// call was cancelled does.
type ctxClient struct{ *envClient }

func (c ctxClient) Read(ctx context.Context, e contract.Envelope) (contract.Result, error) {
	if err := ctx.Err(); err != nil {
		return contract.Result{}, err
	}
	return c.envClient.Read(ctx, e)
}

type fakeCredentials struct {
	allowed map[string]string // key -> id
	calls   []string
	err     error
	scopes  []string
	// hold is set on every grant, as a source does for a shared binding.
	hold func(context.Context, func(context.Context) error) error
}

func (f *fakeCredentials) Credential(_ context.Context, a contract.Actor, addonID, key string) (CredentialGrant, error) {
	f.calls = append(f.calls, a.UserID+"|"+addonID+"|"+key)
	if f.err != nil {
		return CredentialGrant{}, f.err
	}
	if key == "" {
		return CredentialGrant{Credential: "secret", GrantedScopes: f.scopes, Hold: f.hold}, nil
	}
	id, ok := f.allowed[key]
	if !ok {
		return CredentialGrant{}, &CredentialRefusal{Code: CodeResourceNotAllowed, Allowed: []string{"acme/widgets"}}
	}
	return CredentialGrant{Credential: "secret", ResourceKey: key, ResourceID: id, Hold: f.hold}, nil
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

	It("runs the call of a shared credential inside its hold, and never when the hold refuses", func() {
		type holdKey struct{}
		held := 0
		creds.hold = func(ctx context.Context, run func(context.Context) error) error {
			held++
			return run(context.WithValue(ctx, holdKey{}, "admitted"))
		}
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"whoami"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(Equal(1))
		Expect(client.reads.Load()).To(Equal(int32(1)))

		// A lowering that already happened refuses at the hold: the add-on
		// gets nothing, and the answer is the plain denial.
		creds.hold = func(context.Context, func(context.Context) error) error { return ErrForbidden }
		_, err = read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"whoami"}`)})
		Expect(err).To(MatchError(ErrForbidden))
		Expect(client.reads.Load()).To(Equal(int32(1)))

		// A lowering while the call runs cancels the context the add-on got.
		cancelled := make(chan struct{})
		creds.hold = func(ctx context.Context, run func(context.Context) error) error {
			cctx, cancel := context.WithCancel(ctx)
			cancel()
			err := run(cctx)
			close(cancelled)
			return err
		}
		g.Runtime = currentFixture{ctxClient{envClient: client}}
		_, err = read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"whoami"}`)})
		Expect(err).To(MatchError(context.Canceled))
		Eventually(cancelled).Should(BeClosed())
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

	It("drops granted scopes that the caller put in the request and sets the host's", func() {
		_, err := read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"whoami"}`), GrantedScopes: []string{"admin"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(client.seen[0].GrantedScopes).To(BeEmpty())
		creds.scopes = []string{"read"}
		_, err = read(g, contract.Request{Arguments: json.RawMessage(`{"tool":"whoami"}`), GrantedScopes: []string{"admin"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(client.seen[1].GrantedScopes).To(Equal([]string{"read"}))
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

// perAddonCredentials gives each add-on ID its own credential and resource
// list. It stands for any owner source: the host asks it by ID and never
// branches on the ID itself.
type perAddonCredentials struct{ lists map[string]*fakeCredentials }

func (p perAddonCredentials) Credential(ctx context.Context, a contract.Actor, addonID, key string) (CredentialGrant, error) {
	f, ok := p.lists[addonID]
	if !ok {
		return CredentialGrant{}, &CredentialRefusal{Code: CodeNoCredential}
	}
	g, err := f.Credential(ctx, a, addonID, key)
	if err == nil {
		g.Credential = "secret-of-" + addonID
	}
	return g, err
}

var _ = Describe("two differently named add-ons with credentials", Label("tools"), func() {
	ctx := context.Background()
	defs := []contract.ToolDefinition{
		{Name: "read-file", Kind: "read", Description: "x", InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string"}},"required":["repo"],"additionalProperties":false}`)},
	}
	source := credCaps{capSet{toolSet: toolSet{gen: 1, tools: map[string][]string{"providerA": {"read-file"}, "providerB": {"read-file"}}}, defs: defs}}

	It("needs no host change and no branch on the add-on ID", func() {
		client := &envClient{}
		a := &fakeCredentials{allowed: map[string]string{"acme/widgets": "r-0123456789"}}
		b := &fakeCredentials{allowed: map[string]string{"acme/gadgets": "r-9876543210"}}
		g := &Gateway{Admission: &addonAdmitted{}, Resolver: strictResolver{}, Runtime: currentFixture{client}, Tools: source,
			Credentials: perAddonCredentials{lists: map[string]*fakeCredentials{"providerA": a, "providerB": b}},
			Revalidate:  func(context.Context, contract.Actor) error { return nil }}
		read := func(id, repo string) (contract.Result, error) {
			req := contract.Request{AddonID: id, Generation: 1, OperationID: "op", Action: "read",
				Arguments: json.RawMessage(`{"tool":"read-file","repo":"` + repo + `"}`)}
			return g.Dispatch(ctx, contract.Actor{UserID: "alice", CredentialID: "c", Mode: "named-user"}, req)
		}

		_, err := read("providerA", "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		_, err = read("providerB", "acme/gadgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(client.seen).To(HaveLen(2))
		Expect(client.seen[0].Credential).To(Equal("secret-of-providerA"))
		Expect(client.seen[0].ResourceID).To(Equal("r-0123456789"))
		Expect(client.seen[1].Credential).To(Equal("secret-of-providerB"))
		Expect(client.seen[1].ResourceID).To(Equal("r-9876543210"))

		// A resource of one add-on is not allowed for the other, and the
		// refusal is the same typed one, before the add-on is called.
		for _, c := range []struct{ id, repo string }{{"providerA", "acme/gadgets"}, {"providerB", "acme/widgets"}} {
			_, err = read(c.id, c.repo)
			var refusal *CredentialRefusal
			Expect(errors.As(err, &refusal)).To(BeTrue())
			Expect(refusal.Code).To(Equal(CodeResourceNotAllowed))
			Expect(err).To(MatchError(ErrForbidden))
		}
		Expect(client.reads.Load()).To(Equal(int32(2)))
	})
})

var _ = Describe("the refusal codes of a credentialed call", func() {
	codes := []string{CodeResourceNotAllowed, CodeNoCredential, CodeWriteNotAllowed, CodeSelectConnection, CodeReconnectNeeded, CodeOwnerReconnectNeeded}
	It("names reconnect_needed on the wire", func() {
		Expect(CodeReconnectNeeded).To(Equal("reconnect_needed"))
	})
	It("crosses the carrier from the owner to a forwarding frontend as the same refusal", func() {
		for _, code := range codes {
			Expect(refusalCode(code)).To(BeTrue(), code)
			rec := httptest.NewRecorder()
			typedError(rec, fmt.Errorf("call: %w", &CredentialRefusal{Code: code}))
			Expect(rec.Code).To(Equal(http.StatusForbidden), code)
			var b sdkWire
			Expect(json.Unmarshal(rec.Body.Bytes(), &b)).To(Succeed())
			Expect(b.Type).To(Equal(code))
			Expect(refusalCode(b.Type)).To(BeTrue(), code)
		}
		Expect(refusalCode("forbidden")).To(BeFalse())
	})
	It("is a plain denial for a caller that does not tell refusals apart", func() {
		Expect(errors.Is(&CredentialRefusal{Code: CodeReconnectNeeded}, ErrForbidden)).To(BeTrue())
	})
})

// optionalCaps is credCaps with a resource argument that a call may leave out.
type optionalCaps struct{ capSet }

func (c optionalCaps) DeclaredCapabilities(ctx context.Context, id string, gen uint64) (contract.Capabilities, error) {
	caps, err := c.capSet.DeclaredCapabilities(ctx, id, gen)
	caps.Credentials = []contract.CredentialSlot{{ID: "token", Title: "Token", Kind: contract.CredentialKindToken, ResourceArgument: "repo", ResourceOptional: true}}
	return caps, err
}

// defaultCredentials is a source with one connection that lists the keys in
// only; with more than one key the connection cannot pick for the call.
type defaultCredentials struct {
	fakeCredentials
	only []string
}

func (f *defaultCredentials) DefaultCredential(_ context.Context, a contract.Actor, addonID string) (CredentialGrant, error) {
	f.calls = append(f.calls, a.UserID+"|"+addonID+"|<default>")
	if len(f.only) != 1 {
		return CredentialGrant{}, &CredentialRefusal{Code: CodeResourceNotAllowed, Allowed: f.only}
	}
	return CredentialGrant{Credential: "secret", ResourceKey: f.only[0], ResourceID: f.allowed[f.only[0]]}, nil
}

var _ = Describe("a read that may leave out the resource argument", Label("tools"), func() {
	ctx := context.Background()
	defs := []contract.ToolDefinition{
		{Name: "read-file", Kind: "read", Description: "x", InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string"}},"additionalProperties":false}`)},
	}
	source := optionalCaps{capSet{toolSet: toolSet{gen: 1, tools: map[string][]string{"sample": {"read-file"}}}, defs: defs}}
	var (
		client *envClient
		creds  *defaultCredentials
	)
	gateway := func(src CredentialSource) *Gateway {
		return &Gateway{Admission: &addonAdmitted{}, Resolver: strictResolver{}, Runtime: currentFixture{client}, Tools: source, Credentials: src,
			Revalidate: func(context.Context, contract.Actor) error { return nil }}
	}
	read := func(g *Gateway, args string) (contract.Result, error) {
		req := contract.Request{Arguments: json.RawMessage(args), AddonID: "sample", Generation: 1, OperationID: "op", Action: "read"}
		return g.Dispatch(ctx, contract.Actor{UserID: "alice", CredentialID: "c", Mode: "named-user"}, req)
	}
	BeforeEach(func() {
		client = &envClient{}
		creds = &defaultCredentials{fakeCredentials: fakeCredentials{allowed: map[string]string{"http://ha.local:8123": "r-0123456789"}}, only: []string{"http://ha.local:8123"}}
	})

	It("gives the add-on the resource of the only connection when the call names none", func() {
		_, err := read(gateway(creds), `{"tool":"read-file"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(client.seen).To(HaveLen(1))
		Expect(client.seen[0].Credential).To(Equal("secret"))
		Expect(client.seen[0].ResourceKey).To(Equal("http://ha.local:8123"))
		Expect(client.seen[0].ResourceID).To(Equal("r-0123456789"))
		Expect(creds.calls).To(Equal([]string{"alice|sample|<default>"}))
	})

	It("holds a call that names the resource to the list, as before", func() {
		_, err := read(gateway(creds), `{"tool":"read-file","repo":"http://ha.local:8123"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(creds.calls).To(Equal([]string{"alice|sample|http://ha.local:8123"}))
		Expect(client.seen[0].ResourceKey).To(Equal("http://ha.local:8123"))
	})

	It("refuses a resource that is not listed, names the listed ones and never calls the add-on", func() {
		_, err := read(gateway(creds), `{"tool":"read-file","repo":"http://evil.example"}`)
		var refusal *CredentialRefusal
		Expect(errors.As(err, &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(CodeResourceNotAllowed))
		Expect(refusal.Allowed).To(Equal([]string{"acme/widgets"}))
		Expect(client.reads.Load()).To(Equal(int32(0)))
	})

	It("names the listed resources when the connection lists several, so the model picks one", func() {
		creds.only = []string{"http://a.local:8123", "http://b.local:8123"}
		_, err := read(gateway(creds), `{"tool":"read-file"}`)
		var refusal *CredentialRefusal
		Expect(errors.As(err, &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(CodeResourceNotAllowed))
		Expect(refusal.Allowed).To(Equal([]string{"http://a.local:8123", "http://b.local:8123"}))
		Expect(client.reads.Load()).To(Equal(int32(0)))
	})

	It("refuses the omission when the source cannot serve it", func() {
		_, err := read(gateway(&creds.fakeCredentials), `{"tool":"read-file"}`)
		Expect(err).To(HaveOccurred())
		Expect(client.reads.Load()).To(Equal(int32(0)))
	})

	It("does not change a slot that is not optional: the omission is refused", func() {
		g := &Gateway{Admission: &addonAdmitted{}, Resolver: strictResolver{}, Runtime: currentFixture{client}, Tools: credCaps{capSet{toolSet: toolSet{gen: 1, tools: map[string][]string{"sample": {"read-file"}}}, defs: defs}},
			Credentials: creds, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		_, err := read(g, `{"tool":"read-file"}`)
		Expect(err).To(HaveOccurred())
		Expect(creds.calls).To(BeEmpty())
		Expect(client.reads.Load()).To(Equal(int32(0)))
	})
})
