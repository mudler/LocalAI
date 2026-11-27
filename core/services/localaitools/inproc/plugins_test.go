package inproc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/nib/plugin/claudeplugin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/fetch"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

const pluginSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// pluginFetcher serves the marketplace "team" with a text-only plugin (fmt)
// and a plugin with a command hook (hook).
type pluginFetcher struct{}

func (pluginFetcher) FetchMarketplace(context.Context, string, string, string) ([]byte, string, error) {
	entry := func(name string) string {
		return fmt.Sprintf(`{"name":%q,"description":"The %s plugin","source":{"source":"github","repo":"o/%s","sha":%q}}`, name, name, name, pluginSHA)
	}
	return []byte(`{"name":"team","owner":{"name":"o"},"plugins":[` + entry("fmt") + "," + entry("hook") + `]}`), pluginSHA, nil
}

func (pluginFetcher) ResolveRef(context.Context, string, string) (string, error) {
	return pluginSHA, nil
}

func (pluginFetcher) FetchPlugin(_ context.Context, _ fetch.MarketplaceSource, _ *claudeplugin.Marketplace, e *claudeplugin.Entry, sha string) (*fetch.Fetched, error) {
	files := map[string]string{
		".claude-plugin/plugin.json":     `{"name": "` + e.Name + `", "version": "1.0.0"}`,
		"skills/" + e.Name + "/SKILL.md": "---\nname: " + e.Name + "\ndescription: Do it\n---\nBody",
	}
	if e.Name == "hook" {
		files["hooks/hooks.json"] = `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/check.sh"}]}]}}`
		files["check.sh"] = "#!/bin/sh\nexit 0\n"
	}
	dir := GinkgoT().TempDir()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return nil, err
		}
	}
	return &fetch.Fetched{Dir: dir, SHA: sha, Source: e.Source, Done: func() {}}, nil
}

type inlinePluginJobs struct{}

func (inlinePluginJobs) Enqueue(j plugins.Job) { _ = j.Run(context.Background()) }

func newPluginService() *plugins.Service {
	st, err := store.NewFileStore("")
	Expect(err).NotTo(HaveOccurred())
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	files, err := jsonstore.New("", blobs, jsonstore.Options{Admin: "admin"})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = files.Close() })
	svc, err := plugins.New(plugins.Options{
		Store: st, Fetcher: pluginFetcher{}, Queue: inlinePluginJobs{}, Root: "/plugins",
		Workspace: content.Workspace{Files: files, Actor: workspace.Actor{Principal: "admin"}},
	})
	Expect(err).NotTo(HaveOccurred())
	return svc
}

var _ = Describe("inproc.Client plugins", func() {
	var (
		c     *Client
		jobs  map[string]string
		admin = localaitools.WithUser(context.Background(), &auth.User{ID: "root", Role: auth.RoleAdmin})
		alice = localaitools.WithUser(context.Background(), &auth.User{ID: "alice", Role: auth.RoleUser})
	)
	BeforeEach(func() {
		svc := newPluginService()
		jobs = map[string]string{}
		c = &Client{Plugins: func() *plugins.Service { return svc }, PluginJobs: func(p, j string) { jobs[p] = j }}
	})

	It("runs every action with the rights of the acting user", func() {
		_, err := c.AddPluginMarketplace(alice, "owner/team", "", "")
		Expect(err).To(MatchError(plugins.ErrForbidden))
		m, err := c.AddPluginMarketplace(admin, "owner/team", "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(SatisfyAll(HaveField("ID", "team"), HaveField("HeadSHA", pluginSHA)))
		m, err = c.RefreshPluginMarketplace(admin, "team")
		Expect(err).NotTo(HaveOccurred())
		Expect(m.RefreshedAt).NotTo(BeNil())
		list, err := c.ListPluginMarketplaces(alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))

		page, err := c.BrowsePlugins(alice, localaitools.PluginBrowseQuery{Query: "fmt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Items).To(HaveLen(1))
		Expect(page.Items[0].Plugin).To(Equal("fmt@team"))

		res, err := c.InstallPlugin(alice, "fmt@team", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(SatisfyAll(HaveField("Plugin", "fmt@team"), HaveField("SHA", pluginSHA), HaveField("Skills", 1)))
		Expect(jobs).To(HaveKeyWithValue("fmt@team", res.JobID))

		installs, err := c.ListPluginInstalls(alice, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(installs).To(HaveLen(1))
		in := installs[0]
		Expect(in).To(SatisfyAll(HaveField("Scope", "user"), HaveField("Enabled", true), HaveField("RunsCode", false)))

		st, err := c.SetPluginEnabled(alice, in.ID, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Enabled).To(BeFalse())

		_, err = c.CheckPluginUpdate(alice, in.ID)
		Expect(err).To(MatchError(plugins.ErrNoUpdate))

		Expect(c.RemovePlugin(alice, in.ID)).To(Succeed())
		installs, err = c.ListPluginInstalls(alice, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(installs).To(BeEmpty())
	})

	It("refuses the install of a plugin that runs code with the browser-session error", func() {
		_, err := c.AddPluginMarketplace(admin, "owner/team", "", "")
		Expect(err).NotTo(HaveOccurred())
		_, err = c.InstallPlugin(admin, "hook@team", "instance")
		Expect(errors.Is(err, localaitools.ErrPluginNeedsBrowser)).To(BeTrue(), fmt.Sprint(err))
		Expect(err.Error()).To(SatisfyAll(ContainSubstring("hook@team runs code (hook-command)"), ContainSubstring(plugins.ReviewPath("hook@team"))))
		installs, err := c.ListPluginInstalls(admin, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(installs).To(BeEmpty())
	})

	It("needs an acting user with authentication on, and acts as the local owner without", func() {
		_, err := c.ListPluginMarketplaces(context.Background())
		Expect(err).To(MatchError(errNoActingUser))
		c.SingleUser = true
		_, err = c.AddPluginMarketplace(context.Background(), "owner/team", "", "")
		Expect(err).NotTo(HaveOccurred())
	})

	It("reports a server without plugins", func() {
		c.Plugins = nil
		_, err := c.ListPluginMarketplaces(admin)
		Expect(err).To(MatchError(errNoPlugins))
	})
})
