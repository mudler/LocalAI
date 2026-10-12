// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCatalog(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Addon catalog") }

var ctx = context.Background()
var admin = contract.Actor{UserID: "owner", CredentialID: "session", Mode: "named-user"}

func checker(_ context.Context, a contract.Actor) error {
	if a != admin {
		return fmt.Errorf("denied")
	}
	return nil
}
func release() Release {
	return Release{AddonID: "tables", Name: "Tables", Version: "1.0.0", Artifact: "oci://registry.example/tables", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", OS: "linux", Arch: "amd64"}
}
func library(handler http.HandlerFunc) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		handler(w, r)
	}))
	DeferCleanup(s.Close)
	return s
}
func setup() *Sources {
	s, e := NewSources(GinkgoT().TempDir()+"/sources.json", checker, CredentialResolver{})
	Expect(e).NotTo(HaveOccurred())
	return s
}
func save(s *Sources, id, url string) {
	Expect(s.Save(ctx, admin, Source{ID: id, Name: id, URL: url, Enabled: true})).To(Succeed())
}

var _ = Describe("private catalogs", func() {
	It("fetches private bearer and basic catalogs without OCI credentials", func() {
		for _, kind := range []string{"bearer", "basic"} {
			server := library(func(w http.ResponseWriter, r *http.Request) {
				if kind == "bearer" {
					Expect(r.Header.Get("Authorization")).To(Equal("Bearer secret"))
				} else {
					u, p, ok := r.BasicAuth()
					Expect(ok).To(BeTrue())
					Expect(u).To(Equal("user"))
					Expect(p).To(Equal("secret"))
				}
				json.NewEncoder(w).Encode([]Release{release()})
			})
			auth := "bearer: secret"
			if kind == "basic" {
				auth = "username: user\n  password: secret"
			}
			store, e := credentials.Parse([]byte("- match: "+server.URL+"\n  allow_insecure: true\n  "+auth), nil)
			Expect(e).NotTo(HaveOccurred())
			resolver := CredentialResolver{References: map[string]CredentialBinding{"http": {Origin: server.URL, Store: store}, "oci": {Origin: "https://registry.example", Store: store}}}
			s := setup()
			s.credentials = resolver
			Expect(s.Save(ctx, admin, Source{ID: "private", Name: "Private", URL: server.URL, Enabled: true, AuthReference: "http"})).To(Succeed())
			_, e = s.Refresh(ctx, "private")
			Expect(e).NotTo(HaveOccurred())
			c := Catalog{Sources: s}
			r, e := c.Resolve(ctx, "private", "tables", "1.0.0")
			Expect(e).NotTo(HaveOccurred())
			Expect(r.SourceID).To(Equal("private"))
			Expect(r.Digest).To(Equal(release().Digest))
			_, e = resolver.Resolve(ctx, "oci", server.URL)
			Expect(e).To(HaveOccurred())
			b, e := json.Marshal(resolver)
			Expect(e).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("secret"))
		}
	})
	It("blocks redirects without leaking credentials or error details", func() {
		hits := 0
		target := library(func(w http.ResponseWriter, r *http.Request) { hits++ })
		server := library(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL+"/?secret=hidden", 302) })
		store, e := credentials.Parse([]byte("- match: "+server.URL+"\n  allow_insecure: true\n  bearer: secret"), nil)
		Expect(e).NotTo(HaveOccurred())
		s := setup()
		s.credentials = CredentialResolver{References: map[string]CredentialBinding{"http": {Origin: server.URL, Store: store}}}
		Expect(s.Save(ctx, admin, Source{ID: "one", Name: "One", URL: server.URL, Enabled: true, AuthReference: "http"})).To(Succeed())
		_, e = s.Refresh(ctx, "one")
		Expect(e).To(MatchError(ErrRefresh))
		Expect(hits).To(BeZero())
		Expect(e.Error()).NotTo(ContainSubstring("hidden"))
	})
	It("keeps duplicate names source scoped and excludes disabled sources", func() {
		server := library(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode([]Release{release()}) })
		s := setup()
		c := Catalog{Sources: s}
		for _, id := range []string{"one", "two"} {
			save(s, id, server.URL)
			_, e := s.Refresh(ctx, id)
			Expect(e).NotTo(HaveOccurred())
		}
		page, e := c.Search(ctx, Query{Text: "tables"})
		Expect(e).NotTo(HaveOccurred())
		Expect(page.Releases).To(HaveLen(2))
		Expect(s.Save(ctx, admin, Source{ID: "one", Name: "one", URL: server.URL})).To(Succeed())
		page, e = c.Search(ctx, Query{})
		Expect(e).NotTo(HaveOccurred())
		Expect(page.Releases).To(HaveLen(1))
		_, e = c.Resolve(ctx, "one", "tables", "1.0.0")
		Expect(e).To(HaveOccurred())
		_, e = s.Refresh(ctx, "one")
		Expect(e).To(HaveOccurred())
	})
	It("reports refresh failures rather than serving stale success", func() {
		fail := false
		server := library(func(w http.ResponseWriter, r *http.Request) {
			if fail {
				http.Error(w, "secret", 401)
				return
			}
			json.NewEncoder(w).Encode([]Release{release()})
		})
		s := setup()
		save(s, "one", server.URL)
		_, e := s.Refresh(ctx, "one")
		Expect(e).NotTo(HaveOccurred())
		fail = true
		result, e := s.Refresh(ctx, "one")
		Expect(e).To(MatchError(ErrRefresh))
		Expect(result.LastSuccess.IsZero()).To(BeFalse())
		_, e = (&Catalog{Sources: s}).Resolve(ctx, "one", "tables", "1.0.0")
		Expect(e).To(MatchError(ErrRefresh))
	})
})
