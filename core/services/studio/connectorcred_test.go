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

	It("opens the token of the selected binding and nothing else", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		first := r.bind(studio.ConnectorInput{Name: "Alpha", Token: "token-alpha", Resources: []string{"Acme/Widgets"}})
		second := r.bind(studio.ConnectorInput{Name: "Beta", Token: "token-beta", Resources: []string{"acme/tools", "acme/widgets"}})

		grant, err := r.conns.CredentialFor(ctx, alice, "telegram", first.ID, "acme/WIDGETS")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.Secret).To(Equal("token-alpha"))
		Expect(grant.Resource.Key).To(Equal("Acme/Widgets"))
		Expect(grant.Resource.ID).To(Equal(connectors.ResourceID("acme/widgets")))
		Expect(grant.BindingID).To(Equal(first.ID))
		Expect(grant.BindingName).To(Equal("Alpha"))

		// Both bindings list acme/widgets; the selected one answers, not the first.
		grant, err = r.conns.CredentialFor(ctx, alice, "telegram", second.ID, "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.Secret).To(Equal("token-beta"))
		Expect(grant.BindingName).To(Equal("Beta"))

		// A resource that only the other binding lists is refused with the list of this one.
		_, err = r.conns.CredentialFor(ctx, alice, "telegram", first.ID, "acme/tools")
		var na *connectors.NotAllowedError
		Expect(errors.As(err, &na)).To(BeTrue())
		Expect(na.Allowed).To(Equal([]string{"Acme/Widgets"}))
	})

	It("never picks a binding: no selection is an error, whatever the person has", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		_, err := r.conns.CredentialFor(ctx, alice, "telegram", "", "acme/widgets")
		Expect(err).To(MatchError(connectors.ErrNoConnection))
		r.bind(studio.ConnectorInput{Token: "t", Resources: []string{"acme/widgets"}})
		_, err = r.conns.CredentialFor(ctx, alice, "telegram", "", "acme/widgets")
		Expect(err).To(MatchError(connectors.ErrNoConnection), "one binding is not picked here either: the caller selects it")
	})

	It("refuses a resource on the list of no resource, naming what is allowed", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		b := r.bind(studio.ConnectorInput{Token: "t", Resources: []string{"acme/widgets"}})
		_, err := r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "evil/repo")
		var na *connectors.NotAllowedError
		Expect(errors.As(err, &na)).To(BeTrue())
		Expect(na.Allowed).To(Equal([]string{"acme/widgets"}))
	})

	It("refuses every resource when the list is empty", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		b := r.bind(studio.ConnectorInput{Token: "t"})
		_, err := r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "acme/widgets")
		var na *connectors.NotAllowedError
		Expect(errors.As(err, &na)).To(BeTrue())
		Expect(na.Allowed).To(BeEmpty())
	})

	It("never lends the binding of another user or of another add-on", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		b := r.bind(studio.ConnectorInput{Token: "t", Resources: []string{"acme/widgets"}})
		_, err := r.conns.CredentialFor(ctx, "mallory", "telegram", b.ID, "acme/widgets")
		Expect(err).To(MatchError(connectors.ErrNoCredential))
		_, err = r.conns.CredentialFor(ctx, alice, "github", b.ID, "acme/widgets")
		Expect(err).To(MatchError(connectors.ErrNoCredential))
		_, err = r.conns.CredentialFor(ctx, alice, "telegram", "no-such-binding", "acme/widgets")
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

	It("defaults the write mode to off, keeps and changes it, and refuses an unknown one", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		b := r.bind(studio.ConnectorInput{Token: "t", Resources: []string{"a/b"}})
		Expect(b.Writes()).To(Equal(connectors.WriteOff))
		grant, err := r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "a/b")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.WriteMode).To(Equal(connectors.WriteOff))

		in := studio.ConnectorInput{Name: "Support bot", AgentID: b.AgentID, Enabled: true}
		mode := connectors.WritePropose
		in.WriteMode = &mode
		got, err := r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Writes()).To(Equal(connectors.WritePropose))
		in.WriteMode = nil
		got, err = r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Writes()).To(Equal(connectors.WritePropose), "an update without the field keeps the mode")
		grant, err = r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "a/b")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.WriteMode).To(Equal(connectors.WritePropose))

		bad := "always"
		in.WriteMode = &bad
		_, err = r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).To(MatchError(connectors.ErrInvalid))
	})
})

// A call that names no resource gets the one resource of the connection: the
// connection is the only source of the target and of the login.
var _ = Describe("the default resource of a connection", func() {
	ctx := context.Background()

	It("gives the token and the only listed resource to a call that names none", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		b := r.bind(studio.ConnectorInput{Name: "Home", Token: "token-home", Resources: []string{"http://ha.local:8123"}})
		grant, err := r.conns.DefaultCredentialFor(ctx, alice, "telegram", b.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.Secret).To(Equal("token-home"))
		Expect(grant.Resource.Key).To(Equal("http://ha.local:8123"))
		Expect(grant.Resource.ID).To(Equal(connectors.ResourceID("http://ha.local:8123")))
		Expect(grant.BindingID).To(Equal(b.ID))
	})

	It("names the listed resources when the connection lists several or none", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		many := r.bind(studio.ConnectorInput{Name: "Many", Token: "t", Resources: []string{"http://b.local:8123", "http://a.local:8123"}})
		_, err := r.conns.DefaultCredentialFor(ctx, alice, "telegram", many.ID)
		var na *connectors.NotAllowedError
		Expect(errors.As(err, &na)).To(BeTrue())
		Expect(na.Allowed).To(Equal([]string{"http://a.local:8123", "http://b.local:8123"}))
		none := r.bind(studio.ConnectorInput{Name: "None", Token: "t"})
		_, err = r.conns.DefaultCredentialFor(ctx, alice, "telegram", none.ID)
		Expect(errors.As(err, &na)).To(BeTrue())
		Expect(na.Allowed).To(BeEmpty())
	})

	It("keeps the other checks: no selection, another user, a binding without a token", func() {
		r := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		b := r.bind(studio.ConnectorInput{Token: "t", Resources: []string{"http://ha.local:8123"}})
		_, err := r.conns.DefaultCredentialFor(ctx, alice, "telegram", "")
		Expect(err).To(MatchError(connectors.ErrNoConnection))
		_, err = r.conns.DefaultCredentialFor(ctx, "mallory", "telegram", b.ID)
		Expect(err).To(MatchError(connectors.ErrNoCredential))
		noToken := connectors.Binding{Resources: connectors.NewResources([]string{"http://ha.local:8123"})}
		_, err = connectors.OnlyEntryUsable(noToken, noToken.HasTokenCredential())
		Expect(err).To(MatchError(connectors.ErrNoCredential))
	})
})
