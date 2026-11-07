package studio_test

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A kind may opt in to wildcard resources and to guarded tokens. The tokens
// below are made up.
var _ = Describe("wildcard resources and guarded tokens of a connector kind", func() {
	ctx := context.Background()
	const classic, fine = "ghp_FAKEFAKEFAKEFAKEFAKE", "github_pat_FAKEFAKEFAKE"
	var r *connRig

	newRig := func(wildcards bool) {
		bot := newScriptedBot()
		addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
		addon.Kind.AgentOptional = true
		addon.Kind.WildcardResources = wildcards
		addon.Kind.Options = []contract.ConnectorOption{{ID: "classicToken", Title: "Classic tokens", Default: "refuse",
			Values: []contract.ConnectorOptionValue{{ID: "refuse", Title: "Refuse"}, {ID: "allow", Title: "Allow"}}}}
		addon.Kind.TokenGuards = []contract.TokenGuard{{Prefix: "ghp_", Option: "classicToken", Allow: "allow"}}
		r = newConnRigWith(newShared(), connStore(), studiobot.NewTransport(addon), connSay("never"), nil)
		r.bot, r.untrusted = bot, true
	}
	create := func(in studio.ConnectorInput) (studio.ConnectorInfo, error) {
		in.Kind, in.Name = "telegram", "Tokens"
		if in.Token == "" {
			in.Token = fine
		}
		return r.conns.CreateConnector(ctx, alice, in)
	}
	allowClassic := map[string]string{"classicToken": "allow"}

	Describe("the resource list", func() {
		It("takes owner/* without a confirmation and matches it as the host does", func() {
			newRig(true)
			b, err := create(studio.ConnectorInput{Resources: []string{"Acme/*"}})
			Expect(err).NotTo(HaveOccurred())
			g, err := r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "acme/widgets")
			Expect(err).NotTo(HaveOccurred())
			Expect(g.Resource.Key).To(Equal("acme/widgets"))
			for _, key := range []string{"other-owner/x", "acme-evil/x", "acme", "acme/a/b", "*", "acme/*"} {
				_, err = r.conns.CredentialFor(ctx, alice, "telegram", b.ID, key)
				var na *connectors.NotAllowedError
				Expect(errors.As(err, &na)).To(BeTrue(), key)
			}
		})

		It("needs the confirmation for *, and write mode stays independent", func() {
			newRig(true)
			_, err := create(studio.ConnectorInput{Resources: []string{"*"}})
			Expect(err).To(MatchError(connectors.ErrInvalid))
			Expect(err).To(MatchError(ContainSubstring("confirm_any_resource")))
			_, err = create(studio.ConnectorInput{Resources: []string{"*"}, ConfirmAnyResource: "yes"})
			Expect(err).To(MatchError(connectors.ErrInvalid))
			b, err := create(studio.ConnectorInput{Resources: []string{"*"}, ConfirmAnyResource: studio.AnyResourceConfirmation})
			Expect(err).NotTo(HaveOccurred())
			Expect(b.Writes()).To(Equal(connectors.WriteOff))
			g, err := r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "any/repo")
			Expect(err).NotTo(HaveOccurred())
			Expect(g.WriteMode).To(Equal(connectors.WriteOff))
			g, err = r.conns.CredentialFor(ctx, alice, "telegram", b.ID, contract.AnyResource)
			Expect(err).NotTo(HaveOccurred())
			Expect(g.Resource.Key).To(Equal("*"))
		})

		It("asks again when an update adds * but not when it keeps it", func() {
			newRig(true)
			b, err := create(studio.ConnectorInput{Resources: []string{"acme/x"}})
			Expect(err).NotTo(HaveOccurred())
			up := studio.ConnectorInput{Name: "Tokens", Resources: []string{"*"}}
			_, err = r.conns.UpdateConnector(ctx, alice, b.ID, up)
			Expect(err).To(MatchError(ContainSubstring("confirm_any_resource")))
			up.ConfirmAnyResource = studio.AnyResourceConfirmation
			_, err = r.conns.UpdateConnector(ctx, alice, b.ID, up)
			Expect(err).NotTo(HaveOccurred())
			up.ConfirmAnyResource = ""
			_, err = r.conns.UpdateConnector(ctx, alice, b.ID, up)
			Expect(err).NotTo(HaveOccurred(), "a binding that has * keeps it")
		})

		It("refuses partial stars, and * with other entries", func() {
			newRig(true)
			for _, list := range [][]string{{"acme/wid*"}, {"*/x"}, {"a/*/b"}, {"*", "acme/x"}} {
				_, err := create(studio.ConnectorInput{Resources: list, ConfirmAnyResource: studio.AnyResourceConfirmation})
				Expect(err).To(MatchError(connectors.ErrInvalid), "%v", list)
			}
		})

		It("is exact-match only for a kind that did not opt in", func() {
			newRig(false)
			b, err := create(studio.ConnectorInput{Resources: []string{"acme/*"}})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "acme/widgets")
			var na *connectors.NotAllowedError
			Expect(errors.As(err, &na)).To(BeTrue())
		})
	})

	Describe("a guarded token", func() {
		It("is refused unless the connection allows it, and the fine-grained one is unaffected", func() {
			newRig(true)
			_, err := create(studio.ConnectorInput{Token: classic, Resources: []string{"acme/x"}})
			Expect(err).To(MatchError(connectors.ErrInvalid))
			Expect(err.Error()).NotTo(ContainSubstring(classic))
			_, err = create(studio.ConnectorInput{Token: fine, Resources: []string{"acme/x"}})
			Expect(err).NotTo(HaveOccurred())
		})

		It("needs the option and the confirmation together", func() {
			newRig(true)
			_, err := create(studio.ConnectorInput{Token: classic, Resources: []string{"acme/x"}, Options: allowClassic})
			Expect(err).To(MatchError(ContainSubstring("confirm_wider_token")))
			b, err := create(studio.ConnectorInput{Token: classic, Resources: []string{"acme/x"}, Options: allowClassic, ConfirmWiderToken: studio.WiderTokenConfirmation})
			Expect(err).NotTo(HaveOccurred())
			g, err := r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "acme/x")
			Expect(err).NotTo(HaveOccurred())
			Expect(g.Secret).To(Equal(classic))
			// The repository list is still the host's: nothing else is reachable.
			_, err = r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "acme/y")
			Expect(err).To(HaveOccurred())
		})

		It("stops a stored classic token when the option is switched off", func() {
			newRig(true)
			b, err := create(studio.ConnectorInput{Token: classic, Resources: []string{"acme/x"}, Options: allowClassic, ConfirmWiderToken: studio.WiderTokenConfirmation})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Name: "Tokens", Options: map[string]string{"classicToken": "refuse"}})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "acme/x")
			Expect(err).To(MatchError(studio.ErrTokenGuarded))
		})

		It("is refused when an update swaps in a classic token without the option", func() {
			newRig(true)
			b, err := create(studio.ConnectorInput{Resources: []string{"acme/x"}})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Name: "Tokens", Token: classic})
			Expect(err).To(MatchError(connectors.ErrInvalid))
		})

		It("does not ask again for a connection that already allows it", func() {
			newRig(true)
			b, err := create(studio.ConnectorInput{Token: classic, Resources: []string{"acme/x"}, Options: allowClassic, ConfirmWiderToken: studio.WiderTokenConfirmation})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Name: "Tokens", Token: "ghp_ANOTHERFAKEFAKEFAKE", Options: allowClassic})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
