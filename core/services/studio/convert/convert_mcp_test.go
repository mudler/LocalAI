package convert_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/convert"
)

func mcpPool(t *testing.T, dir string, cfg map[string]any) {
	t.Helper()
	base := map[string]any{"name": alice + ":Helper", "model": "llama", "system_prompt": "help"}
	for k, v := range cfg {
		base[k] = v
	}
	write(t, dir+"/pool.json", mustJSON(map[string]any{alice + ":Helper": base}))
}

func TestOldMCPServersBecomeDisabledConnectorsOfTheOwnerAndTheAgentGetsThem(t *testing.T) {
	dir := t.TempDir()
	mcpPool(t, dir, map[string]any{"mcp_servers": []any{
		map[string]any{"url": "https://mcp.example/a", "token": "SECRET-A"},
		map[string]any{"url": "https://other.example/b"},
		map[string]any{"url": ""},
	}})
	tg := newTargets()
	r := run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks(t))

	got := tg.mcp[alice]
	if len(got) != 2 {
		t.Fatalf("connectors = %+v", got)
	}
	if got[0].Transport != "http" || got[0].URL != "https://mcp.example/a" || got[0].Token != "SECRET-A" || got[1].Token != "" {
		t.Fatalf("connectors = %+v", got)
	}
	if got[0].Name == got[1].Name || !strings.Contains(got[0].Name, "Helper") {
		t.Fatalf("names = %q %q", got[0].Name, got[1].Name)
	}
	ag := tg.agents[alice]
	if len(ag) != 1 || !slices.Equal(ag[0].Connectors, []string{"mcp-1-1", "mcp-1-2"}) {
		t.Fatalf("the agent got %+v", ag)
	}
	conv, dropped := 0, 0
	for _, it := range r.Items {
		if it.Kind != convert.KindConnector || !strings.Contains(it.Name, "MCP") {
			continue
		}
		switch it.Status {
		case convert.StatusConverted:
			conv++
			if !strings.Contains(it.Reason, "switched off") {
				t.Errorf("a converted connector must say it starts switched off: %+v", it)
			}
		case convert.StatusDropped:
			dropped++
		}
		if strings.Contains(it.Reason+it.Name, "SECRET") || strings.Contains(it.Reason, "mcp.example") {
			t.Errorf("the report holds a value: %+v", it)
		}
	}
	if conv != 2 || dropped != 1 {
		t.Fatalf("converted %d, dropped %d: %+v", conv, dropped, r.Items)
	}
	it, _ := find(r, convert.KindAgent, "Helper")
	if strings.Contains(it.Reason, "mcp_servers") {
		t.Fatalf("the agent reports its MCP servers as dropped: %q", it.Reason)
	}
}

func TestAStdioServerConvertsOnlyForAnAdministrator(t *testing.T) {
	dir := t.TempDir()
	mcpPool(t, dir, map[string]any{"mcp_stdio_servers": []any{
		map[string]any{"cmd": "npx", "args": []any{"-y", "pkg"}, "env": []any{"API_KEY=SECRET-ENV", "MODE=fast"}},
	}})
	for _, admin := range []bool{false, true} {
		tg := newTargets()
		tg.admins[alice] = admin
		r := run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks(t))
		var item convert.Item
		for _, it := range r.Items {
			if it.Kind == convert.KindConnector && strings.Contains(it.Name, "MCP") {
				item = it
			}
		}
		if admin {
			got := tg.mcp[alice]
			if len(got) != 1 || got[0].Transport != "stdio" || got[0].Command != "npx" || !slices.Equal(got[0].Args, []string{"-y", "pkg"}) ||
				got[0].Env["API_KEY"] != "SECRET-ENV" || got[0].Env["MODE"] != "fast" {
				t.Fatalf("connectors = %+v", got)
			}
			if item.Status != convert.StatusConverted {
				t.Fatalf("item = %+v", item)
			}
			continue
		}
		if len(tg.mcp[alice]) != 0 || item.Status != convert.StatusDropped || !strings.Contains(item.Reason, "administrator") {
			t.Fatalf("a user who is not an administrator: connectors %+v, item %+v", tg.mcp[alice], item)
		}
		if strings.Contains(item.Reason, "SECRET") {
			t.Fatalf("the reason holds a value: %q", item.Reason)
		}
		if len(tg.agents[alice]) != 1 || len(tg.agents[alice][0].Connectors) != 0 {
			t.Fatalf("the agent must still convert, without it: %+v", tg.agents[alice])
		}
	}
}

func TestAStdioListGivenAsMCPServersJSONConverts(t *testing.T) {
	dir := t.TempDir()
	mcpPool(t, dir, map[string]any{"mcp_stdio_servers": mustJSON(map[string]any{"mcpServers": map[string]any{
		"files": map[string]any{"command": "mcp-files", "args": []any{"/data"}, "env": map[string]any{"K": "v"}},
	}})})
	tg := newTargets()
	tg.admins[alice] = true
	run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks(t))
	got := tg.mcp[alice]
	if len(got) != 1 || got[0].Command != "mcp-files" || got[0].Env["K"] != "v" || !strings.Contains(got[0].Name, "files") {
		t.Fatalf("connectors = %+v", got)
	}
}

func TestMCPConversionIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	mcpPool(t, dir, map[string]any{"mcp_servers": []any{map[string]any{"url": "https://mcp.example/a"}}})
	tg := newTargets()
	marks := memMarks(t)
	src := convert.Sources{StateDir: dir, AuthEnabled: true}
	run(t, src, tg, marks)
	second := run(t, src, tg, marks)
	if len(tg.mcp[alice]) != 1 || len(tg.agents[alice]) != 1 {
		t.Fatalf("a second run made more: %+v %+v", tg.mcp, tg.agents)
	}
	if len(second.Notable) != 0 {
		t.Fatalf("a second run has news: %+v", second.Notable)
	}
	stored, _, _ := marks.Report(t.Context())
	var n int
	for _, it := range stored.Items {
		if it.Kind == convert.KindConnector && it.Status == convert.StatusConverted {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the stored report keeps %d converted connectors: %+v", n, stored.Items)
	}
}

func TestANameThatIsTakenGetsANumber(t *testing.T) {
	dir := t.TempDir()
	mcpPool(t, dir, map[string]any{"mcp_servers": []any{map[string]any{"url": "https://mcp.example/a"}, map[string]any{"url": "https://mcp.example/b"}}})
	tg := newTargets()
	run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks(t))
	got := tg.mcp[alice]
	if len(got) != 2 || got[0].Name == got[1].Name {
		t.Fatalf("two servers of one host must get two names: %+v", got)
	}
}

func TestAFailedConnectorHoldsBackTheAgentUntilTheNextRunAndNothingIsMadeTwice(t *testing.T) {
	dir := t.TempDir()
	mcpPool(t, dir, map[string]any{"mcp_servers": []any{map[string]any{"url": "https://mcp.example/a"}, map[string]any{"url": "https://mcp.example/b"}}})
	tg := newTargets()
	// The name the second entry will get.
	name2 := ""
	marks := memMarks(t)
	src := convert.Sources{StateDir: dir, AuthEnabled: true}
	probe := newTargets()
	run(t, src, probe, memMarks(t))
	name2 = probe.mcp[alice][1].Name
	tg.mcpErr[name2] = fmt.Errorf("disk full")
	r := run(t, src, tg, marks)
	if len(tg.agents[alice]) != 0 {
		t.Fatalf("the agent was saved without its connectors: %+v", tg.agents[alice])
	}
	if it, _ := find(r, convert.KindAgent, "Helper"); it.Status != convert.StatusFailed {
		t.Fatalf("agent item = %+v", it)
	}
	delete(tg.mcpErr, name2)
	run(t, src, tg, marks)
	if len(tg.mcp[alice]) != 2 {
		t.Fatalf("connectors after the retry = %+v, want 2 and none twice", tg.mcp[alice])
	}
	if ag := tg.agents[alice]; len(ag) != 1 || len(ag[0].Connectors) != 2 {
		t.Fatalf("agent after the retry = %+v", ag)
	}
}

func TestAConnectorTheTargetRefusesForGoodIsDroppedWithItsReason(t *testing.T) {
	dir := t.TempDir()
	mcpPool(t, dir, map[string]any{"mcp_servers": []any{map[string]any{"url": "https://mcp.example/a"}}})
	probe := newTargets()
	run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, probe, memMarks(t))
	tg := newTargets()
	tg.mcpErr[probe.mcp[alice][0].Name] = fmt.Errorf("%w: the URL is not valid", convert.ErrInvalid)
	r := run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks(t))
	if len(tg.agents[alice]) != 1 || len(tg.agents[alice][0].Connectors) != 0 {
		t.Fatalf("agent = %+v", tg.agents[alice])
	}
	found := false
	for _, it := range r.Items {
		if it.Kind == convert.KindConnector && it.Status == convert.StatusDropped && strings.Contains(it.Reason, "the URL is not valid") {
			found = true
		}
	}
	if !found {
		t.Fatalf("items = %+v", r.Items)
	}
}
