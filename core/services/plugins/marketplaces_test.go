package plugins_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// testClock is a settable clock.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) Advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.now = c.now.Add(d) }

const curated = "https://raw.githubusercontent.com/mudler/LocalAI/master/plugins/.claude-plugin/marketplace.json"

var admin = plugins.Actor{ID: "admin", Admin: true, HumanSurface: true}
var user = plugins.Actor{ID: "u1"}

func marketplaceJSON(name string, entries ...string) string {
	out := `{"name":"` + name + `","owner":{"name":"o"},"plugins":[`
	for i, e := range entries {
		if i > 0 {
			out += ","
		}
		out += e
	}
	return out + "]}"
}

func githubEntry(name, repo, sha string) string {
	if sha == "" {
		return fmt.Sprintf(`{"name":%q,"source":{"source":"github","repo":%q}}`, name, repo)
	}
	return fmt.Sprintf(`{"name":%q,"source":{"source":"github","repo":%q,"sha":%q}}`, name, repo, sha)
}

var _ = Describe("CuratedMarketplaceURL", func() {
	DescribeTable("reads the tag of a release and master otherwise",
		func(version, ref string) {
			Expect(plugins.CuratedMarketplaceURL(version)).To(Equal(
				"https://raw.githubusercontent.com/mudler/LocalAI/" + ref + "/plugins/.claude-plugin/marketplace.json"))
		},
		Entry("release", "v1.2.3", "v1.2.3"),
		Entry("empty version", "", "master"),
		Entry("commit after a tag", "v1.2.3-14-gabc1234", "master"),
		Entry("dev", "dev", "master"),
	)
})

var _ = Describe("ParseSource", func() {
	DescribeTable("accepts owner/repo and https",
		func(in, kind, loc string) {
			k, l, err := plugins.ParseSource(in)
			Expect(err).NotTo(HaveOccurred())
			Expect(k).To(Equal(kind))
			Expect(l).To(Equal(loc))
		},
		Entry("github", "owner/repo", store.KindGitHub, "owner/repo"),
		Entry("github with .git", "owner/repo.git", store.KindGitHub, "owner/repo"),
		Entry("git", "https://git.example.test/a/b.git", store.KindGit, "https://git.example.test/a/b.git"),
		Entry("json", "https://example.test/x/.claude-plugin/marketplace.json", store.KindURL, "https://example.test/x/.claude-plugin/marketplace.json"),
	)
	DescribeTable("refuses everything else",
		func(in string) {
			_, _, err := plugins.ParseSource(in)
			Expect(errors.Is(err, plugins.ErrInvalid)).To(BeTrue())
		},
		Entry("ssh", "ssh://example.test/a.git"),
		Entry("scp-like", "git@example.test:a/b.git"),
		Entry("http", "http://example.test/a.git"),
		Entry("file", "file:///srv/a.git"),
		Entry("local path", "/srv/a"),
		Entry("relative path", "./a"),
		Entry("credentials", "https://u:p@example.test/a.git"),
		Entry("empty", ""),
	)
})

var _ = Describe("ParseDefaults", func() {
	It("reads sources with and without an ID", func() {
		got, err := plugins.ParseDefaults("team=owner/repo, https://example.test/tools/.claude-plugin/marketplace.json,owner/other.git")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]plugins.DefaultMarketplace{
			{ID: "team", Kind: store.KindGitHub, Location: "owner/repo"},
			{ID: "tools", Kind: store.KindURL, Location: "https://example.test/tools/.claude-plugin/marketplace.json"},
			{ID: "other", Kind: store.KindGitHub, Location: "owner/other"},
		}))
	})
	It("refuses a bad source and a duplicate ID", func() {
		_, err := plugins.ParseDefaults("ssh://example.test/a.git")
		Expect(err).To(HaveOccurred())
		_, err = plugins.ParseDefaults("a/repo,b/repo")
		Expect(err).To(MatchError(ContainSubstring("used twice")))
	})
})

var _ = Describe("Marketplaces", func() {
	var (
		ctx      context.Context
		st       store.Store
		ff       *fakeFetcher
		clk      *testClock
		official bool
		svc      *plugins.Service
		newSvc   func(defaults []plugins.DefaultMarketplace) *plugins.Service
	)
	BeforeEach(func() {
		ctx = context.Background()
		var err error
		st, err = store.NewFileStore(filepath.Join(GinkgoT().TempDir(), "plugins.json"))
		Expect(err).NotTo(HaveOccurred())
		ff = newFakeFetcher()
		clk = &testClock{now: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)}
		official = true
		newSvc = func(defaults []plugins.DefaultMarketplace) *plugins.Service {
			s, err := plugins.New(plugins.Options{
				Store: st, Fetcher: ff, Now: clk.Now, Defaults: defaults,
				Official: func() bool { return official },
			})
			Expect(err).NotTo(HaveOccurred())
			return s
		}
		svc = newSvc(nil)
	})

	ids := func() []string {
		list, err := svc.ListMarketplaces(ctx)
		Expect(err).NotTo(HaveOccurred())
		var out []string
		for _, m := range list {
			out = append(out, m.ID)
		}
		return out
	}

	Describe("seeding", func() {
		It("adds the curated marketplace to a fresh store only", func() {
			Expect(svc.Seed(ctx)).To(Succeed())
			list, err := svc.ListMarketplaces(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].ID).To(Equal("localai"))
			Expect(list[0].Kind).To(Equal(store.KindURL))
			Expect(list[0].Location).To(Equal(curated))
			Expect(list[0].Default).To(BeTrue())
			Expect(list[0].Active).To(BeTrue())

			Expect(svc.UpdateMarketplace(ctx, admin, "localai", plugins.UpdateMarketplaceRequest{Enabled: new(false)})).Error().NotTo(HaveOccurred())
			Expect(svc.Seed(ctx)).To(Succeed())
			m, err := svc.Marketplace(ctx, "localai")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Enabled).To(BeFalse(), "seeding must not touch a store that has records")
		})

		It("seeds the flag's list instead of the curated one", func() {
			defs, err := plugins.ParseDefaults("team=owner/repo")
			Expect(err).NotTo(HaveOccurred())
			Expect(newSvc(defs).Seed(ctx)).To(Succeed())
			Expect(ids()).To(Equal([]string{"team"}))
		})

		It("does not add the flag's list to a store that has records", func() {
			Expect(svc.Seed(ctx)).To(Succeed())
			defs, err := plugins.ParseDefaults("team=owner/repo")
			Expect(err).NotTo(HaveOccurred())
			Expect(newSvc(defs).Seed(ctx)).To(Succeed())
			Expect(ids()).To(Equal([]string{"localai"}))
		})

		It("moves the curated marketplace to the tag of the new version after an upgrade", func() {
			at := func(version string) *plugins.Service {
				s, err := plugins.New(plugins.Options{Store: st, Fetcher: ff, Now: clk.Now, Version: version})
				Expect(err).NotTo(HaveOccurred())
				return s
			}
			Expect(at("v4.0.0").Seed(ctx)).To(Succeed())
			Expect(svc.UpdateMarketplace(ctx, admin, "localai", plugins.UpdateMarketplaceRequest{Enabled: new(false)})).Error().NotTo(HaveOccurred())
			Expect(at("v4.1.0").Seed(ctx)).To(Succeed())
			m, err := svc.Marketplace(ctx, "localai")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Location).To(Equal(plugins.CuratedMarketplaceURL("v4.1.0")))
			Expect(m.Enabled).To(BeFalse(), "only the location follows the version")
			Expect(at("").Seed(ctx)).To(Succeed())
			m, err = svc.Marketplace(ctx, "localai")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Location).To(Equal(curated), "a development build reads master")
		})

		It("leaves a removed curated marketplace and the flag's defaults as they are", func() {
			Expect(svc.Seed(ctx)).To(Succeed())
			Expect(svc.RemoveMarketplace(ctx, admin, "localai")).To(Succeed())
			s, err := plugins.New(plugins.Options{Store: st, Fetcher: ff, Now: clk.Now, Version: "v9.0.0"})
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Seed(ctx)).To(Succeed())
			all, err := st.Marketplaces(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(all[0].Removed).To(BeTrue())
			Expect(all[0].Location).To(Equal(curated))
		})

		It("keeps a removed default removed after a restart", func() {
			Expect(svc.Seed(ctx)).To(Succeed())
			Expect(svc.RemoveMarketplace(ctx, admin, "localai")).To(Succeed())
			restarted := newSvc(nil)
			Expect(restarted.Seed(ctx)).To(Succeed())
			list, err := restarted.ListMarketplaces(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(BeEmpty())
			all, err := st.Marketplaces(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(all).To(HaveLen(1))
			Expect(all[0].Removed).To(BeTrue())
		})
	})

	Describe("the official libraries switch", func() {
		It("suspends the defaults and restores them", func() {
			ff.setMarketplace(curated, marketplaceJSON("localai"), "", nil)
			Expect(svc.Seed(ctx)).To(Succeed())
			official = false
			m, err := svc.Marketplace(ctx, "localai")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Suspended).To(BeTrue())
			Expect(m.Active).To(BeFalse())
			Expect(m.Enabled).To(BeTrue(), "the stored choice stays")
			res, err := svc.RefreshTick(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Refreshed).To(BeEmpty())
			Expect(ff.count(curated)).To(Equal(0))

			official = true
			m, err = svc.Marketplace(ctx, "localai")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Active).To(BeTrue())
			res, err = svc.RefreshTick(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Refreshed).To(Equal([]string{"localai"}))
		})
	})

	Describe("adding", func() {
		It("refreshes at once and takes the ID from the marketplace name", func() {
			ff.setMarketplace("owner/repo", marketplaceJSON("Team_Tools", githubEntry("a", "o/a", shaA)), shaB, nil)
			m, err := svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/repo"})
			Expect(err).NotTo(HaveOccurred())
			Expect(m.ID).To(Equal("team-tools"))
			Expect(m.Name).To(Equal("Team_Tools"))
			Expect(m.HeadSHA).To(Equal(shaB))
			snap, err := st.Snapshot(ctx, "team-tools")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.SHA).To(Equal(shaB))
			Expect(snap.Entries).To(HaveLen(1))
		})

		It("is for administrators only", func() {
			_, err := svc.AddMarketplace(ctx, user, plugins.AddMarketplaceRequest{Source: "owner/repo"})
			Expect(err).To(MatchError(plugins.ErrForbidden))
			Expect(svc.RemoveMarketplace(ctx, user, "x")).To(MatchError(plugins.ErrForbidden))
			_, err = svc.RefreshMarketplace(ctx, user, "x")
			Expect(err).To(MatchError(plugins.ErrForbidden))
			_, err = svc.UpdateMarketplace(ctx, user, "x", plugins.UpdateMarketplaceRequest{})
			Expect(err).To(MatchError(plugins.ErrForbidden))
		})

		It("stores nothing when the first fetch fails", func() {
			ff.setMarketplace("owner/repo", "", "", errors.New("down"))
			_, err := svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/repo", ID: "team"})
			Expect(err).To(HaveOccurred())
			Expect(ids()).To(BeEmpty())
		})

		It("refuses a taken ID and the ID of a removed default", func() {
			Expect(svc.Seed(ctx)).To(Succeed())
			ff.setMarketplace("owner/repo", marketplaceJSON("team"), shaA, nil)
			_, err := svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/repo"})
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/repo"})
			Expect(errors.Is(err, store.ErrConflict)).To(BeTrue())
			Expect(svc.RemoveMarketplace(ctx, admin, "localai")).To(Succeed())
			_, err = svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/repo", ID: "localai"})
			Expect(err).To(MatchError(ContainSubstring("removed default")))
		})

		It("stops at 20 marketplaces", func() {
			for i := range plugins.MaxMarketplaces {
				src := fmt.Sprintf("owner/r%d", i)
				ff.setMarketplace(src, marketplaceJSON(fmt.Sprintf("m%d", i)), shaA, nil)
				_, err := svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: src})
				Expect(err).NotTo(HaveOccurred())
			}
			ff.setMarketplace("owner/last", marketplaceJSON("last"), shaA, nil)
			_, err := svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/last"})
			Expect(errors.Is(err, plugins.ErrLimit)).To(BeTrue())
		})

		It("refuses a ref on a JSON URL and a bad ref", func() {
			_, err := svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "https://example.test/m.json", Ref: "main"})
			Expect(errors.Is(err, plugins.ErrInvalid)).To(BeTrue())
			_, err = svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/repo", Ref: "--upload-pack=x"})
			Expect(errors.Is(err, plugins.ErrInvalid)).To(BeTrue())
		})
	})

	Describe("refreshing", func() {
		BeforeEach(func() {
			ff.setMarketplace("owner/repo", marketplaceJSON("team", githubEntry("a", "o/a", shaA)), shaA, nil)
			_, err := svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/repo"})
			Expect(err).NotTo(HaveOccurred())
		})

		It("keeps the old snapshot and sets last_error when the refresh fails", func() {
			ff.setMarketplace("owner/repo", "{not json", shaB, nil)
			m, err := svc.RefreshMarketplace(ctx, admin, "team")
			Expect(err).To(HaveOccurred())
			Expect(m.LastError).To(Equal(plugins.ErrCodeParse))
			Expect(m.HeadSHA).To(Equal(shaA))
			snap, err := st.Snapshot(ctx, "team")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.SHA).To(Equal(shaA))
			Expect(snap.Entries).To(HaveLen(1))

			ff.setMarketplace("owner/repo", "", "", errors.New("network down at https://secret.example.test"))
			m, err = svc.RefreshMarketplace(ctx, admin, "team")
			Expect(err).To(HaveOccurred())
			Expect(m.LastError).To(Equal(plugins.ErrCodeFetch))

			ff.setMarketplace("owner/repo", marketplaceJSON("team"), shaB, nil)
			m, err = svc.RefreshMarketplace(ctx, admin, "team")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.LastError).To(BeEmpty())
			Expect(m.HeadSHA).To(Equal(shaB))
		})

		It("lists npm, archive and command entries as not supported", func() {
			ff.setMarketplace("owner/repo", marketplaceJSON("team",
				githubEntry("ok", "o/ok", shaA),
				`{"name":"n","source":{"source":"npm","package":"x"}}`,
				`{"name":"z","source":{"source":"archive","url":"https://example.test/z.zip"}}`,
				`{"name":"c","source":{"source":"command","command":"make"}}`,
				`{"name":"bad","source":{"source":"github","repo":"nope"}}`,
			), shaB, nil)
			_, err := svc.RefreshMarketplace(ctx, admin, "team")
			Expect(err).NotTo(HaveOccurred())
			snap, err := st.Snapshot(ctx, "team")
			Expect(err).NotTo(HaveOccurred())
			supported := map[string]bool{}
			for _, e := range snap.Entries {
				supported[e.Name] = e.Supported
				if !e.Supported {
					Expect(e.Reason).NotTo(BeEmpty())
				}
			}
			Expect(supported).To(Equal(map[string]bool{"ok": true, "n": false, "z": false, "c": false}))
			Expect(snap.Skipped).To(ContainElement(HaveField("Name", "bad")))
		})

		It("lists a relative source of a url marketplace as not supported", func() {
			loc := "https://example.test/m/.claude-plugin/marketplace.json"
			ff.setMarketplace(loc, marketplaceJSON("urlm", `{"name":"r","source":"./r"}`), "", nil)
			_, err := svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: loc})
			Expect(err).NotTo(HaveOccurred())
			snap, err := st.Snapshot(ctx, "urlm")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.Entries[0].Supported).To(BeFalse())
		})

		It("keeps the snapshot of a url marketplace whose body did not change", func() {
			loc := "https://example.test/m/.claude-plugin/marketplace.json"
			ff.setMarketplace(loc, marketplaceJSON("urlm"), "", nil)
			_, err := svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: loc})
			Expect(err).NotTo(HaveOccurred())
			before, err := st.Snapshot(ctx, "urlm")
			Expect(err).NotTo(HaveOccurred())
			clk.Advance(time.Hour)
			_, err = svc.RefreshMarketplace(ctx, admin, "urlm")
			Expect(err).NotTo(HaveOccurred())
			after, err := st.Snapshot(ctx, "urlm")
			Expect(err).NotTo(HaveOccurred())
			Expect(after.FetchedAt).To(BeTemporally("==", before.FetchedAt))

			ff.setMarketplace(loc, marketplaceJSON("urlm", githubEntry("a", "o/a", shaA)), "", nil)
			_, err = svc.RefreshMarketplace(ctx, admin, "urlm")
			Expect(err).NotTo(HaveOccurred())
			after, err = st.Snapshot(ctx, "urlm")
			Expect(err).NotTo(HaveOccurred())
			Expect(after.BodySHA256).NotTo(Equal(before.BodySHA256))
			Expect(after.Entries).To(HaveLen(1))
		})

		It("refreshes a marketplace once per interval and again after a ref change", func() {
			clk.Advance(time.Minute)
			res, err := svc.RefreshTick(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Refreshed).To(BeEmpty(), "just added")
			clk.Advance(plugins.DefaultRefreshInterval)
			res, err = svc.RefreshTick(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Refreshed).To(Equal([]string{"team"}))
			res, err = svc.RefreshTick(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Refreshed).To(BeEmpty())

			_, err = svc.UpdateMarketplace(ctx, admin, "team", plugins.UpdateMarketplaceRequest{Ref: new("v2")})
			Expect(err).NotTo(HaveOccurred())
			res, err = svc.RefreshTick(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Refreshed).To(Equal([]string{"team"}))
		})

		It("skips disabled marketplaces", func() {
			_, err := svc.UpdateMarketplace(ctx, admin, "team", plugins.UpdateMarketplaceRequest{Enabled: new(false)})
			Expect(err).NotTo(HaveOccurred())
			clk.Advance(2 * plugins.DefaultRefreshInterval)
			res, err := svc.RefreshTick(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Refreshed).To(BeEmpty())
		})

		It("clears a blocked update when the entry goes back to the installed commit", func() {
			Expect(st.InstallTx(ctx, func(tx store.Tx) error {
				return tx.CreateInstall(store.Install{
					ID: "i1", PluginID: "a@team", Scope: store.ScopeUser, Owner: "u1", Digest: "sha256:" + shaA + shaA[:24],
					SourceSHA: shaA, State: store.StateBlocked, UpdateSHA: shaB,
				})
			})).To(Succeed())
			ff.setMarketplace("owner/repo", marketplaceJSON("team", githubEntry("a", "o/a", shaA)), shaC, nil)
			_, err := svc.RefreshMarketplace(ctx, admin, "team")
			Expect(err).NotTo(HaveOccurred())
			in, err := st.Installs(ctx, store.InstallFilter{ID: "i1"})
			Expect(err).NotTo(HaveOccurred())
			Expect(in[0].State).To(Equal(store.StateInstalled))
			Expect(in[0].UpdateSHA).To(BeEmpty())
		})

		It("marks an install with a newer version and clears the mark when the entry goes back", func() {
			Expect(st.InstallTx(ctx, func(tx store.Tx) error {
				return tx.CreateInstall(store.Install{
					ID: "i1", PluginID: "a@team", Scope: store.ScopeInstance, Digest: "sha256:" + shaA + shaA[:24],
					SourceSHA: shaA, State: store.StateInstalled,
				})
			})).To(Succeed())

			ff.setMarketplace("owner/repo", marketplaceJSON("team", githubEntry("a", "o/a", shaB)), shaB, nil)
			_, err := svc.RefreshMarketplace(ctx, admin, "team")
			Expect(err).NotTo(HaveOccurred())
			in, err := st.Installs(ctx, store.InstallFilter{ID: "i1"})
			Expect(err).NotTo(HaveOccurred())
			Expect(in[0].State).To(Equal(store.StateUpdateAvailable))
			Expect(in[0].UpdateSHA).To(Equal(shaB))
			Expect(in[0].SourceSHA).To(Equal(shaA), "a refresh never installs")

			ff.setMarketplace("owner/repo", marketplaceJSON("team", githubEntry("a", "o/a", shaA)), shaC, nil)
			_, err = svc.RefreshMarketplace(ctx, admin, "team")
			Expect(err).NotTo(HaveOccurred())
			in, err = st.Installs(ctx, store.InstallFilter{ID: "i1"})
			Expect(err).NotTo(HaveOccurred())
			Expect(in[0].State).To(Equal(store.StateInstalled))
			Expect(in[0].UpdateSHA).To(BeEmpty())
		})

		It("resolves the ref of an entry without a sha", func() {
			Expect(st.InstallTx(ctx, func(tx store.Tx) error {
				return tx.CreateInstall(store.Install{ID: "i2", PluginID: "b@team", Scope: store.ScopeInstance, SourceSHA: shaA, State: store.StateInstalled})
			})).To(Succeed())
			ff.mu.Lock()
			ff.refs["https://github.com/o/b.git#"] = shaC
			ff.mu.Unlock()
			ff.setMarketplace("owner/repo", marketplaceJSON("team", githubEntry("b", "o/b", "")), shaB, nil)
			_, err := svc.RefreshMarketplace(ctx, admin, "team")
			Expect(err).NotTo(HaveOccurred())
			in, err := st.Installs(ctx, store.InstallFilter{ID: "i2"})
			Expect(err).NotTo(HaveOccurred())
			Expect(in[0].UpdateSHA).To(Equal(shaC))
		})
	})

	It("removes a marketplace that is not a default", func() {
		ff.setMarketplace("owner/repo", marketplaceJSON("team"), shaA, nil)
		_, err := svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/repo"})
		Expect(err).NotTo(HaveOccurred())
		Expect(svc.RemoveMarketplace(ctx, admin, "team")).To(Succeed())
		all, err := st.Marketplaces(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(BeEmpty())
		_, err = st.Snapshot(ctx, "team")
		Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
		Expect(svc.RemoveMarketplace(ctx, admin, "team")).To(MatchError(plugins.ErrNotFound))
	})
})
