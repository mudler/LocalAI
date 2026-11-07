package connectors_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

var _ = Describe("the OAuth records", func() {
	It("seals each record type with its own additional data, so a moved value fails to open", func() {
		key, _ := connectors.NewKey()
		s, _ := connectors.NewSealer(key)
		aads := []string{
			connectors.AAD("b1", "u1"),
			connectors.GrantAAD("b1", "u1", "providerA"),
			connectors.GrantAAD("b2", "u1", "providerA"),
			connectors.GrantAAD("b1", "u2", "providerA"),
			connectors.GrantAAD("b1", "u1", "providerB"),
			connectors.OAuthClientAAD("providerA"),
			connectors.OAuthClientAAD("providerB"),
			connectors.LoginStateAAD("h1", "b1", "u1"),
			connectors.LoginStateAAD("h2", "b1", "u1"),
		}
		for i, aad := range aads {
			sealed, err := s.Seal(aad, []byte("value"))
			Expect(err).NotTo(HaveOccurred())
			for j, other := range aads {
				_, err := s.Open(other, sealed)
				if i == j {
					Expect(err).NotTo(HaveOccurred())
				} else {
					Expect(err).To(HaveOccurred(), "%q opened as %q", aad, other)
				}
			}
		}
	})

	It("round trips the secrets of a grant through the sealer", func() {
		key, _ := connectors.NewKey()
		s, _ := connectors.NewSealer(key)
		aad := connectors.GrantAAD("b1", "u1", "providerA")
		sealed, err := s.SealGrant(aad, connectors.GrantSecrets{Access: "access-sentinel", Refresh: "refresh-sentinel", TokenType: "Bearer"})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(sealed)).NotTo(ContainSubstring("sentinel"))
		got, err := s.OpenGrant(aad, sealed)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(connectors.GrantSecrets{Access: "access-sentinel", Refresh: "refresh-sentinel", TokenType: "Bearer"}))
		_, err = s.OpenGrant(connectors.GrantAAD("b2", "u1", "providerA"), sealed)
		Expect(err).To(HaveOccurred())
	})

	It("validates the auth kind of a binding", func() {
		b := connectors.Binding{Name: "n", Kind: "k", AgentID: "a"}
		for _, k := range []string{"", connectors.AuthToken, connectors.AuthOAuth2} {
			b.AuthKind = k
			Expect(connectors.Validate(b)).To(Succeed(), k)
		}
		b.AuthKind = "password"
		Expect(connectors.Validate(b)).NotTo(Succeed())
		b.AuthKind, b.Token = connectors.AuthOAuth2, []byte("sealed")
		Expect(connectors.Validate(b)).NotTo(Succeed(), "an oauth2 binding holds no token of its own")
	})

	It("asks for a usable credential of the binding's kind before it matches a resource", func() {
		res := connectors.NewResources([]string{"acme/widgets"})
		tok := connectors.Binding{ID: "t", Token: []byte("sealed"), Resources: res}
		_, err := entryOf(tok, "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		tok.Token = nil
		_, err = entryOf(tok, "acme/widgets")
		Expect(errors.Is(err, connectors.ErrNoCredential)).To(BeTrue())

		oauth := connectors.Binding{ID: "o", AuthKind: connectors.AuthOAuth2, Resources: res}
		_, err = entryOf(oauth, "acme/widgets")
		Expect(errors.Is(err, connectors.ErrNoCredential)).To(BeTrue(), "an oauth2 binding is usable only with its grant")
		e, err := connectors.EntryUsable(oauth, true, "ACME/widgets", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(e.Key).To(Equal("acme/widgets"))
		_, err = connectors.EntryUsable(oauth, false, "", false)
		Expect(errors.Is(err, connectors.ErrNoCredential)).To(BeTrue())
		var na *connectors.NotAllowedError
		_, err = connectors.EntryUsable(oauth, true, "other/repo", false)
		Expect(errors.As(err, &na)).To(BeTrue())
	})
})
