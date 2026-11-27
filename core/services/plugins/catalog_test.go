package plugins_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

var _ = Describe("Catalog, install views and the user state", func() {
	var (
		ctx   context.Context
		st    store.Store
		ff    *fakeFetcher
		repo  *pluginRepo
		queue *recordQueue
		svc   *plugins.Service
		carol = plugins.Actor{ID: "carol"}
	)
	const fmtURL = "https://github.com/o/fmt.git#"
	point := func(sha string) {
		ff.mu.Lock()
		defer ff.mu.Unlock()
		ff.refs[fmtURL] = sha
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		st, err = store.NewFileStore("")
		Expect(err).NotTo(HaveOccurred())
		ff = newFakeFetcher()
		repo = &pluginRepo{versions: map[string]map[string]string{}}
		ff.plugins["fmt"] = repo.fetch
		queue = &recordQueue{errs: map[string]error{}}
		clk := &testClock{now: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)}
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		files, err := jsonstore.New("", blobs, jsonstore.Options{Admin: "admin", Now: clk.Now})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = files.Close() })
		ws := content.Workspace{Files: files, Actor: workspace.Actor{Principal: "admin"}}
		svc, err = plugins.New(plugins.Options{Store: st, Fetcher: ff, Queue: queue, Now: clk.Now, Workspace: ws, Root: "/plugins"})
		Expect(err).NotTo(HaveOccurred())
		ff.setMarketplace("owner/team", marketplaceJSON("team",
			githubEntry("fmt", "o/fmt", ""),
			`{"name":"lint","description":"Lints Go code","category":"quality","tags":["go"],"source":{"source":"github","repo":"o/lint"}}`,
			`{"name":"pkg","source":{"source":"npm","package":"x"}}`,
		), shaA, nil)
		_, err = svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/team"})
		Expect(err).NotTo(HaveOccurred())
		repo.set(shaA, pluginFiles(textPlugin, "v1"))
		point(shaA)
	})

	install := func(a plugins.Actor, scope string) string {
		p, err := svc.Inspect(ctx, a, "fmt@team")
		Expect(err).NotTo(HaveOccurred())
		job, err := svc.Install(ctx, a, p.Token, scope)
		Expect(err).NotTo(HaveOccurred())
		Expect(queue.err(job)).NotTo(HaveOccurred())
		list, err := st.Installs(ctx, store.InstallFilter{PluginID: "fmt@team", Scope: scope})
		Expect(err).NotTo(HaveOccurred())
		Expect(list).NotTo(BeEmpty())
		return list[len(list)-1].ID
	}

	Describe("Catalog", func() {
		It("lists every entry of the active marketplaces with its plugin ID", func() {
			page, err := svc.Catalog(ctx, user, plugins.CatalogQuery{})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Total).To(Equal(3))
			Expect(page.Limit).To(Equal(plugins.DefaultCatalogLimit))
			Expect(page.Items[0].PluginID).To(Equal("fmt@team"))
			Expect(page.Items[0].RunsCode).To(BeNil(), "unknown before an install")
			Expect(page.Items[2].Supported).To(BeFalse())
		})

		It("filters by text, category, marketplace, and pages", func() {
			page, err := svc.Catalog(ctx, user, plugins.CatalogQuery{Q: "GO"})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Items).To(HaveLen(1))
			Expect(page.Items[0].Name).To(Equal("lint"))
			page, err = svc.Catalog(ctx, user, plugins.CatalogQuery{Category: "Quality"})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Items).To(HaveLen(1))
			page, err = svc.Catalog(ctx, user, plugins.CatalogQuery{Marketplace: "other"})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Items).To(BeEmpty())
			page, err = svc.Catalog(ctx, user, plugins.CatalogQuery{Offset: 1, Limit: 1})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Total).To(Equal(3))
			Expect(page.Items).To(HaveLen(1))
			Expect(page.Items[0].Name).To(Equal("lint"))
			page, err = svc.Catalog(ctx, user, plugins.CatalogQuery{Offset: 10})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Items).To(BeEmpty())
			_, err = svc.Catalog(ctx, user, plugins.CatalogQuery{Offset: -1})
			Expect(errors.Is(err, plugins.ErrInvalid)).To(BeTrue())
		})

		It("knows whether an installed plugin runs code and shows only the caller's installs", func() {
			id := install(user, store.ScopeUser)
			yes, no := true, false
			page, err := svc.Catalog(ctx, user, plugins.CatalogQuery{RunsCode: &no})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Items).To(HaveLen(1))
			Expect(page.Items[0].Installs).To(ConsistOf(plugins.InstallRef{ID: id, Scope: store.ScopeUser, State: store.StateInstalled}))
			page, err = svc.Catalog(ctx, user, plugins.CatalogQuery{RunsCode: &yes})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Items).To(BeEmpty())
			page, err = svc.Catalog(ctx, carol, plugins.CatalogQuery{RunsCode: &no})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Items).To(HaveLen(1))
			Expect(page.Items[0].Installs).To(BeEmpty(), "another user's install is not shown")
		})

		It("shows the last review of an entry before anyone installs it", func() {
			p, err := svc.Inspect(ctx, carol, "fmt@team")
			Expect(err).NotTo(HaveOccurred())
			it, err := svc.CatalogEntry(ctx, user, "team", "fmt")
			Expect(err).NotTo(HaveOccurred())
			Expect(it.RunsCode).To(HaveValue(BeFalse()))
			Expect(it.RunsCodeFrom).To(Equal(plugins.RunsCodeFromReview))
			Expect(it.Reviewed).NotTo(BeNil())
			Expect(it.Reviewed.SHA).To(Equal(p.SHA))
			Expect(it.Reviewed.Digest).To(Equal(p.Digest))
			Expect(it.Reviewed.Model.Skills).To(HaveLen(1))
			no := false
			page, err := svc.Catalog(ctx, user, plugins.CatalogQuery{RunsCode: &no})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Items).To(HaveLen(1), "a reviewed text-only entry passes the text-only filter")

			install(user, store.ScopeUser)
			it, err = svc.CatalogEntry(ctx, user, "team", "fmt")
			Expect(err).NotTo(HaveOccurred())
			Expect(it.RunsCodeFrom).To(Equal(plugins.RunsCodeFromInstall), "an install wins over a review")
		})

		It("takes runsCode of the curated marketplace as a hint, and of no other marketplace", func() {
			entry := func(name string, runs bool) string {
				b, _ := json.Marshal(map[string]any{"name": name, "source": map[string]any{"source": "github", "repo": "o/" + name},
					"metadata": map[string]any{"localai": map[string]any{"runsCode": runs}}})
				return string(b)
			}
			ff.setMarketplace(plugins.CuratedMarketplaceURL(""), marketplaceJSON("localai", entry("hooky", true), entry("texty", false)), "", nil)
			Expect(svc.Seed(ctx)).To(Succeed())
			Expect(st.SaveMarketplace(ctx, store.Marketplace{ID: plugins.DefaultMarketplaceID, Name: "localai", Kind: store.KindURL,
				Location: plugins.CuratedMarketplaceURL(""), Enabled: true, Default: true})).To(Succeed())
			_, err := svc.RefreshMarketplace(ctx, admin, plugins.DefaultMarketplaceID)
			Expect(err).NotTo(HaveOccurred())
			ff.setMarketplace("owner/third", marketplaceJSON("third", entry("claims", false)), shaA, nil)
			_, err = svc.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/third"})
			Expect(err).NotTo(HaveOccurred())

			it, err := svc.CatalogEntry(ctx, user, plugins.DefaultMarketplaceID, "hooky")
			Expect(err).NotTo(HaveOccurred())
			Expect(it.RunsCode).To(HaveValue(BeTrue()))
			Expect(it.RunsCodeFrom).To(Equal(plugins.RunsCodeFromCurated))
			it, err = svc.CatalogEntry(ctx, user, plugins.DefaultMarketplaceID, "texty")
			Expect(err).NotTo(HaveOccurred())
			Expect(it.RunsCode).To(HaveValue(BeFalse()))
			it, err = svc.CatalogEntry(ctx, user, "third", "claims")
			Expect(err).NotTo(HaveOccurred())
			Expect(it.RunsCode).To(BeNil(), "a third-party marketplace's own claim is not trusted")
		})

		It("leaves out a disabled marketplace", func() {
			off := false
			_, err := svc.UpdateMarketplace(ctx, admin, "team", plugins.UpdateMarketplaceRequest{Enabled: &off})
			Expect(err).NotTo(HaveOccurred())
			page, err := svc.Catalog(ctx, user, plugins.CatalogQuery{})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Items).To(BeEmpty())
			_, err = svc.CatalogEntry(ctx, user, "team", "fmt")
			Expect(errors.Is(err, plugins.ErrNotFound)).To(BeTrue())
		})

		It("returns one entry, and not found for an unknown one", func() {
			it, err := svc.CatalogEntry(ctx, user, "team", "lint")
			Expect(err).NotTo(HaveOccurred())
			Expect(it.PluginID).To(Equal("lint@team"))
			_, err = svc.CatalogEntry(ctx, user, "team", "nope")
			Expect(errors.Is(err, plugins.ErrNotFound)).To(BeTrue())
			_, err = svc.CatalogEntry(ctx, user, "nope", "lint")
			Expect(errors.Is(err, plugins.ErrNotFound)).To(BeTrue())
		})

		It("refuses a caller with no ID", func() {
			_, err := svc.Catalog(ctx, plugins.Actor{}, plugins.CatalogQuery{})
			Expect(err).To(MatchError(plugins.ErrForbidden))
		})
	})

	Describe("install views", func() {
		It("lists the instance installs and the caller's own, and all of them for an administrator", func() {
			mine := install(user, store.ScopeUser)
			list, err := svc.ListInstalls(ctx, user, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].ID).To(Equal(mine))
			Expect(list[0].Name).To(Equal("fmt"))
			Expect(list[0].Enabled).To(BeTrue(), "a user install is on at install")
			list, err = svc.ListInstalls(ctx, carol, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(BeEmpty())
			_, err = svc.ListInstalls(ctx, carol, true)
			Expect(err).To(MatchError(plugins.ErrForbidden))
			list, err = svc.ListInstalls(ctx, admin, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
		})

		It("gives the detail and the files of a visible install only", func() {
			id := install(user, store.ScopeUser)
			d, err := svc.GetInstall(ctx, user, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(d.Model.Skills).To(HaveLen(1))
			Expect(d.Files).NotTo(BeEmpty())
			r, err := svc.InstallFile(ctx, user, id, "commands/run.md")
			Expect(err).NotTo(HaveOccurred())
			body, err := io.ReadAll(r)
			Expect(r.Close()).To(Succeed())
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("Run $ARGUMENTS"))
			_, err = svc.GetInstall(ctx, carol, id)
			Expect(errors.Is(err, plugins.ErrNotFound)).To(BeTrue())
			_, err = svc.InstallFile(ctx, carol, id, "commands/run.md")
			Expect(errors.Is(err, plugins.ErrNotFound)).To(BeTrue())
			_, err = svc.InstallFile(ctx, user, id, "../etc/passwd")
			Expect(errors.Is(err, plugins.ErrInvalid)).To(BeTrue())
			_, err = svc.InstallFile(ctx, user, id, "missing.md")
			Expect(errors.Is(err, plugins.ErrNotFound)).To(BeTrue())
			_, err = svc.GetInstall(ctx, admin, id)
			Expect(err).NotTo(HaveOccurred())
		})

		It("opens a file of a preview for its principal, without using the token up", func() {
			p, err := svc.Inspect(ctx, user, "fmt@team")
			Expect(err).NotTo(HaveOccurred())
			for range 2 {
				r, err := svc.PreviewFile(ctx, user, p.Token, ".claude-plugin/plugin.json")
				Expect(err).NotTo(HaveOccurred())
				Expect(r.Close()).To(Succeed())
			}
			_, err = svc.PreviewFile(ctx, carol, p.Token, ".claude-plugin/plugin.json")
			Expect(err).To(MatchError(plugins.ErrTokenInvalid))
			_, err = svc.Install(ctx, user, p.Token, store.ScopeUser)
			Expect(err).NotTo(HaveOccurred(), "the token is still good")
		})
	})

	Describe("the user state", func() {
		on, off := true, false

		It("turns an instance install on and off for one user and keeps the settings", func() {
			id := install(admin, store.ScopeInstance)
			st0, err := svc.MyState(ctx, carol, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(st0.Enabled).To(BeFalse(), "an instance install starts off")
			s, err := svc.SetMyState(ctx, carol, id, plugins.SetMyStateRequest{Enabled: &on})
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Enabled).To(BeTrue())
			s, err = svc.SetMyState(ctx, carol, id, plugins.SetMyStateRequest{Enabled: &off})
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Enabled).To(BeFalse())
			other, err := svc.MyState(ctx, user, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(other.Enabled).To(BeFalse(), "the state is per user")
		})

		It("needs the explicit re-enable after an update added code", func() {
			id := install(admin, store.ScopeInstance)
			_, err := svc.SetMyState(ctx, carol, id, plugins.SetMyStateRequest{Enabled: &on})
			Expect(err).NotTo(HaveOccurred())
			Expect(st.MarkNeedsReenable(ctx, id)).To(Succeed())
			_, err = svc.SetMyState(ctx, carol, id, plugins.SetMyStateRequest{Enabled: &on})
			Expect(err).To(MatchError(plugins.ErrNeedsReenable))
			s, err := svc.SetMyState(ctx, carol, id, plugins.SetMyStateRequest{Enabled: &on, Reenable: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Enabled).To(BeTrue())
			Expect(s.NeedsReenable).To(BeFalse())
		})

		It("checks the settings against the plugin and refuses secrets", func() {
			files := pluginFiles(textPlugin, "v1")
			files[".claude-plugin/plugin.json"] = `{"name":"fmt","userConfig":{"width":{"type":"number","title":"Width","description":"Line width"},"token":{"type":"string","title":"Token","description":"API token","sensitive":true}}}`
			repo.set(shaB, files)
			point(shaB)
			id := install(user, store.ScopeUser)
			s, err := svc.SetMyState(ctx, user, id, plugins.SetMyStateRequest{Config: json.RawMessage(`{"width": 80}`)})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(s.Config)).To(MatchJSON(`{"width": 80}`))
			Expect(s.Enabled).To(BeTrue(), "a settings change keeps the switch")
			_, err = svc.SetMyState(ctx, user, id, plugins.SetMyStateRequest{Config: json.RawMessage(`{"nope": 1}`)})
			Expect(errors.Is(err, plugins.ErrInvalid)).To(BeTrue())
			_, err = svc.SetMyState(ctx, user, id, plugins.SetMyStateRequest{Config: json.RawMessage(`[1]`)})
			Expect(errors.Is(err, plugins.ErrInvalid)).To(BeTrue())
			_, err = svc.SetMyState(ctx, user, id, plugins.SetMyStateRequest{Config: json.RawMessage(`{"token": "x"}`)})
			Expect(errors.Is(err, plugins.ErrUnsupported)).To(BeTrue())
			_, err = svc.SetMyState(ctx, user, id, plugins.SetMyStateRequest{Secrets: map[string]string{"token": "x"}})
			Expect(errors.Is(err, plugins.ErrUnsupported)).To(BeTrue())
			s, err = svc.SetMyState(ctx, user, id, plugins.SetMyStateRequest{Config: json.RawMessage(`null`)})
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Config).To(BeEmpty())
		})

		It("keeps no state for another user's install", func() {
			id := install(user, store.ScopeUser)
			_, err := svc.MyState(ctx, carol, id)
			Expect(errors.Is(err, plugins.ErrNotFound)).To(BeTrue())
			_, err = svc.SetMyState(ctx, admin, id, plugins.SetMyStateRequest{Enabled: &on})
			Expect(errors.Is(err, plugins.ErrNotFound)).To(BeTrue(), "an administrator keeps no state on it either")
		})
	})
})
