// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// spyRuntime records the envelopes the broker sends, so a test can see where
// the credential goes.
type spyRuntime struct {
	*fixtureRuntime
	smu      sync.Mutex
	previews []contract.Envelope
	applies  []contract.Envelope
}

func (s *spyRuntime) Preview(ctx context.Context, e contract.Envelope) (contract.Preview, error) {
	s.smu.Lock()
	s.previews = append(s.previews, e)
	s.smu.Unlock()
	return s.fixtureRuntime.Preview(ctx, e)
}

func (s *spyRuntime) Apply(ctx context.Context, e contract.Envelope, r contract.Revision) (contract.Receipt, error) {
	s.smu.Lock()
	s.applies = append(s.applies, e)
	s.smu.Unlock()
	return s.fixtureRuntime.Apply(ctx, e, r)
}

// credentialTools declares an add-on with a credential slot and one write tool.
type credentialTools struct{ approval string }

func (c credentialTools) DeclaredTools(context.Context, string, uint64) ([]string, error) {
	return []string{"comment"}, nil
}

func (c credentialTools) DeclaredCapabilities(context.Context, string, uint64) (contract.Capabilities, error) {
	return contract.Capabilities{
		Tools:       []string{"comment"},
		Credentials: []contract.CredentialSlot{{ID: "token", Title: "Token", Kind: contract.CredentialKindToken, ResourceArgument: "repo"}},
		ToolDefinitions: []contract.ToolDefinition{{Name: "comment", Kind: contract.ToolKindWrite, Approval: c.approval,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string"},"body":{"type":"string"}},"required":["repo","body"],"additionalProperties":false}`)}},
	}, nil
}

// bindingSource stands for the person's binding.
type bindingSource struct {
	mu    sync.Mutex
	mode  string
	repos []string
	token string
}

func (b *bindingSource) set(mode string) { b.mu.Lock(); b.mode = mode; b.mu.Unlock() }

func (b *bindingSource) Credential(_ context.Context, _ contract.Actor, _ string, key string) (gateway.CredentialGrant, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.repos {
		if strings.EqualFold(r, key) {
			return gateway.CredentialGrant{Credential: b.token, ResourceKey: r, ResourceID: "r-0123456789", WriteMode: b.mode}, nil
		}
	}
	return gateway.CredentialGrant{}, &gateway.CredentialRefusal{Code: gateway.CodeResourceNotAllowed, Allowed: b.repos}
}

func chat(id string) contract.Actor {
	return contract.Actor{UserID: id, CredentialID: "chats", Mode: "named-user"}
}

var _ = Describe("the write tools of an add-on with a credential", Label("proposals", "credentials"), func() {
	ctx := context.Background()
	const secret = "ghp_secretsecretsecretsecretsecretsecret12"
	var (
		f       *brokerFixture
		spy     *spyRuntime
		binding *bindingSource
		who     = named("owner")
	)
	setup := func(approval string) {
		binding = &bindingSource{mode: gateway.WritePropose, repos: []string{"acme/widgets"}, token: secret}
		rt := newFixtureRuntime()
		spy = &spyRuntime{fixtureRuntime: rt}
		f = openBrokerFixture(ModeNamedUser, GinkgoT().TempDir(), rt, &fixtureInstances{gen: 1, rt: spy}, &clock{t: time.Now()}, &account{blocked: map[string]bool{}},
			func(o *BrokerOptions) {
				o.Tools = credentialTools{approval: approval}
				o.Credentials = binding
				o.ConfirmCredential = func(a contract.Actor) bool {
					return a.CredentialID == "chats" || strings.HasPrefix(a.CredentialID, "session:")
				}
				o.ConfirmPerson = func(a contract.Actor) bool { return strings.HasPrefix(a.CredentialID, "session:") }
			})
		DeferCleanup(func() { f.close() })
	}
	request := func(op, repo string) contract.Request {
		return contract.Request{AddonID: "gh", OperationID: op, Generation: 1, Action: "write",
			Arguments: json.RawMessage(`{"tool":"comment","repo":"` + repo + `","body":"hello"}`)}
	}

	Describe("in propose mode", func() {
		BeforeEach(func() { setup("") })

		It("stores a proposal that awaits a person, with the credential in the preview and never at rest", func() {
			p, err := f.broker.Propose(ctx, who, request("op-1", "Acme/Widgets"))
			Expect(err).NotTo(HaveOccurred())
			Expect(p.State).To(Equal(gateway.ProposalPending))
			Expect(p.AwaitsPerson).To(BeTrue())
			Expect(p.ResourceID).To(Equal("r-0123456789"))
			Expect(spy.previews).To(HaveLen(1))
			Expect(spy.previews[0].Request.Credential).To(Equal(secret))
			Expect(spy.previews[0].Request.ResourceKey).To(Equal("acme/widgets"))
			disk, err := os.ReadFile(filepath.Join(f.dir, "proposals", proposalFileName))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(disk)).NotTo(ContainSubstring(secret))
			Expect(string(disk)).NotTo(ContainSubstring("ghp_"))
			view, _ := json.Marshal(p)
			Expect(string(view)).NotTo(ContainSubstring(secret))
		})
		It("does not let a chat turn apply it, and lets the person do it with a fresh credential", func() {
			_, err := f.broker.Propose(ctx, who, request("op-1", "acme/widgets"))
			Expect(err).NotTo(HaveOccurred())
			_, err = f.broker.Confirm(ctx, chat("owner"), "op-1")
			Expect(err).To(MatchError(ErrForbidden))
			Expect(spy.applies).To(BeEmpty())
			receipt, err := f.broker.Confirm(ctx, session("owner"), "op-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
			Expect(spy.applies).To(HaveLen(1))
			Expect(spy.applies[0].Request.Credential).To(Equal(secret))
			Expect(spy.applies[0].Request.ResourceKey).To(Equal("acme/widgets"))
		})
		It("does not let a chat turn confirm it when the chat and the browser share one credential", func() {
			// Local-owner mode: one credential stands for both, so only the mark
			// of the call tells a chat turn from the browser.
			setup("")
			f.broker.opts.ConfirmCredential = func(contract.Actor) bool { return true }
			f.broker.opts.ConfirmPerson = func(contract.Actor) bool { return true }
			_, err := f.broker.Propose(ctx, who, request("op-1", "acme/widgets"))
			Expect(err).NotTo(HaveOccurred())
			_, err = f.broker.Confirm(gateway.WithChatTurn(ctx), who, "op-1")
			Expect(err).To(MatchError(ErrForbidden))
			Expect(spy.applies).To(BeEmpty())
			receipt, err := f.broker.Confirm(ctx, who, "op-1")
			Expect(err).NotTo(HaveOccurred(), "the same proposal is still pending for the browser")
			Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
		})
		It("refuses a repository that is not on the list before the add-on is called", func() {
			_, err := f.broker.Propose(ctx, who, request("op-1", "acme/secret"))
			var refusal *gateway.CredentialRefusal
			Expect(errors.As(err, &refusal)).To(BeTrue())
			Expect(refusal.Code).To(Equal(gateway.CodeResourceNotAllowed))
			Expect(spy.previews).To(BeEmpty())
			_, err = f.broker.Get(ctx, who, "op-1")
			Expect(err).To(HaveOccurred(), "nothing was stored")
		})
		It("refuses the call when the write mode was lowered before the confirmation, and keeps the proposal pending", func() {
			_, err := f.broker.Propose(ctx, who, request("op-1", "acme/widgets"))
			Expect(err).NotTo(HaveOccurred())
			binding.set(gateway.WriteOff)
			_, err = f.broker.Confirm(ctx, session("owner"), "op-1")
			var refusal *gateway.CredentialRefusal
			Expect(errors.As(err, &refusal)).To(BeTrue())
			Expect(refusal.Code).To(Equal(gateway.CodeWriteNotAllowed))
			Expect(spy.applies).To(BeEmpty())
			got, err := f.broker.Get(ctx, who, "op-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.State).To(Equal(gateway.ProposalPending))
		})
		It("refuses a call that names the host resource itself", func() {
			r := request("op-1", "acme/widgets")
			r.ResourceID = "r-0123456789"
			_, err := f.broker.Propose(ctx, who, r)
			Expect(err).To(MatchError(ErrForbidden))
			Expect(spy.previews).To(BeEmpty())
		})
	})

	It("never runs a write while the mode is off", func() {
		setup("")
		binding.set("")
		for _, mode := range []string{"", gateway.WriteOff} {
			binding.set(mode)
			_, err := f.broker.Propose(ctx, who, request("op-1", "acme/widgets"))
			var refusal *gateway.CredentialRefusal
			Expect(errors.As(err, &refusal)).To(BeTrue())
			Expect(refusal.Code).To(Equal(gateway.CodeWriteNotAllowed))
		}
		Expect(spy.previews).To(BeEmpty())
	})

	It("is unavailable when the host has no credential source", func() {
		setup("")
		f.broker.opts.Credentials = nil
		_, err := f.broker.Propose(ctx, who, request("op-1", "acme/widgets"))
		Expect(err).To(MatchError(ErrUnavailable))
	})

	Describe("in auto mode", func() {
		It("lets a chat turn apply the proposal it just stored", func() {
			setup("")
			binding.set(gateway.WriteAuto)
			p, err := f.broker.Propose(ctx, who, request("op-1", "acme/widgets"))
			Expect(err).NotTo(HaveOccurred())
			Expect(p.AwaitsPerson).To(BeFalse())
			receipt, err := f.broker.Confirm(ctx, chat("owner"), "op-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
		})
		It("keeps a proposal for a person when the tool declares approval always", func() {
			setup(contract.ApprovalAlways)
			binding.set(gateway.WriteAuto)
			p, err := f.broker.Propose(ctx, who, request("op-1", "acme/widgets"))
			Expect(err).NotTo(HaveOccurred())
			Expect(p.AwaitsPerson).To(BeTrue())
			_, err = f.broker.Confirm(ctx, chat("owner"), "op-1")
			Expect(err).To(MatchError(ErrForbidden))
			Expect(spy.applies).To(BeEmpty())
		})
		It("falls back to a person when the mode dropped to propose before the apply", func() {
			setup("")
			binding.set(gateway.WriteAuto)
			_, err := f.broker.Propose(ctx, who, request("op-1", "acme/widgets"))
			Expect(err).NotTo(HaveOccurred())
			binding.set(gateway.WritePropose)
			_, err = f.broker.Confirm(ctx, chat("owner"), "op-1")
			Expect(err).To(MatchError(ErrForbidden))
			Expect(spy.applies).To(BeEmpty())
		})
		It("does not let another user confirm", func() {
			setup("")
			binding.set(gateway.WriteAuto)
			_, err := f.broker.Propose(ctx, who, request("op-1", "acme/widgets"))
			Expect(err).NotTo(HaveOccurred())
			_, err = f.broker.Confirm(ctx, chat("intruder"), "op-1")
			Expect(err).To(HaveOccurred())
			Expect(spy.applies).To(BeEmpty())
		})
	})
})

// connectionSource stands for a person with several connections. A call that
// carries a connection of its own (the apply of a proposal) gets that one;
// every other call gets the one the chat selected at that moment.
type connectionSource struct {
	mu       sync.Mutex
	selected string
	tokens   map[string]string
	names    map[string]string
}

func (c *connectionSource) choose(id string) { c.mu.Lock(); c.selected = id; c.mu.Unlock() }
func (c *connectionSource) remove(id string) { c.mu.Lock(); delete(c.tokens, id); c.mu.Unlock() }

func (c *connectionSource) Credential(ctx context.Context, _ contract.Actor, _ string, key string) (gateway.CredentialGrant, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := gateway.ConnectionOverride(ctx)
	if id == "" {
		id = c.selected
	}
	tok, ok := c.tokens[id]
	if !ok {
		return gateway.CredentialGrant{}, &gateway.CredentialRefusal{Code: gateway.CodeSelectConnection}
	}
	return gateway.CredentialGrant{Credential: tok, ResourceKey: key, ResourceID: "r-0123456789", WriteMode: gateway.WritePropose, BindingID: id, BindingName: c.names[id]}, nil
}

var _ = Describe("the connection of a proposal", Label("proposals", "credentials"), func() {
	ctx := context.Background()
	var (
		f   *brokerFixture
		spy *spyRuntime
		src *connectionSource
		who = named("owner")
	)
	BeforeEach(func() {
		src = &connectionSource{selected: "b1", tokens: map[string]string{"b1": "token-work", "b2": "token-home"}, names: map[string]string{"b1": "Work", "b2": "Home"}}
		rt := newFixtureRuntime()
		spy = &spyRuntime{fixtureRuntime: rt}
		f = openBrokerFixture(ModeNamedUser, GinkgoT().TempDir(), rt, &fixtureInstances{gen: 1, rt: spy}, &clock{t: time.Now()}, &account{blocked: map[string]bool{}},
			func(o *BrokerOptions) {
				o.Tools = credentialTools{}
				o.Credentials = src
				o.ConfirmCredential = func(a contract.Actor) bool {
					return a.CredentialID == "chats" || strings.HasPrefix(a.CredentialID, "session:")
				}
				o.ConfirmPerson = func(a contract.Actor) bool { return strings.HasPrefix(a.CredentialID, "session:") }
			})
		DeferCleanup(func() { f.close() })
	})
	propose := func(op string) gateway.Proposal {
		p, err := f.broker.Propose(ctx, who, contract.Request{AddonID: "gh", OperationID: op, Generation: 1, Action: "write",
			Arguments: json.RawMessage(`{"tool":"comment","repo":"acme/widgets","body":"hello"}`)})
		Expect(err).NotTo(HaveOccurred())
		return p
	}

	It("names the connection it was previewed with", func() {
		p := propose("op-1")
		Expect(p.ConnectionID).To(Equal("b1"))
		Expect(p.ConnectionName).To(Equal("Work"))
		Expect(spy.previews[0].Request.Credential).To(Equal("token-work"))
	})
	It("applies with that very connection, even if the chat selects another one meanwhile", func() {
		propose("op-1")
		src.choose("b2")
		_, err := f.broker.Confirm(ctx, session("owner"), "op-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(spy.applies).To(HaveLen(1))
		Expect(spy.applies[0].Request.Credential).To(Equal("token-work"), "the connection the person saw")
	})
	It("refuses the apply when that connection is gone, and sends nothing", func() {
		propose("op-1")
		src.remove("b1")
		src.choose("b2")
		_, err := f.broker.Confirm(ctx, session("owner"), "op-1")
		Expect(err).To(HaveOccurred())
		Expect(spy.applies).To(BeEmpty())
	})
	It("refuses to propose when no connection is selected", func() {
		src.choose("")
		_, err := f.broker.Propose(ctx, who, contract.Request{AddonID: "gh", OperationID: "op-2", Generation: 1, Action: "write",
			Arguments: json.RawMessage(`{"tool":"comment","repo":"acme/widgets","body":"hello"}`)})
		var refusal *gateway.CredentialRefusal
		Expect(errors.As(err, &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(gateway.CodeSelectConnection))
	})
})
