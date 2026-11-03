package studio_test

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The credential a tool call of an add-on gets: the token of one of the
// caller's own bindings, and only for a resource that the binding lists.
var _ = Describe("connector credentials for tool calls", func() {
	ctx := context.Background()

	It("opens the token of the binding that lists the resource", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		first := r.bind(studio.ConnectorInput{Name: "Alpha", Token: "token-alpha", Resources: []string{"Acme/Widgets"}})
		second := r.bind(studio.ConnectorInput{Name: "Beta", Token: "token-beta", Resources: []string{"acme/tools", "acme/widgets"}})
		Expect(first.Resources).To(HaveLen(1))

		grant, err := r.conns.CredentialFor(ctx, alice, "telegram", "acme/WIDGETS")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.Secret).To(Equal("token-alpha"), "the first binding in list order wins")
		Expect(grant.Resource.Key).To(Equal("Acme/Widgets"))
		Expect(grant.Resource.ID).To(Equal(connectors.ResourceID("acme/widgets")))

		grant, err = r.conns.CredentialFor(ctx, alice, "telegram", "acme/tools")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.Secret).To(Equal("token-beta"))
		Expect(second.Resources).To(HaveLen(2))
	})

	It("refuses a resource on no list, naming what is allowed", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		r.bind(studio.ConnectorInput{Token: "t", Resources: []string{"acme/widgets"}})
		_, err := r.conns.CredentialFor(ctx, alice, "telegram", "evil/repo")
		var na *connectors.NotAllowedError
		Expect(errors.As(err, &na)).To(BeTrue())
		Expect(na.Allowed).To(Equal([]string{"acme/widgets"}))
	})

	It("refuses every resource when the list is empty", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		r.bind(studio.ConnectorInput{Token: "t"})
		_, err := r.conns.CredentialFor(ctx, alice, "telegram", "acme/widgets")
		var na *connectors.NotAllowedError
		Expect(errors.As(err, &na)).To(BeTrue())
		Expect(na.Allowed).To(BeEmpty())
	})

	It("never lends the binding of another user or of another add-on", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		r.bind(studio.ConnectorInput{Token: "t", Resources: []string{"acme/widgets"}})
		_, err := r.conns.CredentialFor(ctx, "mallory", "telegram", "acme/widgets")
		Expect(err).To(MatchError(connectors.ErrNoCredential))
		_, err = r.conns.CredentialFor(ctx, alice, "github", "acme/widgets")
		Expect(err).To(MatchError(connectors.ErrNoCredential))
	})

	It("keeps, replaces and clears the list through the binding service, and never returns the token", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		b := r.bind(studio.ConnectorInput{Token: "t", Resources: []string{" a/b ", "A/B", "c/d"}})
		Expect(connectors.ResourceKeys(b.Resources)).To(Equal([]string{"a/b", "c/d"}))
		in := studio.ConnectorInput{Name: "Support bot", AgentID: b.AgentID, Enabled: true}
		got, err := r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(connectors.ResourceKeys(got.Resources)).To(Equal([]string{"a/b", "c/d"}), "an update without the field keeps the list")
		Expect(got.Token).To(BeEmpty())
		in.Resources = []string{"e/f"}
		got, err = r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(connectors.ResourceKeys(got.Resources)).To(Equal([]string{"e/f"}))
		in.Resources = []string{}
		got, err = r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Resources).To(BeEmpty())
		in.Resources = []string{"has space"}
		_, err = r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).To(MatchError(connectors.ErrInvalid))
	})
})
