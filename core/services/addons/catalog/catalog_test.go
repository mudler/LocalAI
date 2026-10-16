// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
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
				_ = WriteIndex(w, []Release{release()})
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
			c := Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
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
	It("lists only releases the configured target platform can install", func() {
		other := release()
		other.OS, other.Arch = "windows", "arm64"
		onlyOther := release()
		onlyOther.AddonID, onlyOther.Name = "notes", "Notes"
		onlyOther.OS, onlyOther.Arch = "windows", "arm64"
		server := library(func(w http.ResponseWriter, r *http.Request) {
			_ = WriteIndex(w, []Release{release(), other, onlyOther})
		})
		s := setup()
		save(s, "main", server.URL)
		_, e := s.Refresh(ctx, "main")
		Expect(e).NotTo(HaveOccurred())
		c := Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
		page, e := c.Search(ctx, Query{})
		Expect(e).NotTo(HaveOccurred())
		Expect(page.Total).To(Equal(1))
		Expect(page.Releases).To(HaveLen(1))
		Expect(page.Releases[0].OS + "/" + page.Releases[0].Arch).To(Equal("linux/amd64"))
		// Everything listed resolves, and what is not listed does not.
		_, e = c.Resolve(ctx, "main", "tables", "1.0.0")
		Expect(e).NotTo(HaveOccurred())
		_, e = c.Resolve(ctx, "main", "notes", "1.0.0")
		Expect(e).To(MatchError(ErrNotFound))
		// The same cache serves another owner target its own entries.
		w := Catalog{Sources: s, TargetOS: "windows", TargetArch: "arm64"}
		page, e = w.Search(ctx, Query{})
		Expect(e).NotTo(HaveOccurred())
		Expect(page.Total).To(Equal(2))
		// Paging counts the filtered set.
		page, e = w.Search(ctx, Query{Limit: 1, Offset: 1})
		Expect(e).NotTo(HaveOccurred())
		Expect(page.Total).To(Equal(2))
		Expect(page.Releases).To(HaveLen(1))
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
		server := library(func(w http.ResponseWriter, r *http.Request) { _ = WriteIndex(w, []Release{release()}) })
		s := setup()
		c := Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
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
			_ = WriteIndex(w, []Release{release()})
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

var _ = Describe("target platform resolution", func() {
	It("resolves the host platform from a multi platform catalog", func() {
		host := release()
		host.OS, host.Arch = runtime.GOOS, runtime.GOARCH
		other := host
		other.Arch = "arm64"
		if host.Arch == other.Arch {
			other.Arch = "amd64"
		}
		server := library(func(w http.ResponseWriter, r *http.Request) { _ = WriteIndex(w, []Release{host, other}) })
		s := setup()
		save(s, "one", server.URL)
		_, e := s.Refresh(ctx, "one")
		Expect(e).NotTo(HaveOccurred())
		got, e := (Catalog{Sources: s}).Resolve(ctx, "one", "tables", "1.0.0")
		Expect(e).NotTo(HaveOccurred())
		Expect(got.OS).To(Equal(host.OS))
		Expect(got.Arch).To(Equal(host.Arch))
	})
})

var _ = Describe("owner target override", func() {
	It("selects exact configured owner targets and rejects absent or ambiguous targets", func() {
		amd := release()
		arm := amd
		arm.Arch = "arm64"
		arm.Digest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		server := library(func(w http.ResponseWriter, r *http.Request) { _ = WriteIndex(w, []Release{amd, arm}) })
		s := setup()
		save(s, "one", server.URL)
		_, e := s.Refresh(ctx, "one")
		Expect(e).NotTo(HaveOccurred())
		for _, want := range []Release{amd, arm} {
			got, err := (Catalog{Sources: s, TargetOS: want.OS, TargetArch: want.Arch}).Resolve(ctx, "one", "tables", "1.0.0")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.OS).To(Equal(want.OS))
			Expect(got.Arch).To(Equal(want.Arch))
			Expect(got.Digest).To(Equal(want.Digest))
		}
		for _, c := range []Catalog{{Sources: s, TargetOS: "linux", TargetArch: "riscv64"}, {Sources: s, TargetOS: "darwin", TargetArch: "arm64"}} {
			_, e := c.Resolve(ctx, "one", "tables", "1.0.0")
			Expect(e).To(MatchError(ErrNotFound))
		}
		// Even a corrupt cache must not resolve an ambiguous exact target.
		s.mu.Lock()
		cache := s.cache["one"]
		cache.releases = append(cache.releases, cache.releases[0])
		s.cache["one"] = cache
		s.mu.Unlock()
		_, e = (Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}).Resolve(ctx, "one", "tables", "1.0.0")
		Expect(e).To(MatchError(ErrInvalid))
	})
})

var _ = Describe("package references", func() {
	It("pins channels and restricts package transport to secure or opted in loopback origins", func() {
		for _, tc := range []struct {
			artifact     string
			allow, valid bool
		}{{"https://example.org/p.tar", false, true}, {"http://example.org/p.tar", true, false}, {"http://127.0.0.1:8080/p.tar", false, false}, {"http://127.0.0.1:8080/p.tar", true, true}, {"file:///tmp/p.tar", true, false}, {"https://user:secret@example.org/p.tar", false, false}} {
			r := release()
			r.Artifact = tc.artifact
			r.Channel = "development"
			server := library(func(w http.ResponseWriter, _ *http.Request) { _ = WriteIndex(w, []Release{r}) })
			s := setup()
			Expect(s.Save(ctx, admin, Source{ID: "test", Name: "Test", URL: server.URL, Enabled: true, AllowLoopbackHTTP: tc.allow})).To(Succeed())
			_, e := s.Refresh(ctx, "test")
			if !tc.valid {
				Expect(e).To(HaveOccurred())
				continue
			}
			Expect(e).NotTo(HaveOccurred())
			c := Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
			got, e := c.Resolve(ctx, "test", "tables", "development")
			Expect(e).NotTo(HaveOccurred())
			Expect(got.Channel).To(Equal("development"))
			Expect(got.Selection).To(Equal("development"))
			Expect(got.Version).To(Equal("1.0.0"))
			Expect(got.Digest).To(Equal(r.Digest))
		}
	})
})

var _ = Describe("catalog display metadata", func() {
	entries := func(mutate func(*Release)) []Release {
		plain := release()
		app := release()
		app.AddonID, app.Name = "notes", "Notes"
		app.UI = EntryUI{Title: "Notes", Description: "Keep notes.", MainPage: "home", Icon: "ui/icon.png", LaunchMode: "always"}
		if mutate != nil {
			mutate(&app)
		}
		return []Release{plain, app}
	}
	refresh := func(releases []Release) (Catalog, error) {
		server := library(func(w http.ResponseWriter, r *http.Request) { _ = WriteIndex(w, releases) })
		s := setup()
		save(s, "main", server.URL)
		_, e := s.Refresh(ctx, "main")
		return Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}, e
	}
	It("surfaces optional UI metadata in search results and resolutions without changing plain entries", func() {
		c, e := refresh(entries(nil))
		Expect(e).NotTo(HaveOccurred())
		page, e := c.Search(ctx, Query{})
		Expect(e).NotTo(HaveOccurred())
		Expect(page.Releases).To(HaveLen(2))
		byID := map[string]Release{}
		for _, r := range page.Releases {
			byID[r.AddonID] = r
		}
		Expect(byID["tables"].UI).To(Equal(EntryUI{}))
		Expect(byID["notes"].UI).To(Equal(EntryUI{Title: "Notes", Description: "Keep notes.", MainPage: "home", Icon: "ui/icon.png", LaunchMode: "always"}))
		r, e := c.Resolve(ctx, "main", "notes", "1.0.0")
		Expect(e).NotTo(HaveOccurred())
		Expect(r.UI).To(Equal(byID["notes"].UI))
		// A plain entry keeps its wire form: no empty ui object appears.
		b, e := json.Marshal(byID["tables"])
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).NotTo(ContainSubstring(`"ui"`))
	})
	It("rejects entries with remote icons, reserved launch modes, markup or overlong display text", func() {
		for name, mutate := range map[string]func(*Release){
			"https icon":     func(r *Release) { r.UI.Icon = "https://cdn.example/i.png" },
			"scheme-less":    func(r *Release) { r.UI.Icon = "//cdn.example/i.png" },
			"data icon":      func(r *Release) { r.UI.Icon = "data:image/png;base64,AAAA" },
			"absolute icon":  func(r *Release) { r.UI.Icon = "/etc/passwd.png" },
			"traversal icon": func(r *Release) { r.UI.Icon = "../i.png" },
			"svg icon":       func(r *Release) { r.UI.Icon = "ui/icon.svg" },
			"on-demand":      func(r *Release) { r.UI.LaunchMode = "on-demand" },
			"unknown mode":   func(r *Release) { r.UI.LaunchMode = "sometimes" },
			"markup title":   func(r *Release) { r.UI.Title = "<img src=x onerror=alert(1)>" },
			"long title":     func(r *Release) { r.UI.Title = strings.Repeat("t", 81) },
			"long text":      func(r *Release) { r.UI.Description = strings.Repeat("d", 281) },
			"control text":   func(r *Release) { r.UI.Description = "a\nb" },
			"bad main page":  func(r *Release) { r.UI.MainPage = "../home" },
		} {
			_, e := refresh(entries(mutate))
			Expect(e).To(MatchError(ErrRefresh), name)
		}
		// One bad entry never leaves the others served as if it were fine.
		c, e := refresh(entries(func(r *Release) { r.UI.Icon = "https://cdn.example/i.png" }))
		Expect(e).To(HaveOccurred())
		_, e = c.Search(ctx, Query{})
		Expect(e).To(HaveOccurred())
	})
})
