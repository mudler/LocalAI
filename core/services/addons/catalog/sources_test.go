// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"encoding/json"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"os"
	"sync"
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
		server := library(func(w http.ResponseWriter, r *http.Request) { _ = WriteIndex(w, []Release{release()}) })
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
			server := library(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
			save(s, "one", server.URL)
			_, e := s.Refresh(ctx, "one")
			Expect(e).To(MatchError(ErrRefresh))
		}
		server := library(func(w http.ResponseWriter, r *http.Request) {
			_ = WriteIndex(w, []Release{release(), release()})
		})
		save(s, "one", server.URL)
		_, e := s.Refresh(ctx, "one")
		Expect(e).To(HaveOccurred())
	})
})

var _ = Describe("reserved add-on IDs", func() {
	It("refuses a library entry whose add-on ID is a host route segment", func() {
		s := setup()
		for _, id := range contract.ReservedAddonIDs() {
			r := release()
			r.AddonID = id
			server := library(func(w http.ResponseWriter, _ *http.Request) { _ = WriteIndex(w, []Release{r}) })
			save(s, "one", server.URL)
			_, e := s.Refresh(ctx, "one")
			Expect(e).To(MatchError(ErrRefresh), id)
		}
	})
})

var _ = Describe("source reads", func() {
	It("lists and gets protected source copies including disabled and empty sources after reload", func() {
		s := setup()
		empty := library(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("[]")) })
		store, err := credentials.Parse([]byte("- match: https://example.org\n  bearer: never-expose-this-secret"), nil)
		Expect(err).NotTo(HaveOccurred())
		s.credentials = CredentialResolver{References: map[string]CredentialBinding{"private-ref": {Origin: "https://example.org", Store: store}}}
		src := Source{ID: "disabled", Name: "Private", URL: "https://example.org", AuthReference: "private-ref", Policy: VerificationPolicy{RequireSignature: true, Issuer: "issuer", Identity: "identity"}}
		Expect(s.Save(ctx, admin, src)).To(Succeed())
		save(s, "empty", empty.URL)
		_, e := s.Refresh(ctx, "empty")
		Expect(e).NotTo(HaveOccurred())
		for _, reload := range []bool{false, true} {
			if reload {
				s, e = NewSources(s.path, checker, CredentialResolver{})
				Expect(e).NotTo(HaveOccurred())
			}
			list, e := s.List(ctx, admin)
			Expect(e).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(2))
			Expect(list[0]).To(Equal(src))
			encoded, err := json.Marshal(list)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(encoded)).NotTo(ContainSubstring("never-expose-this-secret"))
			list[0].Policy.Identity = "changed"
			list[0].AuthReference = "changed"
			got, e := s.Get(ctx, admin, "disabled")
			Expect(e).NotTo(HaveOccurred())
			Expect(got).To(Equal(src))
			got.Name = "changed"
			again, e := s.Get(ctx, admin, "disabled")
			Expect(e).NotTo(HaveOccurred())
			Expect(again).To(Equal(src))
			_, e = s.Get(ctx, admin, "missing")
			Expect(e).To(MatchError(ErrNotFound))
		}
	})
	It("requires current authoritative administration and context for source reads", func() {
		s := setup()
		save(s, "one", "https://example.org")
		for _, actor := range []contract.Actor{{}, {UserID: "admin", Mode: "local-owner"}} {
			list, e := s.List(ctx, actor)
			Expect(e).To(MatchError(ErrForbidden))
			Expect(list).To(BeNil())
			got, e := s.Get(ctx, actor, "one")
			Expect(e).To(MatchError(ErrForbidden))
			Expect(got).To(BeZero())
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, e := s.List(canceled, admin)
		Expect(e).To(MatchError(context.Canceled))
		_, e = s.Get(canceled, admin, "one")
		Expect(e).To(MatchError(context.Canceled))
		for _, check := range []AdminCheck{nil, func(context.Context, contract.Actor) error { return ErrForbidden }} {
			s.admin = check
			_, e := s.List(ctx, admin)
			Expect(e).To(MatchError(ErrForbidden))
			_, e = s.Get(ctx, admin, "one")
			Expect(e).To(MatchError(ErrForbidden))
		}
	})
	It("serializes source reads with concurrent configuration edits", func() {
		s := setup()
		save(s, "one", "https://example.org")
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				for j := 0; j < 10; j++ {
					save(s, "one", "https://example.org")
					list, e := s.List(ctx, admin)
					Expect(e).NotTo(HaveOccurred())
					Expect(list).To(HaveLen(1))
					list[0].Name = "private mutation"
					got, e := s.Get(ctx, admin, "one")
					Expect(e).NotTo(HaveOccurred())
					Expect(got.Name).To(Equal("one"))
				}
			}()
		}
		wg.Wait()
	})
})

var _ = Describe("what the update check reads", func() {
	serve := func(rs ...Release) *Sources {
		server := library(func(w http.ResponseWriter, r *http.Request) { _ = WriteIndex(w, rs) })
		s := setup()
		Expect(s.Save(ctx, admin, Source{ID: "lib", Name: "lib", URL: server.URL, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
		_, e := s.Refresh(ctx, "lib")
		Expect(e).NotTo(HaveOccurred())
		return s
	}
	It("keeps the compatibility hint of an entry and refuses a malformed range", func() {
		r := release()
		r.Requires = Requirements{Host: contract.VersionRange{Min: 1, Max: 2}, SDK: contract.VersionRange{Min: 1, Max: 1}}
		s := serve(r)
		got, e := (Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}).Resolve(ctx, "lib", "tables", "1.0.0")
		Expect(e).NotTo(HaveOccurred())
		Expect(got.Requires).To(Equal(r.Requires))
		for name, bad := range map[string]Requirements{
			"min zero":     {Host: contract.VersionRange{Min: 0, Max: 3}},
			"max under":    {API: contract.VersionRange{Min: 3, Max: 2}},
			"sdk inverted": {SDK: contract.VersionRange{Min: 2, Max: 1}},
		} {
			b := release()
			b.Requires = bad
			server := library(func(w http.ResponseWriter, _ *http.Request) { _ = WriteIndex(w, []Release{b}) })
			s := setup()
			Expect(s.Save(ctx, admin, Source{ID: "lib", Name: "lib", URL: server.URL, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
			_, e := s.Refresh(ctx, "lib")
			Expect(e).To(MatchError(ErrRefresh), name)
		}
		b, _ := json.Marshal(release())
		Expect(string(b)).NotTo(ContainSubstring("requires"))
	})
	It("lists the releases of one add-on for every platform, from the cache, and refuses a failed library", func() {
		other := release()
		other.OS = "plan9"
		unrelated := release()
		unrelated.AddonID = "notes"
		s := serve(release(), other, unrelated)
		c := Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
		list, e := c.Releases(ctx, "lib", "tables")
		Expect(e).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2))
		_, e = c.Releases(ctx, "absent", "tables")
		Expect(e).To(MatchError(ErrUnavailable))
		state, ok := s.State("lib")
		Expect(ok).To(BeTrue())
		Expect(state.Failed).To(BeFalse())
		Expect(state.LastSuccess).NotTo(BeZero())
		_, ok = s.State("absent")
		Expect(ok).To(BeFalse())
		// A failed fetch is not served as the old data, and keeps the last success.
		Expect(s.Save(ctx, admin, Source{ID: "lib", Name: "lib", URL: "http://127.0.0.1:1/none", Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
		_, e = s.Refresh(ctx, "lib")
		Expect(e).To(MatchError(ErrRefresh))
		_, e = c.Releases(ctx, "lib", "tables")
		Expect(e).To(MatchError(ErrRefresh))
		state, _ = s.State("lib")
		Expect(state.Failed).To(BeTrue())
	})
	It("names an unavailable credential reference as a credential failure inside the refresh failure", func() {
		server := library(func(w http.ResponseWriter, _ *http.Request) { _ = WriteIndex(w, []Release{release()}) })
		s := setup()
		s.credentials = CredentialResolver{References: map[string]CredentialBinding{"gone": {Origin: server.URL}}}
		Expect(s.Save(ctx, admin, Source{ID: "lib", Name: "lib", URL: server.URL, Enabled: true, AllowLoopbackHTTP: true, AuthReference: "gone"})).To(Succeed())
		_, e := s.Refresh(ctx, "lib")
		Expect(e).To(MatchError(ErrRefresh))
		Expect(e).To(MatchError(ErrCredential))
		Expect(e.Error()).NotTo(ContainSubstring(server.URL))
	})
})
