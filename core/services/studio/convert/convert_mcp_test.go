package convert_test

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/convert"
)

func mcpPool(dir string, cfg map[string]any) {
	base := map[string]any{"name": alice + ":Helper", "model": "llama", "system_prompt": "help"}
	maps.Copy(base, cfg)
	write(dir+"/pool.json", mustJSON(map[string]any{alice + ":Helper": base}))
}

var _ = Describe("converting the old MCP servers", func() {
	It("turns old MCP servers into disabled connectors of the owner and gives them to the agent", func() {
		dir := GinkgoT().TempDir()
		mcpPool(dir, map[string]any{"mcp_servers": []any{
			map[string]any{"url": "https://mcp.example/a", "token": "SECRET-A"},
			map[string]any{"url": "https://other.example/b"},
			map[string]any{"url": ""},
		}})
		tg := newTargets()
		r := run(convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks())

		got := tg.mcp[alice]
		Expect(got).To(HaveLen(2), "connectors = %+v", got)
		Expect(got[0].Transport).To(Equal("http"), "connectors = %+v", got)
		Expect(got[0].URL).To(Equal("https://mcp.example/a"), "connectors = %+v", got)
		Expect(got[0].Token).To(Equal("SECRET-A"), "connectors = %+v", got)
		Expect(got[1].Token).To(BeEmpty(), "connectors = %+v", got)
		Expect(got[0].Name).NotTo(Equal(got[1].Name), "names = %q %q", got[0].Name, got[1].Name)
		Expect(got[0].Name).To(ContainSubstring("Helper"), "names = %q %q", got[0].Name, got[1].Name)
		ag := tg.agents[alice]
		Expect(ag).To(HaveLen(1), "the agent got %+v", ag)
		Expect(slices.Equal(ag[0].Connectors, []string{"mcp-1-1", "mcp-1-2"})).To(BeTrue(), "the agent got %+v", ag)
		conv, dropped := 0, 0
		for _, it := range r.Items {
			if it.Kind != convert.KindConnector || !strings.Contains(it.Name, "MCP") {
				continue
			}
			switch it.Status {
			case convert.StatusConverted:
				conv++
				Expect(it.Reason).To(ContainSubstring("switched off"), "a converted connector must say it starts switched off: %+v", it)
			case convert.StatusDropped:
				dropped++
			}
			Expect(it.Reason+it.Name).NotTo(ContainSubstring("SECRET"), "the report holds a value: %+v", it)
			Expect(it.Reason).NotTo(ContainSubstring("mcp.example"), "the report holds a value: %+v", it)
		}
		Expect(conv).To(Equal(2), "converted %d, dropped %d: %+v", conv, dropped, r.Items)
		Expect(dropped).To(Equal(1), "converted %d, dropped %d: %+v", conv, dropped, r.Items)
		it, _ := find(r, convert.KindAgent, "Helper")
		Expect(it.Reason).NotTo(ContainSubstring("mcp_servers"), "the agent reports its MCP servers as dropped")
	})

	It("converts a stdio server only for an administrator", func() {
		dir := GinkgoT().TempDir()
		mcpPool(dir, map[string]any{"mcp_stdio_servers": []any{
			map[string]any{"cmd": "npx", "args": []any{"-y", "pkg"}, "env": []any{"API_KEY=SECRET-ENV", "MODE=fast"}},
		}})
		for _, admin := range []bool{false, true} {
			tg := newTargets()
			tg.admins[alice] = admin
			r := run(convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks())
			var item convert.Item
			for _, it := range r.Items {
				if it.Kind == convert.KindConnector && strings.Contains(it.Name, "MCP") {
					item = it
				}
			}
			if admin {
				got := tg.mcp[alice]
				Expect(got).To(HaveLen(1), "connectors = %+v", got)
				Expect(got[0].Transport).To(Equal("stdio"), "connectors = %+v", got)
				Expect(got[0].Command).To(Equal("npx"), "connectors = %+v", got)
				Expect(got[0].Args).To(Equal([]string{"-y", "pkg"}), "connectors = %+v", got)
				Expect(got[0].Env).To(HaveKeyWithValue("API_KEY", "SECRET-ENV"))
				Expect(got[0].Env).To(HaveKeyWithValue("MODE", "fast"))
				Expect(item.Status).To(Equal(convert.StatusConverted), "item = %+v", item)
				continue
			}
			Expect(tg.mcp[alice]).To(BeEmpty(), "a user who is not an administrator: item %+v", item)
			Expect(item.Status).To(Equal(convert.StatusDropped), "a user who is not an administrator: item %+v", item)
			Expect(item.Reason).To(ContainSubstring("administrator"), "a user who is not an administrator: item %+v", item)
			Expect(item.Reason).NotTo(ContainSubstring("SECRET"), "the reason holds a value")
			Expect(tg.agents[alice]).To(HaveLen(1), "the agent must still convert, without it: %+v", tg.agents[alice])
			Expect(tg.agents[alice][0].Connectors).To(BeEmpty(), "the agent must still convert, without it: %+v", tg.agents[alice])
		}
	})

	It("converts a stdio list given as MCP servers JSON", func() {
		dir := GinkgoT().TempDir()
		mcpPool(dir, map[string]any{"mcp_stdio_servers": mustJSON(map[string]any{"mcpServers": map[string]any{
			"files": map[string]any{"command": "mcp-files", "args": []any{"/data"}, "env": map[string]any{"K": "v"}},
		}})})
		tg := newTargets()
		tg.admins[alice] = true
		run(convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks())
		got := tg.mcp[alice]
		Expect(got).To(HaveLen(1), "connectors = %+v", got)
		Expect(got[0].Command).To(Equal("mcp-files"), "connectors = %+v", got)
		Expect(got[0].Env).To(HaveKeyWithValue("K", "v"))
		Expect(got[0].Name).To(ContainSubstring("files"), "connectors = %+v", got)
	})

	It("converts MCP servers idempotently", func() {
		dir := GinkgoT().TempDir()
		mcpPool(dir, map[string]any{"mcp_servers": []any{map[string]any{"url": "https://mcp.example/a"}}})
		tg := newTargets()
		marks := memMarks()
		src := convert.Sources{StateDir: dir, AuthEnabled: true}
		run(src, tg, marks)
		second := run(src, tg, marks)
		Expect(tg.mcp[alice]).To(HaveLen(1), "a second run made more: %+v %+v", tg.mcp, tg.agents)
		Expect(tg.agents[alice]).To(HaveLen(1), "a second run made more: %+v %+v", tg.mcp, tg.agents)
		Expect(second.Notable).To(BeEmpty(), "a second run has news")
		stored, _, _ := marks.Report(context.Background())
		var n int
		for _, it := range stored.Items {
			if it.Kind == convert.KindConnector && it.Status == convert.StatusConverted {
				n++
			}
		}
		Expect(n).To(Equal(1), "the stored report keeps %d converted connectors: %+v", n, stored.Items)
	})

	It("numbers a name that is taken", func() {
		dir := GinkgoT().TempDir()
		mcpPool(dir, map[string]any{"mcp_servers": []any{map[string]any{"url": "https://mcp.example/a"}, map[string]any{"url": "https://mcp.example/b"}}})
		tg := newTargets()
		run(convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks())
		got := tg.mcp[alice]
		Expect(got).To(HaveLen(2), "two servers of one host must get two names: %+v", got)
		Expect(got[0].Name).NotTo(Equal(got[1].Name), "two servers of one host must get two names: %+v", got)
	})

	It("holds back the agent after a failed connector until the next run and makes nothing twice", func() {
		dir := GinkgoT().TempDir()
		mcpPool(dir, map[string]any{"mcp_servers": []any{map[string]any{"url": "https://mcp.example/a"}, map[string]any{"url": "https://mcp.example/b"}}})
		tg := newTargets()
		marks := memMarks()
		src := convert.Sources{StateDir: dir, AuthEnabled: true}
		// The name the second entry will get.
		probe := newTargets()
		run(src, probe, memMarks())
		name2 := probe.mcp[alice][1].Name
		tg.mcpErr[name2] = fmt.Errorf("disk full")
		r := run(src, tg, marks)
		Expect(tg.agents[alice]).To(BeEmpty(), "the agent was saved without its connectors")
		it, _ := find(r, convert.KindAgent, "Helper")
		Expect(it.Status).To(Equal(convert.StatusFailed), "agent item = %+v", it)
		delete(tg.mcpErr, name2)
		run(src, tg, marks)
		Expect(tg.mcp[alice]).To(HaveLen(2), "connectors after the retry, want 2 and none twice")
		ag := tg.agents[alice]
		Expect(ag).To(HaveLen(1), "agent after the retry = %+v", ag)
		Expect(ag[0].Connectors).To(HaveLen(2), "agent after the retry = %+v", ag)
	})

	It("drops a connector the target refuses for good, with its reason", func() {
		dir := GinkgoT().TempDir()
		mcpPool(dir, map[string]any{"mcp_servers": []any{map[string]any{"url": "https://mcp.example/a"}}})
		probe := newTargets()
		run(convert.Sources{StateDir: dir, AuthEnabled: true}, probe, memMarks())
		tg := newTargets()
		tg.mcpErr[probe.mcp[alice][0].Name] = fmt.Errorf("%w: the URL is not valid", convert.ErrInvalid)
		r := run(convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks())
		Expect(tg.agents[alice]).To(HaveLen(1), "agent = %+v", tg.agents[alice])
		Expect(tg.agents[alice][0].Connectors).To(BeEmpty(), "agent = %+v", tg.agents[alice])
		found := false
		for _, it := range r.Items {
			if it.Kind == convert.KindConnector && it.Status == convert.StatusDropped && strings.Contains(it.Reason, "the URL is not valid") {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "items = %+v", r.Items)
	})
})
