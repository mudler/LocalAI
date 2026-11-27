package mcpconns_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

var _ = Describe("connectors that a plugin owns", func() {
	It("makes one per server, updates it when the package changes, and writes nothing otherwise", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		servers := []mcpconns.PluginServer{{Name: "docs", URL: f.URL, Headers: map[string]string{"X-Team": "blue"}}}
		Expect(e.svc.SyncPlugin(ctx, "alice", "in-1", "fmt", "sha256:a", servers)).To(Succeed())
		list, err := e.svc.List(ctx, "alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		c := list[0]
		Expect(c.Name).To(Equal("fmt:docs"))
		Expect(c.PluginInstallID).To(Equal("in-1"))
		Expect(c.PluginServer).To(Equal("docs"))
		Expect(c.HasHeaders).To(BeTrue())
		Expect(c.Secrets).To(BeEmpty(), "the API never sees the sealed headers")
		raw, _ := e.store.Get(ctx, c.ID)
		Expect(string(raw.Secrets)).NotTo(ContainSubstring("blue"), "the headers are sealed at rest")

		tools, missing := e.svc.Resolve(ctx, "alice", []string{c.ID}, false)
		Expect(missing).To(BeEmpty())
		Expect(tools).NotTo(BeEmpty())
		f.mu.Lock()
		Expect(f.headers).To(ContainElement("blue"))
		f.mu.Unlock()

		e.advance(1)
		Expect(e.svc.SyncPlugin(ctx, "alice", "in-1", "fmt", "sha256:a", servers)).To(Succeed())
		same, _ := e.store.Get(ctx, c.ID)
		Expect(same.Updated.Equal(raw.Updated)).To(BeTrue(), "the same package writes nothing")

		g := newFakeServer(0)
		e.advance(1)
		Expect(e.svc.SyncPlugin(ctx, "alice", "in-1", "fmt", "sha256:b", []mcpconns.PluginServer{{Name: "docs", URL: g.URL}})).To(Succeed())
		moved, _ := e.store.Get(ctx, c.ID)
		Expect(moved.URL).To(Equal(g.URL), "an update that changes the address updates the connector")
		Expect(moved.HasHeaders).To(BeFalse())
		Expect(moved.ID).To(Equal(c.ID))

		Expect(e.svc.SyncPlugin(ctx, "alice", "in-1", "fmt", "sha256:c", nil)).To(Succeed())
		list, _ = e.svc.List(ctx, "alice")
		Expect(list).To(BeEmpty(), "a server the plugin dropped goes")
	})

	It("cannot be edited, switched or deleted by its owner", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		Expect(e.svc.SyncPlugin(ctx, "alice", "in-1", "fmt", "sha256:a", []mcpconns.PluginServer{{Name: "docs", URL: f.URL}})).To(Succeed())
		list, _ := e.svc.List(ctx, "alice")
		id := list[0].ID
		_, err := e.svc.Update(ctx, "alice", id, httpInput("mine", f.URL))
		Expect(err).To(MatchError(mcpconns.ErrPluginOwned))
		Expect(err).To(MatchError(mcpconns.ErrForbidden))
		_, err = e.svc.SetEnabled(ctx, "alice", id, false)
		Expect(err).To(MatchError(mcpconns.ErrPluginOwned))
		Expect(e.svc.Delete(ctx, "alice", id)).To(MatchError(mcpconns.ErrPluginOwned))
	})

	It("drops the connectors of one user or of every user", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		srv := []mcpconns.PluginServer{{Name: "docs", URL: f.URL}}
		Expect(e.svc.SyncPlugin(ctx, "alice", "in-1", "fmt", "sha256:a", srv)).To(Succeed())
		Expect(e.svc.SyncPlugin(ctx, "bob", "in-1", "fmt", "sha256:a", srv)).To(Succeed())
		_, err := e.svc.Create(ctx, "alice", httpInput("Mine", f.URL))
		Expect(err).NotTo(HaveOccurred())
		Expect(e.svc.DropPlugin(ctx, "alice", "in-1", false)).To(Succeed())
		a, _ := e.svc.List(ctx, "alice")
		Expect(a).To(HaveLen(1))
		Expect(a[0].Name).To(Equal("Mine"), "the user's own connector stays")
		b, _ := e.svc.List(ctx, "bob")
		Expect(b).To(HaveLen(1))
		Expect(e.svc.DropPlugin(ctx, "", "in-1", true)).To(Succeed())
		b, _ = e.svc.List(ctx, "bob")
		Expect(b).To(BeEmpty())
	})

	It("refuses a private address unless the administrator allows it, and skips a user without MCP", func() {
		e := newEnv()
		ctx := specCtx()
		e.private.Store(false)
		err := e.svc.SyncPlugin(ctx, "alice", "in-1", "fmt", "sha256:a", []mcpconns.PluginServer{{Name: "docs", URL: "http://127.0.0.1:9/mcp"}})
		Expect(err).To(MatchError(mcpconns.ErrInvalid))
		list, _ := e.svc.List(ctx, "alice")
		Expect(list).To(BeEmpty())

		e.private.Store(true)
		e.permit.Store(false)
		Expect(e.svc.SyncPlugin(ctx, "alice", "in-1", "fmt", "sha256:a", []mcpconns.PluginServer{{Name: "docs", URL: "https://mcp.example/x"}})).To(Succeed())
		e.permit.Store(true)
		list, _ = e.svc.List(ctx, "alice")
		Expect(list).To(BeEmpty())
	})

	It("does not count against the user's own limit", func() {
		e := newEnv()
		ctx := specCtx()
		var srv []mcpconns.PluginServer
		for i := range 3 {
			srv = append(srv, mcpconns.PluginServer{Name: string(rune('a' + i)), URL: "https://mcp.example/" + string(rune('a'+i))})
		}
		Expect(e.svc.SyncPlugin(ctx, "alice", "in-1", "fmt", "sha256:a", srv)).To(Succeed())
		for i := range mcpconns.MaxPerUser {
			_, err := e.svc.Create(ctx, "alice", mcpconns.Input{Name: "c" + string(rune('A'+i)), Transport: mcpconns.TransportHTTP, URL: "https://mcp.example/u"})
			Expect(err).NotTo(HaveOccurred())
		}
	})
})
