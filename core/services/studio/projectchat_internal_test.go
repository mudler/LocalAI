package studio

import (
	"fmt"

	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

func newInternalFiles() *files.Service {
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "workspace"), blobs, jsonstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = ws.Close() })
	return files.New(ws, files.WithBlobs(blobs))
}

var _ = Describe("project defaults and the home of a chat", func() {
	avail := availableSets{Skills: []string{"a", "b", "c"}, Addons: []string{"x", "y"}, MCPServers: []string{"crm", "mail"}}

	It("turns off every available skill the default does not list, and leaves the other kinds alone", func() {
		out := projectSwitches(projects.Defaults{Skills: []string{"a"}}, Switches{}, avail)
		Expect(out.SkillsOff).To(Equal([]string{"b", "c"}))
		Expect(out.AddonsOff).To(BeEmpty(), "an empty list is no override")
		Expect(out.McpOff).To(BeEmpty())
	})

	It("maps add-ons and MCP servers the same way", func() {
		out := projectSwitches(projects.Defaults{Addons: []string{"x"}, MCPServers: []string{"mail"}}, Switches{}, avail)
		Expect(out.AddonsOff).To(Equal([]string{"y"}))
		Expect(out.McpOff).To(Equal([]string{"crm"}))
	})

	It("lets an explicit request switch win", func() {
		out := projectSwitches(projects.Defaults{Skills: []string{"a"}}, Switches{SkillsOff: []string{"a"}}, avail)
		Expect(out.SkillsOff).To(Equal([]string{"a"}))
	})

	It("skips a default that names something not installed", func() {
		out := projectSwitches(projects.Defaults{Skills: []string{"a", "ghost"}}, Switches{}, avail)
		Expect(out.SkillsOff).To(Equal([]string{"b", "c"}))
		out = projectSwitches(projects.Defaults{Skills: []string{"ghost"}}, Switches{}, avail)
		Expect(out.SkillsOff).To(BeEmpty(), "nothing available is named, so nothing is overridden")
	})

	It("does not touch collections, and a project without connector defaults changes nothing", func() {
		in := Switches{BuiltinsOn: []string{"g"}}
		out := projectSwitches(projects.Defaults{Collections: []string{"k"}}, in, avail)
		Expect(out).To(Equal(in))
		withConns := availableSets{Connectors: []string{"c1", "c2"}, MCPServers: []string{"crm"}}
		out = projectSwitches(projects.Defaults{}, in, withConns)
		Expect(out).To(Equal(in))
	})

	Describe("connector defaults", func() {
		conns := availableSets{Connectors: []string{"c1", "c2", "c3"}, MCPServers: []string{"crm", "mail"}}

		It("switches off every enabled connector the default does not list", func() {
			out := projectSwitches(projects.Defaults{Connectors: []string{"c1"}}, Switches{}, conns)
			Expect(out.McpOff).To(Equal([]string{"connector:c2", "connector:c3"}))
		})

		It("skips a listed connector that is deleted or disabled", func() {
			out := projectSwitches(projects.Defaults{Connectors: []string{"gone"}}, Switches{}, conns)
			Expect(out.McpOff).To(BeEmpty(), "nothing listed is available: no override")
			out = projectSwitches(projects.Defaults{Connectors: []string{"c1", "gone"}}, Switches{}, conns)
			Expect(out.McpOff).To(Equal([]string{"connector:c2", "connector:c3"}))
		})

		It("merges servers and connectors in one list", func() {
			out := projectSwitches(projects.Defaults{MCPServers: []string{"mail"}, Connectors: []string{"c2"}}, Switches{}, conns)
			Expect(out.McpOff).To(ConsistOf("crm", "connector:c1", "connector:c3"))
		})

		It("keeps the server default when the request switched only a connector", func() {
			out := projectSwitches(projects.Defaults{MCPServers: []string{"mail"}, Connectors: []string{"c2"}},
				Switches{McpOff: []string{"connector:c9"}}, conns)
			Expect(out.McpOff).To(ConsistOf("connector:c9", "crm"))
		})

		It("keeps the connector default when the request switched only a server", func() {
			out := projectSwitches(projects.Defaults{MCPServers: []string{"mail"}, Connectors: []string{"c2"}},
				Switches{McpOff: []string{"mail"}}, conns)
			Expect(out.McpOff).To(ConsistOf("mail", "connector:c1", "connector:c3"))
		})

		It("truncates the project's entries, never the request's, at the switch limit", func() {
			var ids []string
			for i := 0; i < maxMCPSwitches+50; i++ {
				ids = append(ids, fmt.Sprintf("c%03d", i))
			}
			many := availableSets{Connectors: ids}
			req := []string{"mail", "crm"}
			out := projectSwitches(projects.Defaults{Connectors: []string{"c000"}}, Switches{McpOff: req}, many)
			Expect(out.McpOff).To(HaveLen(maxMCPSwitches))
			Expect(out.McpOff[:2]).To(Equal(req))
			Expect(out.McpOff[2]).To(Equal("connector:c001"), "project entries are cut in sorted order")
			Expect(out.McpOff).NotTo(ContainElement("connector:c000"))
			again := projectSwitches(projects.Defaults{Connectors: []string{"c000"}}, Switches{McpOff: req}, many)
			Expect(again.McpOff).To(Equal(out.McpOff))
		})

		It("leaves the request alone when both kinds were switched or nothing is available", func() {
			in := Switches{McpOff: []string{"mail", "connector:c9"}}
			out := projectSwitches(projects.Defaults{MCPServers: []string{"mail"}, Connectors: []string{"c2"}}, in, conns)
			Expect(out.McpOff).To(Equal(in.McpOff))
			out = projectSwitches(projects.Defaults{Connectors: []string{"c1"}}, Switches{}, availableSets{})
			Expect(out.McpOff).To(BeEmpty())
		})
	})

	It("reads a dangling project ID as no project, without an error", func() {
		f := newInternalFiles()
		ps, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		proj := projects.NewService(projects.Options{Store: ps, Files: f})
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Projects: proj})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		ctx := context.Background()
		p, err := proj.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
		Expect(err).NotTo(HaveOccurred())

		_, ok := svc.resolveHome(ctx, "alice", session.ChatRecord{ID: "c"})
		Expect(ok).To(BeFalse(), "no project")
		r, ok := svc.resolveHome(ctx, "alice", session.ChatRecord{ID: "c", ProjectID: p.ID})
		Expect(ok).To(BeTrue())
		Expect(r.Name).To(Equal("Acme"))
		_, ok = svc.resolveHome(ctx, "bob", session.ChatRecord{ID: "c", ProjectID: p.ID})
		Expect(ok).To(BeFalse(), "another owner's project")
		Expect(proj.Delete(ctx, "alice", p.ID)).To(Succeed())
		_, ok = svc.resolveHome(ctx, "alice", session.ChatRecord{ID: "c", ProjectID: p.ID})
		Expect(ok).To(BeFalse(), "a deleted project")
	})
})
