// SPDX-License-Identifier: MIT
package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func host() contract.HostCompatibility {
	return contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: 1, DataFormat: 1, FeatureLevel: contract.HostFeatureLevel, OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// manifestWith validates the Tables manifest after a change to its tool
// definitions.
func manifestWith(change func(m *contract.Manifest)) error {
	GinkgoHelper()
	m := tablesManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	// Work on copies so one case never changes another.
	defs := append([]contract.ToolDefinition(nil), m.Capabilities.ToolDefinitions...)
	m.Capabilities.ToolDefinitions = defs
	change(&m)
	return contract.ValidateManifest(m, host())
}

func schema(props string, required string, extra string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"object","additionalProperties":false,"properties":%s,"required":%s%s}`, props, required, extra))
}

var _ = Describe("Manifest tool definitions", func() {
	It("accepts the Tables definitions and classifies every model-facing tool", func() {
		Expect(manifestWith(func(*contract.Manifest) {})).To(Succeed())
		kinds := map[string]string{}
		for _, d := range tablesManifest().Capabilities.ToolDefinitions {
			kinds[d.Name] = d.Kind
		}
		Expect(kinds).To(Equal(map[string]string{
			"databases-create": "create", "databases-inspect": "read", "databases-rename": "schema",
			"tables-create": "schema", "tables-rename": "schema", "tables-drop": "schema",
			"columns-add": "schema", "columns-rename": "schema", "columns-drop": "schema",
			"rows-query": "read", "rows-aggregate": "read", "rows-export": "read", "rows-insert": "write", "rows-update": "write", "rows-delete": "write",
		}))
	})

	It("keeps the schema bytes of a parsed manifest equal to what the runtime echoes back", func() {
		// The supervisor compares the registration it sent with the one the
		// addon returns by deep equality. The echo is encoded by a JSON
		// encoder, which compacts raw schemas, so a manifest parsed from an
		// indented file must already hold the compact form.
		raw, err := os.ReadFile("../../../../addons/tables/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring("\n    "), "the packaged manifest is indented")
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		wire, err := json.Marshal(m.Capabilities)
		Expect(err).NotTo(HaveOccurred())
		var echoed contract.Capabilities
		Expect(json.Unmarshal(wire, &echoed)).To(Succeed())
		Expect(echoed).To(Equal(m.Capabilities))
		// An HTML-significant character in a description survives the same trip.
		m.Capabilities.ToolDefinitions[0].InputSchema = schema(`{"resourceId":{"type":"string","description":"a & b"}}`, `["resourceId"]`, "")
		withAmp, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		again, err := contract.ParseManifest(withAmp)
		Expect(err).NotTo(HaveOccurred())
		wire, _ = json.Marshal(again.Capabilities)
		Expect(json.Unmarshal(wire, &echoed)).To(Succeed())
		Expect(echoed).To(Equal(again.Capabilities))
	})

	It("rejects a definition that declares an identity, role, approval or operation property", func() {
		for _, name := range []string{"actor", "userId", "user_id", "role", "approved", "confirm", "digest", "proposalId", "operationId", "generation", "tool", "force"} {
			props := fmt.Sprintf(`{"resourceId":{"type":"string"},%q:{"type":"string"}}`, name)
			err := manifestWith(func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[0].InputSchema = schema(props, `["resourceId"]`, "")
			})
			Expect(err).To(HaveOccurred(), name)
		}
		// The same name nested in an object property is refused as well.
		err := manifestWith(func(m *contract.Manifest) {
			m.Capabilities.ToolDefinitions[0].InputSchema = schema(`{"resourceId":{"type":"string"},"filter":{"type":"object","properties":{"approved":{"type":"boolean"}}}}`, `["resourceId"]`, "")
		})
		Expect(err).To(HaveOccurred())
	})

	It("rejects an undeclared tool, a repeated tool, an unknown kind and a missing or unbounded schema", func() {
		cases := map[string]func(*contract.Manifest){
			"undeclared": func(m *contract.Manifest) { m.Capabilities.ToolDefinitions[0].Name = "databases-delete" },
			"repeated": func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[1].Name = m.Capabilities.ToolDefinitions[0].Name
			},
			"kind":  func(m *contract.Manifest) { m.Capabilities.ToolDefinitions[0].Kind = "approve" },
			"empty": func(m *contract.Manifest) { m.Capabilities.ToolDefinitions[0].InputSchema = nil },
			"not object": func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[0].InputSchema = json.RawMessage(`{"type":"string"}`)
			},
			"open schema": func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"]}`)
			},
			// A read tool may name no resource; a tool that changes data may not.
			"no resource on a write": func(m *contract.Manifest) {
				for i := range m.Capabilities.ToolDefinitions {
					if m.Capabilities.ToolDefinitions[i].Kind == "write" {
						m.Capabilities.ToolDefinitions[i].InputSchema = schema(`{"table":{"type":"string"}}`, `["table"]`, "")
					}
				}
			},
			"resource on create": func(m *contract.Manifest) {
				for i := range m.Capabilities.ToolDefinitions {
					if m.Capabilities.ToolDefinitions[i].Kind == "create" {
						m.Capabilities.ToolDefinitions[i].InputSchema = schema(`{"resourceId":{"type":"string"},"name":{"type":"string"}}`, `["resourceId","name"]`, "")
					}
				}
			},
			"ref": func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[0].InputSchema = schema(`{"resourceId":{"$ref":"#/x"}}`, `["resourceId"]`, "")
			},
			"too large": func(m *contract.Manifest) { m.Capabilities.ToolDefinitions[0].Description = strings.Repeat("a", 500) },
			"huge schema": func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[0].InputSchema = schema(`{"resourceId":{"type":"string","description":"`+strings.Repeat("a", 9000)+`"}}`, `["resourceId"]`, "")
			},
			"markup": func(m *contract.Manifest) { m.Capabilities.ToolDefinitions[0].Description = "<b>do it</b>" },
		}
		for name, change := range cases {
			Expect(manifestWith(change)).To(HaveOccurred(), name)
		}
	})
})

var _ = Describe("Tools for the add-on's own page", func() {
	ctx := context.Background()
	It("are never offered to a model and are unknown when a model calls them", func() {
		app := apps.App{ID: "sandbox", Name: "sandbox", Generation: 1, Enabled: true, State: "running", Tools: []string{"status", "set-backend"},
			ToolDefinitions: []contract.ToolDefinition{
				{Name: "status", Kind: contract.ToolKindRead, Description: "Say whether it works.", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
				{Name: "set-backend", Kind: contract.ToolKindRead, Description: "Choose the backend.", UIOnly: true, InputSchema: json.RawMessage(`{"type":"object","properties":{"backend":{"type":"string"}},"additionalProperties":false}`)},
			}}
		f := &fake{list: apps.List{Apps: []apps.App{app}}}
		for name, a := range map[string]*tools.ToolAdapter{"plain": tools.NewToolAdapter(f), "chats": tools.NewChatToolAdapter(f)} {
			defs, err := a.Discover(ctx, member)
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(defs).To(HaveLen(1), name)
			Expect(defs[0].Tool).To(Equal("status"), name)
			_, err = a.Execute(ctx, member, tools.ToolName("sandbox", "set-backend"), json.RawMessage(`{"backend":"host"}`))
			Expect(err).To(MatchError(tools.ErrUnknownTool), name)
		}
		Expect(f.calls()).To(BeZero())
	})
})

var _ = Describe("A tool that shadows a built-in chat tool", func() {
	ctx := context.Background()
	schema := json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)
	It("keeps its namespaced name and carries the built-in it shadows, only for a replaceable name", func() {
		app := apps.App{ID: "sandbox", Name: "sandbox", Generation: 1, Enabled: true, State: "running", Tools: []string{"bash", "write", "fetch"},
			ToolDefinitions: []contract.ToolDefinition{
				{Name: "bash", Kind: contract.ToolKindAction, Description: "Run a script.", InputSchema: schema, Shadows: "bash"},
				// A manifest that got past install with a name off the list
				// still never shadows it.
				{Name: "write", Kind: contract.ToolKindAction, Description: "Write.", InputSchema: schema, Shadows: "write_file"},
				// Nor does a shadow of the wrong kind or schema: web_fetch is
				// a read tool that requires url.
				{Name: "fetch", Kind: contract.ToolKindAction, Description: "Fetch.", InputSchema: schema, Shadows: "web_fetch"},
			}}
		f := &fake{list: apps.List{Apps: []apps.App{app}}}
		defs, err := tools.NewChatToolAdapter(f).Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		Expect(defs).To(HaveLen(3))
		byTool := map[string]tools.ToolDefinition{}
		for _, d := range defs {
			byTool[d.Tool] = d
		}
		Expect(byTool["bash"].Name).To(Equal(tools.ToolName("sandbox", "bash")))
		Expect(byTool["bash"].Shadows).To(Equal("bash"))
		Expect(byTool["write"].Shadows).To(BeEmpty())
		Expect(byTool["fetch"].Shadows).To(BeEmpty())
	})
})

var _ = Describe("The tool cap of one discovery", func() {
	ctx := context.Background()
	// filler is an add-on with n read tools, named so that it sorts before
	// the sandbox: the apps list is ordered by ID.
	filler := func(id string, n int) apps.App {
		a := apps.App{ID: id, Generation: 1, Enabled: true, State: "running"}
		for i := range n {
			name := fmt.Sprintf("read-%d", i)
			a.Tools = append(a.Tools, name)
			a.ToolDefinitions = append(a.ToolDefinitions, contract.ToolDefinition{Name: name, Kind: contract.ToolKindRead, Description: "Read.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)})
		}
		return a
	}
	// sandboxApp is the packaged Sandbox add-on as the owner lists it.
	sandboxApp := func() apps.App {
		raw, err := os.ReadFile("../../../../addons/sandbox/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		return apps.App{ID: "sandbox", Title: "Sandbox", Generation: 23, Enabled: true, State: "running", Tools: m.Capabilities.Tools,
			ToolDefinitions: m.Capabilities.ToolDefinitions, Shadows: contract.ShadowedTools(m.Capabilities.ToolDefinitions)}
	}
	It("still offers the add-on that replaces bash when earlier add-ons fill the cap", func() {
		list := []apps.App{}
		for _, id := range []string{"aaa", "bbb", "ccc", "ddd", "eee"} {
			list = append(list, filler(id, 16)) // 80 tools, more than MaxTools
		}
		list = append(list, sandboxApp())
		f := &fake{list: apps.List{Apps: list}}
		defs, err := tools.NewChatToolAdapter(f).Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(defs)).To(BeNumerically("<=", tools.MaxTools))
		var bash *tools.ToolDefinition
		for i, d := range defs {
			if d.AddonID == "sandbox" && d.Tool == "bash" {
				bash = &defs[i]
			}
		}
		Expect(bash).NotTo(BeNil(), "the replacement of a built-in tool is never dropped by the cap")
		Expect(bash.Shadows).To(Equal("bash"))
		Expect(bash.Execution).NotTo(BeNil())
		Expect(bash.Execution.Class).To(Equal(contract.ExecutionClassCommands))
	})

	// five fillers of 16 tools (80) and the sandbox (more than the cap).
	fiveFillers := func() *fake {
		list := []apps.App{}
		for _, id := range []string{"aaa", "bbb", "ccc", "ddd", "eee"} {
			a := filler(id, 16)
			a.Title = "Add-on " + id
			list = append(list, a)
		}
		list = append(list, sandboxApp())
		return &fake{list: apps.List{Apps: list}}
	}
	cutOf := func(r tools.CapReport) map[string]int {
		m := map[string]int{}
		for _, a := range r.Addons {
			m[a.ID] = a.Cut
		}
		return m
	}

	It("reports what the cap left out, without changing what is offered", func() {
		defs, report, err := tools.NewChatToolAdapter(fiveFillers()).DiscoverWithReport(ctx, member, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Cap).To(Equal(tools.MaxTools))
		Expect(report.Offered).To(Equal(len(defs)))
		Expect(report.Offered).To(Equal(tools.MaxTools))
		Expect(report.Truncated()).To(BeTrue())
		Expect(report.Total()).To(Equal(report.Offered + report.Cut))
		total := 0
		for _, a := range report.Addons {
			total += a.Cut
		}
		Expect(total).To(Equal(report.Cut), "the per add-on counts add up to the cut")
		// The tools of the sandbox that replace bash are never cut; the
		// others come last in ID order, so the later fillers lose tools too.
		Expect(cutOf(report)).To(HaveKey("eee"))
		Expect(cutOf(report)).NotTo(HaveKey("aaa"))
		for _, a := range report.Addons {
			if a.ID == "eee" {
				Expect(a.Title).To(Equal("Add-on eee"))
			}
		}
		var bash bool
		for _, d := range defs {
			bash = bash || (d.AddonID == "sandbox" && d.Tool == "bash")
		}
		Expect(bash).To(BeTrue())
		plain, err := tools.NewChatToolAdapter(fiveFillers()).Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		Expect(plain).To(Equal(defs), "Discover offers the same tools as before")
	})

	It("reports nothing when the cap cuts nothing", func() {
		f := &fake{list: apps.List{Apps: []apps.App{filler("aaa", 3)}}}
		_, report, err := tools.NewChatToolAdapter(f).DiscoverWithReport(ctx, member, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Truncated()).To(BeFalse())
		Expect(report.Addons).To(BeEmpty())
		Expect(report.Offered).To(Equal(3))
	})

	It("lets a switched-off add-on free room for the ones that were cut", func() {
		adapter := tools.NewChatToolAdapter(fiveFillers())
		_, all, err := adapter.DiscoverWithReport(ctx, member, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cutOf(all)).To(HaveKey("eee"), "with every add-on on, the last filler loses tools")
		off := func(id string) bool { return id == "aaa" || id == "bbb" }
		defs, report, err := adapter.DiscoverWithReport(ctx, member, off)
		Expect(err).NotTo(HaveOccurred())
		for _, d := range defs {
			Expect(d.AddonID).NotTo(Equal("aaa"), "a switched-off add-on offers nothing")
			Expect(d.AddonID).NotTo(Equal("bbb"))
		}
		Expect(cutOf(report)).NotTo(HaveKey("eee"), "its room went to the add-on that was cut")
		Expect(report.Truncated()).To(BeFalse())
		Expect(report.Cut).To(BeNumerically("<", all.Cut))
		// On again: cut again.
		_, again, err := adapter.DiscoverWithReport(ctx, member, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cutOf(again)).To(Equal(cutOf(all)))
	})

	It("logs a cut once when it starts or changes, naming add-on IDs and counts only", func() {
		captured := &bytes.Buffer{}
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(captured, &slog.HandlerOptions{Level: slog.LevelWarn}), xlog.LogLevelWarn))
		DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text")) })
		actor := contract.Actor{UserID: "log-once", CredentialID: "session:s1", Mode: "named-user"}
		// A new adapter for each call, as the host builds them.
		for range 3 {
			_, _, err := tools.NewChatToolAdapter(fiveFillers()).DiscoverWithReport(ctx, actor, nil)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(strings.Count(captured.String(), "add-on tool cap reached")).To(Equal(1), captured.String())
		Expect(captured.String()).To(ContainSubstring("eee="))
		Expect(captured.String()).NotTo(ContainSubstring("Add-on eee"), "titles stay out of the log")
		// A different cut is logged again.
		limit := 40
		_, _, err := tools.NewChatToolAdapter(fiveFillers()).WithToolLimit(func() int { return limit }).DiscoverWithReport(ctx, actor, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Count(captured.String(), "add-on tool cap reached")).To(Equal(2), captured.String())
		// A call that excludes add-ons describes one chat, not the server: no log.
		_, _, err = tools.NewChatToolAdapter(fiveFillers()).WithToolLimit(func() int { return 20 }).DiscoverWithReport(ctx, actor, func(string) bool { return false })
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Count(captured.String(), "add-on tool cap reached")).To(Equal(2))
	})

	It("takes the cap from the limit that is set, read at each discovery", func() {
		limit := 24
		adapter := tools.NewChatToolAdapter(fiveFillers()).WithToolLimit(func() int { return limit })
		defs, report, err := adapter.DiscoverWithReport(ctx, member, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Cap).To(Equal(24))
		Expect(len(defs)).To(BeNumerically("<=", 24))
		limit = 128
		defs, report, err = adapter.DiscoverWithReport(ctx, member, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Cap).To(Equal(128))
		Expect(report.Truncated()).To(BeFalse())
		Expect(len(defs)).To(BeNumerically(">", tools.MaxTools))
		limit = 0
		_, report, err = adapter.DiscoverWithReport(ctx, member, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Cap).To(Equal(tools.MaxTools), "an unset limit is the default")
	})
})

var _ = Describe("The execution descriptor of a commands tool", func() {
	ctx := context.Background()
	schema := json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)
	bashTool := func(e *contract.ExecutionDescriptor) contract.ToolDefinition {
		return contract.ToolDefinition{Name: "bash", Kind: contract.ToolKindAction, Description: "Run.", InputSchema: schema, Shadows: "bash", Execution: e}
	}
	discover := func(app apps.App) tools.ToolDefinition {
		defs, err := tools.NewChatToolAdapter(&fake{list: apps.List{Apps: []apps.App{app}}}).Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		Expect(defs).To(HaveLen(1))
		return defs[0]
	}
	It("reaches the chat as the add-on declared it", func() {
		e := &contract.ExecutionDescriptor{Class: contract.ExecutionClassCommands, Title: "Runbox", Note: "It has jq."}
		d := discover(apps.App{ID: "runbox", Title: "Run box", Generation: 1, Enabled: true, State: "running", Tools: []string{"bash"}, ToolDefinitions: []contract.ToolDefinition{bashTool(e)}})
		Expect(d.Execution).To(Equal(e))
	})
	It("falls back to the add-on's title for a manifest that declares none", func() {
		d := discover(apps.App{ID: "runbox", Title: "Run box", Generation: 1, Enabled: true, State: "running", Tools: []string{"bash"}, ToolDefinitions: []contract.ToolDefinition{bashTool(nil)}})
		Expect(d.Execution).To(Equal(&contract.ExecutionDescriptor{Class: contract.ExecutionClassCommands, Title: "Run box"}))
	})
	It("lists the skills that need isolation for the add-on that owns the commands tool", func() {
		skills := []contract.SkillDefinition{{Name: "docx", Shadows: "docx"}, {Name: "runbox-tips"}}
		app := apps.App{ID: "runbox", Title: "Run box", Generation: 1, Enabled: true, State: "running", Tools: []string{"bash"}, Skills: skills,
			ToolDefinitions: []contract.ToolDefinition{bashTool(nil)}}
		list, err := tools.NewChatToolAdapter(&fake{list: apps.List{Apps: []apps.App{app}}}).Skills(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Isolated).To(Equal([]string{"docx"}))
	})
})

var _ = Describe("The add-ons that replace a built-in chat tool", func() {
	ctx := context.Background()
	bash := json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)
	It("are listed in any state, with the names they replace and whether they run", func() {
		running := apps.App{ID: "sandbox", Title: "Sandbox", Generation: 1, Enabled: true, State: "running", Tools: []string{"bash"}, InstalledAt: "2026-01-01T00:00:00Z",
			ToolDefinitions: []contract.ToolDefinition{{Name: "bash", Kind: contract.ToolKindAction, Description: "Run.", InputSchema: bash, Shadows: "bash"}}}
		stopped := apps.App{ID: "other", Title: "Other", Generation: 1, Enabled: true, State: "stopped", Tools: []string{"bash", "bad"},
			ToolDefinitions: []contract.ToolDefinition{
				{Name: "bash", Kind: contract.ToolKindAction, Description: "Run.", InputSchema: bash, Shadows: "bash"},
				// A shadow that fails the check replaces nothing.
				{Name: "bad", Kind: contract.ToolKindAction, Description: "Fetch.", InputSchema: bash, Shadows: "web_fetch"},
			}}
		plain := apps.App{ID: "notes", Generation: 1, Enabled: true, State: "running", Tools: []string{"read"},
			ToolDefinitions: []contract.ToolDefinition{{Name: "read", Kind: contract.ToolKindRead, Description: "Read.", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}}}
		f := &fake{list: apps.List{Apps: []apps.App{running, stopped, plain}}}
		list, err := tools.NewChatToolAdapter(f).Shadowers(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(Equal([]tools.AddonShadows{
			{AddonID: "other", Title: "Other", Tools: []string{"bash"}, Running: false},
			{AddonID: "sandbox", Title: "Sandbox", InstalledAt: "2026-01-01T00:00:00Z", Tools: []string{"bash"}, Running: true},
		}))
	})
})

var _ = Describe("The add-ons that declare context files", func() {
	ctx := context.Background()
	It("are listed in any state, with their files and whether they run", func() {
		files := []contract.ContextFile{{Scope: contract.ScopeUser, Path: "Memory/index.md"}}
		running := apps.App{ID: "memory", Generation: 1, Enabled: true, State: "running", ContextFiles: files}
		stopped := apps.App{ID: "aaa-notes", Generation: 1, Enabled: true, State: "stopped", ContextFiles: files}
		plain := apps.App{ID: "plain", Generation: 1, Enabled: true, State: "running"}
		list, err := tools.NewChatToolAdapter(&fake{list: apps.List{Apps: []apps.App{running, stopped, plain}}}).ContextFiles(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(Equal([]tools.AddonContextFiles{
			{AddonID: "aaa-notes", Files: files, Running: false},
			{AddonID: "memory", Files: files, Running: true},
		}))
	})
})

var _ = Describe("typed execution status model exclusion", func() {
	It("keeps declared uiOnly execution status tools out of generic adapters", func() {
		app := apps.App{ID: "renamed", Generation: 1, Enabled: true, State: "running", Tools: []string{"health"}, ToolDefinitions: []contract.ToolDefinition{{Name: "health", Kind: "read", UIOnly: true, Description: "Observe execution.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}}}
		f := &fake{list: apps.List{Apps: []apps.App{app}}}
		for _, a := range []*tools.ToolAdapter{tools.NewToolAdapter(f), tools.NewChatToolAdapter(f)} {
			defs, e := a.Discover(context.Background(), member)
			Expect(e).NotTo(HaveOccurred())
			Expect(defs).To(BeEmpty())
			_, e = a.Execute(context.Background(), member, tools.ToolName("renamed", "health"), json.RawMessage(`{}`))
			Expect(e).To(MatchError(tools.ErrUnknownTool))
		}
		Expect(f.calls()).To(BeZero())
	})
})

var _ = Describe("The permission a tool declares to the chat", func() {
	ctx := context.Background()
	discover := func(credentialed bool, scoped ...bool) map[string]string {
		app := tablesApp(tablesManifest())
		app.Credentialed = credentialed
		app.Resources.AgentScoped = len(scoped) > 0 && scoped[0]
		defs, err := tools.NewChatToolAdapter(&fake{list: apps.List{Apps: []apps.App{app}}}).Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		out := map[string]string{}
		for _, d := range defs {
			out[d.Tool] = d.Permission
		}
		return out
	}
	It("is tools.write for the tools that change things, in an add-on that holds a credential", func() {
		got := discover(true)
		Expect(got["databases-inspect"]).To(BeEmpty(), "a read needs nothing")
		Expect(got["tables-create"]).To(Equal(contract.CapToolsWrite))
		Expect(got["databases-create"]).To(Equal(contract.CapToolsWrite))
	})
	It("is empty for every tool of an add-on without a credential, when its resources are not scoped per agent", func() {
		for name, p := range discover(false, false) {
			Expect(p).To(BeEmpty(), name)
		}
	})
	It("is tools.write for the tools that change things, in an add-on that keeps the owner's own data", func() {
		app := tablesApp(tablesManifest())
		app.OwnerData = true
		defs, err := tools.NewChatToolAdapter(&fake{list: apps.List{Apps: []apps.App{app}}}).Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		got := map[string]string{}
		for _, d := range defs {
			got[d.Tool] = d.Permission
			Expect(d.OwnerData).To(BeTrue(), d.Tool)
		}
		Expect(got["databases-inspect"]).To(BeEmpty(), "a read needs nothing")
		Expect(got["rows-query"]).To(BeEmpty())
		Expect(got["rows-insert"]).To(Equal(contract.CapToolsWrite))
	})
	It("is tools.write for the tools that change things, in an add-on whose resources are scoped per agent", func() {
		got := discover(false, true)
		Expect(got["databases-inspect"]).To(BeEmpty(), "a read needs nothing")
		Expect(got["rows-query"]).To(BeEmpty())
		Expect(got["rows-insert"]).To(Equal(contract.CapToolsWrite))
		Expect(got["tables-drop"]).To(Equal(contract.CapToolsWrite))
	})
})

var _ = Describe("deferred tool definitions", func() {
	ctx := context.Background()
	schema := json.RawMessage(`{"type":"object","properties":{"folder":{"type":"string"}},"additionalProperties":false}`)
	app := func(deferred bool, uiOnly bool) apps.App {
		return apps.App{ID: "notes", Generation: 1, Enabled: true, State: "running", Tools: []string{"list-notes", "page-only"}, ToolDefinitions: []contract.ToolDefinition{
			{Name: "list-notes", Kind: "read", Description: "List the notes.", InputSchema: schema, Deferred: deferred},
			// A manifest that passed the validator never has both flags; the
			// adapter is held to the rule again at the point of use.
			{Name: "page-only", Kind: "read", Description: "For the page.", InputSchema: schema, UIOnly: uiOnly, Deferred: uiOnly},
		}}
	}

	It("tells the chat consumer which tools are deferred, and no other consumer", func() {
		f := &fake{list: apps.List{Apps: []apps.App{app(true, false)}}}
		chat, err := tools.NewChatToolAdapter(f).Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		var deferred []string
		for _, d := range chat {
			if d.Deferred {
				deferred = append(deferred, d.Tool)
			}
		}
		Expect(deferred).To(ConsistOf("list-notes"), "the flag is carried; the chat decides what to do with it")
	})

	It("lists the same tools with the same wire form whether or not they are deferred (MCP, agents, triggers)", func() {
		wire := func(deferred bool) string {
			f := &fake{list: apps.List{Apps: []apps.App{app(deferred, false)}}}
			defs, err := tools.NewToolAdapter(f).Discover(ctx, member)
			Expect(err).NotTo(HaveOccurred())
			raw, err := json.Marshal(defs)
			Expect(err).NotTo(HaveOccurred())
			return string(raw)
		}
		Expect(wire(true)).To(Equal(wire(false)))
		Expect(wire(true)).To(ContainSubstring("addon__notes__list-notes"))
		Expect(wire(true)).NotTo(ContainSubstring("deferred"))
	})

	It("never lists a page-only tool, deferred or not", func() {
		f := &fake{list: apps.List{Apps: []apps.App{app(true, true)}}}
		for _, a := range []*tools.ToolAdapter{tools.NewToolAdapter(f), tools.NewChatToolAdapter(f)} {
			defs, err := a.Discover(ctx, member)
			Expect(err).NotTo(HaveOccurred())
			for _, d := range defs {
				Expect(d.Tool).NotTo(Equal("page-only"))
			}
		}
	})

	It("still runs a deferred tool through the broker like any other", func() {
		f := &fake{list: apps.List{Apps: []apps.App{app(true, false)}}}
		_, err := tools.NewToolAdapter(f).Execute(ctx, member, tools.ToolName("notes", "list-notes"), json.RawMessage(`{"folder":"a"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.calls()).To(Equal(1))
	})
})
