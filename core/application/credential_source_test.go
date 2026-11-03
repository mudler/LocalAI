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
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// githubTransport serves one credential-only kind and answers nothing else.
type githubTransport struct{}

func (githubTransport) Kinds(context.Context) ([]studio.ConnectorKind, error) {
	return []studio.ConnectorKind{{AddonID: "github", ConnectorKind: contract.ConnectorKind{ID: "github", Title: "GitHub", Delivery: contract.DeliveryPull, AgentOptional: true}}}, nil
}

func (githubTransport) Call(context.Context, studio.ConnectorCallScope, contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return contract.ConnectorResponse{}, nil
}

// The connection a tool call of an add-on with a credential uses: the one the
// chat selected, never a first match.
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
		conns, err := studio.NewConnectors(svc, studio.ConnectorOptions{Store: bindings, Transport: githubTransport{}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conns.Close)
		svc.SetConnectors(conns)
		app = &Application{studioStore: chats, studioConnectors: conns}
		src = credentialSource{app}
		mk := func(name, token string) string {
			b, err := conns.CreateConnector(ctx, user, studio.ConnectorInput{Kind: "github", Name: name, Token: token, Resources: []string{"acme/widgets"}})
			Expect(err).NotTo(HaveOccurred())
			return b.ID
		}
		work, home = mk("Work", "token-work"), mk("Home", "token-home")
	})

	It("refuses a call whose chat selected nothing, with several connections", func() {
		_, err := src.Credential(inChat(newChat(session.ChatRecord{Title: "none"})), actor, "github", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeSelectConnection))
	})
	It("uses the connection the chat selected, and only its resource list", func() {
		id := newChat(session.ChatRecord{Title: "sel", Connections: map[string]string{"github": home}})
		grant, err := src.Credential(inChat(id), actor, "github", "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.Credential).To(Equal("token-home"))
		Expect(grant.BindingID).To(Equal(home))
		Expect(grant.BindingName).To(Equal("Home"))
		_, err = src.Credential(inChat(id), actor, "github", "other/repo")
		Expect(refusal(err)).To(Equal(gateway.CodeResourceNotAllowed))
	})
	It("lets the broker name the connection of a proposal over the chat's choice", func() {
		id := newChat(session.ChatRecord{Title: "sel2", Connections: map[string]string{"github": home}})
		grant, err := src.Credential(gateway.WithConnection(inChat(id), work), actor, "github", "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.BindingID).To(Equal(work))
		_, err = src.Credential(gateway.WithConnection(ctx, "no-such-binding"), actor, "github", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeNoCredential))
	})
	It("refuses a connection that belongs to someone else", func() {
		other := credentialSource{app}
		_, err := other.Credential(gateway.WithConnection(ctx, work), contract.Actor{UserID: "mallory", CredentialID: "chats", Mode: "named-user"}, "github", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeNoCredential))
	})
	It("refuses a call from no chat when there are several connections, and names no first", func() {
		_, err := src.Credential(ctx, actor, "github", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeSelectConnection))
	})
	It("refuses a chat of another person", func() {
		id := newChat(session.ChatRecord{Title: "mine", Connections: map[string]string{"github": work}})
		bad := hostops.WithBinding(ctx, hostops.Binding{Principal: "mallory", ChatID: id})
		_, err := src.Credential(bad, contract.Actor{UserID: "mallory", CredentialID: "chats", Mode: "named-user"}, "github", "acme/widgets")
		Expect(refusal(err)).To(Equal(gateway.CodeSelectConnection))
	})
})
