// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// tokenTransport serves one credential-only kind and answers nothing else.
type tokenTransport struct{}

func (tokenTransport) Kinds(context.Context) ([]studio.ConnectorKind, error) {
	return []studio.ConnectorKind{{AddonID: "tracker", ConnectorKind: contract.ConnectorKind{ID: "tracker", Title: "Tracker", Delivery: contract.DeliveryPull, AgentOptional: true}}}, nil
}

func (tokenTransport) Call(context.Context, studio.ConnectorCallScope, contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return contract.ConnectorResponse{}, nil
}

// The connection a tool call of an add-on with a credential uses: one of the
// set the chat enabled, never a first match. The add-on here is a stand-in; the
// rules do not depend on which product it is.
var _ = Describe("the credential source of the add-on gateway", func() {
	ctx := context.Background()
	const user = "alice"
	var (
		app   *Application
		chats store.Store
		src   credentialSource
		work  string
		home  string
	)
	refusal := func(err error) string {
		var r *gateway.CredentialRefusal
		Expect(errors.As(err, &r)).To(BeTrue(), "error = %v", err)
		return r.Code
	}
	actor := contract.Actor{UserID: user, CredentialID: "chats", Mode: "named-user"}
	inChat := func(chat string) context.Context {
		return hostops.WithBinding(ctx, hostops.Binding{Principal: user, ChatID: chat})
	}
	newChat := func(rec session.ChatRecord) string {
		rec.ID, rec.Principal, rec.Kind, rec.Model = "chat-"+rec.Title, user, session.KindChat, "m"
		Expect(chats.CreateChat(ctx, rec)).To(Succeed())
		return rec.ID
	}
	BeforeEach(func() {
		var err error
		chats, err = store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		agentStore, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := studio.New(studio.Deps{Store: chats, Agents: agentStore, Provider: provider.New(), Harness: studiotest.NewGateHarness(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		bindings, err := connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		conns, err := studio.NewConnectors(svc, studio.ConnectorOptions{Store: bindings, Transport: tokenTransport{}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conns.Close)
		svc.SetConnectors(conns)
		app = &Application{studioStore: chats, studioConnectors: conns}
		src = credentialSource{app}
		mk := func(name, token string) string {
			b, err := conns.CreateConnector(ctx, user, studio.ConnectorInput{Kind: "tracker", Name: name, Token: token, Resources: []string{"acme/widgets"}})
			Expect(err).NotTo(HaveOccurred())
			return b.ID
		}
		work, home = mk("Work", "token-work"), mk("Home", "token-home")
	})

	It("refuses a call whose chat selected nothing, with several connections", func() {
		_, err := src.Credential(inChat(newChat(session.ChatRecord{Title: "none"})), actor, "tracker", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeSelectConnection))
	})
	It("uses the connection the chat selected, and only its resource list", func() {
		id := newChat(session.ChatRecord{Title: "sel", Connections: connsets.Sets{"tracker": {home}}})
		grant, err := src.Credential(inChat(id), actor, "tracker", "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.Credential).To(Equal("token-home"))
		Expect(grant.BindingID).To(Equal(home))
		Expect(grant.BindingName).To(Equal("Home"))
		_, err = src.Credential(inChat(id), actor, "tracker", "other/repo")
		Expect(refusal(err)).To(Equal(gateway.CodeResourceNotAllowed))
	})
	It("uses the one connection a chat enables, with no name in the call", func() {
		id := newChat(session.ChatRecord{Title: "one", Connections: connsets.Sets{"tracker": {work}}})
		grant, err := src.Credential(inChat(id), actor, "tracker", "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.BindingID).To(Equal(work))
	})
	It("refuses a call that names no connection when the chat enables two", func() {
		id := newChat(session.ChatRecord{Title: "two", Connections: connsets.Sets{"tracker": {work, home}}})
		_, err := src.Credential(inChat(id), actor, "tracker", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeSelectConnection), "the host picks neither")
	})
	It("takes the connection the studio named, when it is in the chat's set", func() {
		id := newChat(session.ChatRecord{Title: "two2", Connections: connsets.Sets{"tracker": {work, home}}})
		for _, want := range []string{work, home} {
			grant, err := src.Credential(gateway.WithConnection(inChat(id), want), actor, "tracker", "acme/widgets")
			Expect(err).NotTo(HaveOccurred())
			Expect(grant.BindingID).To(Equal(want))
		}
	})
	It("refuses a named connection that is the person's but not in the chat's set", func() {
		id := newChat(session.ChatRecord{Title: "sel2", Connections: connsets.Sets{"tracker": {home}}})
		_, err := src.Credential(gateway.WithConnection(inChat(id), work), actor, "tracker", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeSelectConnection))
	})
	It("lets the broker apply a proposal with the connection it was previewed with, outside a chat", func() {
		grant, err := src.Credential(gateway.WithConnection(ctx, work), actor, "tracker", "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.BindingID).To(Equal(work))
		_, err = src.Credential(gateway.WithConnection(ctx, "no-such-binding"), actor, "tracker", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeNoCredential))
	})
	It("refuses a connection that belongs to someone else", func() {
		_, err := src.Credential(gateway.WithConnection(ctx, work), contract.Actor{UserID: "mallory", CredentialID: "chats", Mode: "named-user"}, "tracker", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeNoCredential))
	})
	It("refuses a call from no chat when there are several connections, and names no first", func() {
		_, err := src.Credential(ctx, actor, "tracker", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeSelectConnection))
	})
	It("refuses a chat of another person", func() {
		id := newChat(session.ChatRecord{Title: "mine", Connections: connsets.Sets{"tracker": {work}}})
		bad := hostops.WithBinding(ctx, hostops.Binding{Principal: "mallory", ChatID: id})
		_, err := src.Credential(bad, contract.Actor{UserID: "mallory", CredentialID: "chats", Mode: "named-user"}, "tracker", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeSelectConnection))
	})
})

// A model chooses among the connections of a chat by name, so a person cannot
// have two credential-only connections of one add-on that fold to one name.
var _ = Describe("the names of connections", func() {
	ctx := context.Background()
	const user = "alice"
	var conns *studio.Connectors
	BeforeEach(func() {
		chats, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := studio.New(studio.Deps{Store: chats, Provider: provider.New(), Harness: studiotest.NewGateHarness(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		bindings, err := connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		conns, err = studio.NewConnectors(svc, studio.ConnectorOptions{Store: bindings, Transport: tokenTransport{}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conns.Close)
		svc.SetConnectors(conns)
	})
	mk := func(owner, name string) (studio.ConnectorInfo, error) {
		return conns.CreateConnector(ctx, owner, studio.ConnectorInput{Kind: "tracker", Name: name, Token: "t", Resources: []string{"a/b"}})
	}
	It("refuses a name that another connection of the person already has, and says why", func() {
		_, err := mk(user, "Work")
		Expect(err).NotTo(HaveOccurred())
		_, err = mk(user, "Work")
		Expect(err).To(MatchError(ContainSubstring(`already have a connection named "Work"`)))
		Expect(errors.Is(err, connectors.ErrInvalid)).To(BeTrue(), "a 400, not an outage")
	})
	It("compares by handle: case and punctuation do not make a new name", func() {
		_, err := mk(user, "Work Acme")
		Expect(err).NotTo(HaveOccurred())
		_, err = mk(user, "work-acme!")
		Expect(err).To(MatchError(ContainSubstring("already have a connection")))
	})
	It("refuses a name with no letter or digit", func() {
		_, err := mk(user, "---")
		Expect(err).To(MatchError(ContainSubstring("letter or digit")))
	})
	It("lets two people use the same name", func() {
		_, err := mk(user, "Work")
		Expect(err).NotTo(HaveOccurred())
		_, err = mk("bob", "Work")
		Expect(err).NotTo(HaveOccurred())
	})
	It("refuses a rename onto another name, and allows keeping its own", func() {
		a, err := mk(user, "Work")
		Expect(err).NotTo(HaveOccurred())
		b, err := mk(user, "Home")
		Expect(err).NotTo(HaveOccurred())
		_, err = conns.UpdateConnector(ctx, user, b.ID, studio.ConnectorInput{Name: "WORK", Resources: []string{"a/b"}})
		Expect(err).To(MatchError(ContainSubstring("already have a connection")))
		_, err = conns.UpdateConnector(ctx, user, a.ID, studio.ConnectorInput{Name: "work", Resources: []string{"a/b"}})
		Expect(err).NotTo(HaveOccurred(), "a connection may change its own capitalisation")
	})
})
