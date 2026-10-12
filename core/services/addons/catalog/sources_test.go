// SPDX-License-Identifier: MIT
package catalog

import (
	"encoding/json"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"os"
)

var _ = Describe("source configuration", func() {
	It("requires authoritative administration for all source edits", func() {
		s := setup()
		src := Source{ID: "one", Name: "One", URL: "https://example.org", Enabled: true}
		for _, actor := range []contract.Actor{{}, {UserID: "admin", Mode: "local-owner"}} {
			Expect(s.Save(ctx, actor, src)).To(MatchError(ErrForbidden))
		}
		s.admin = nil
		Expect(s.Save(ctx, admin, src)).To(MatchError(ErrForbidden))
	})
	It("persists protected references and removes sources without touching installs", func() {
		s := setup()
		save(s, "one", "https://example.org")
		info, e := os.Stat(s.path)
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
		loaded, e := NewSources(s.path, checker, CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		Expect(loaded.sources).To(HaveKey("one"))
		sentinel := s.path + ".installed"
		Expect(os.WriteFile(sentinel, []byte("pinned-source"), 0600)).To(Succeed())
		Expect(s.Save(ctx, admin, Source{ID: "one", Removed: true})).To(Succeed())
		Expect(s.sources).NotTo(HaveKey("one"))
		b, e := os.ReadFile(sentinel)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal("pinned-source"))
	})
	It("invalidates cached releases on explicit source changes", func() {
		server := library(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode([]Release{release()}) })
		s := setup()
		save(s, "one", server.URL)
		_, e := s.Refresh(ctx, "one")
		Expect(e).NotTo(HaveOccurred())
		save(s, "one", "https://example.org")
		_, e = (&Catalog{Sources: s}).Resolve(ctx, "one", "tables", "1.0.0")
		Expect(e).To(HaveOccurred())
	})
	It("rejects secret URLs invalid digests duplicate identities and malformed catalogs", func() {
		s := setup()
		Expect(s.Save(ctx, admin, Source{ID: "one", Name: "one", URL: "https://user:secret@example.org"})).To(HaveOccurred())
		for _, body := range []string{`[{"addonId":"tables"}]`, `[] trailing`, `{"secret":"value"}`} {
			server := library(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) })
			save(s, "one", server.URL)
			_, e := s.Refresh(ctx, "one")
			Expect(e).To(MatchError(ErrRefresh))
		}
		server := library(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]Release{release(), release()})
		})
		save(s, "one", server.URL)
		_, e := s.Refresh(ctx, "one")
		Expect(e).To(HaveOccurred())
	})
})
