package plugins_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// recordConns records what the Plugins service asks of the connectors.
type recordConns struct {
	mu    sync.Mutex
	calls []string
	last  []plugins.RemoteServer
}

func (r *recordConns) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, s)
}

func (r *recordConns) SyncPluginConnectors(_ context.Context, principal, installID, plugin, digest string, servers []plugins.RemoteServer) error {
	r.mu.Lock()
	r.last = servers
	r.mu.Unlock()
	r.add("sync " + principal + " " + installID + " " + plugin)
	return nil
}

func (r *recordConns) DropPluginConnectors(_ context.Context, principal, installID string, all bool) error {
	if all {
		r.add("drop-all " + installID)
	} else {
		r.add("drop " + principal + " " + installID)
	}
	return nil
}

func (r *recordConns) ResyncPluginConnectors(_ context.Context, installID, plugin, digest string, _ []plugins.RemoteServer) error {
	r.add("resync " + installID)
	return nil
}

func (r *recordConns) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

var _ = Describe("the MCP servers of a plugin", func() {
	digest := "sha256:" + strings.Repeat("ab", 32)
	It("gives a connector to each http and sse server, and says why the others get none", func() {
		GinkgoT().Setenv("LEAK_TOKEN", "from-the-server")
		m := plugins.Model{Name: "fmt", MCPServers: []plugins.MCPRef{
			{Name: "docs", Type: "http", URL: "https://mcp.example/${CLAUDE_PLUGIN_ROOT}/x", Headers: map[string]string{"X-Mode": "${MODE:-fast}"}},
			{Name: "events", Type: "sse", URL: "https://mcp.example/sse"},
			{Name: "leak", Type: "http", URL: "https://mcp.example/", Headers: map[string]string{"Authorization": "Bearer ${LEAK_TOKEN}"}},
			{Name: "keyed", Type: "http", URL: "https://mcp.example/${user_config.region}"},
			{Name: "socket", Type: "ws", URL: "wss://mcp.example/"},
			{Name: "local", Type: "stdio", Command: "node"},
		}}
		servers, skipped := plugins.RemoteServers(m, digest)
		Expect(servers).To(HaveLen(2))
		Expect(servers[0].Name).To(Equal("docs"))
		Expect(servers[0].URL).To(Equal("https://mcp.example//workspace/shared-plugins/" + strings.Repeat("ab", 32) + "/x"))
		Expect(servers[0].Headers).To(Equal(map[string]string{"X-Mode": "fast"}))
		Expect(servers[1].Name).To(Equal("events"))
		Expect(skipped).To(ConsistOf(
			plugins.SkippedServer{Name: "leak", Reason: plugins.ServerNeedsSettings},
			plugins.SkippedServer{Name: "keyed", Reason: plugins.ServerNeedsSettings},
			plugins.SkippedServer{Name: "socket", Reason: plugins.ServerNotSupported},
			plugins.SkippedServer{Name: "local", Reason: plugins.ServerLocal},
		))
		for _, s := range servers {
			for _, v := range s.Headers {
				Expect(v).NotTo(ContainSubstring("from-the-server"), "the server's environment reached a plugin")
			}
		}
	})

	It("gives connectors to at most MaxPluginConnectors servers of one plugin", func() {
		m := plugins.Model{Name: "many"}
		for i := 0; i < plugins.MaxPluginConnectors+2; i++ {
			m.MCPServers = append(m.MCPServers, plugins.MCPRef{Name: fmt.Sprintf("s%d", i), Type: "http", URL: "https://mcp.example/"})
		}
		servers, skipped := plugins.RemoteServers(m, digest)
		Expect(servers).To(HaveLen(plugins.MaxPluginConnectors))
		Expect(skipped).To(HaveLen(2))
		Expect(skipped[0].Reason).To(Equal(plugins.ServerOverLimit))
	})

	Describe("the connectors of each user", func() {
		var (
			ctx   context.Context
			st    store.Store
			svc   *plugins.Service
			conns *recordConns
		)
		BeforeEach(func() {
			ctx = context.Background()
			var err error
			st, err = store.NewFileStore("")
			Expect(err).NotTo(HaveOccurred())
			conns = &recordConns{}
			svc, err = plugins.New(plugins.Options{Store: st, Fetcher: newFakeFetcher(), Connectors: conns})
			Expect(err).NotTo(HaveOccurred())
			m, _ := json.Marshal(plugins.Model{Name: "fmt", MCPServers: []plugins.MCPRef{{Name: "docs", Type: "http", URL: "https://mcp.example/"}}})
			Expect(st.PutPackage(ctx, store.Package{Digest: digest, Name: "fmt", Model: m})).To(Succeed())
			Expect(st.InstallTx(ctx, func(tx store.Tx) error {
				return tx.CreateInstall(store.Install{ID: "in1", PluginID: "fmt@m", Scope: store.ScopeInstance, Digest: digest, State: store.StateInstalled})
			})).To(Succeed())
		})

		It("makes them when a user enables the plugin and removes them when the user disables it", func() {
			Expect(svc.SetEnabled(ctx, plugins.Actor{ID: "alice"}, "in1", true)).To(Succeed())
			Expect(conns.seen()).To(Equal([]string{"sync alice in1 fmt"}))
			Expect(conns.last).To(Equal([]plugins.RemoteServer{{Name: "docs", URL: "https://mcp.example/"}}))
			Expect(svc.SetEnabled(ctx, plugins.Actor{ID: "alice"}, "in1", false)).To(Succeed())
			Expect(conns.seen()).To(Equal([]string{"sync alice in1 fmt", "drop alice in1"}))
		})

		It("removes every user's when the install goes", func() {
			Expect(svc.Remove(ctx, plugins.Actor{ID: "root", Admin: true}, "in1")).To(Succeed())
			Expect(conns.seen()).To(Equal([]string{"drop-all in1"}))
		})
	})
})
