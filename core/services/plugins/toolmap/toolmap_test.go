package toolmap_test

import (
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins/toolmap"
)

type call struct {
	name string
	args string
}

func toLocal(name, input string) []call {
	GinkgoHelper()
	calls, err := toolmap.ToLocal(name, json.RawMessage(input))
	Expect(err).NotTo(HaveOccurred())
	out := []call{}
	for _, c := range calls {
		out = append(out, call{c.Name, string(c.Args)})
	}
	return out
}

func expectCalls(got []call, want ...call) {
	GinkgoHelper()
	Expect(got).To(HaveLen(len(want)))
	for i := range want {
		Expect(got[i].name).To(Equal(want[i].name))
		Expect(got[i].args).To(MatchJSON(want[i].args))
	}
}

var _ = Describe("ToLocal (format to LocalAI, spec 11.1)", func() {
	DescribeTable("maps each tool of the table",
		func(name, input string, want ...call) { expectCalls(toLocal(name, input), want...) },
		Entry("Bash", "Bash", `{"command":"ls","timeout":1500}`,
			call{"bash", `{"command":"ls","timeoutSeconds":2}`}),
		Entry("Bash without a timeout", "Bash", `{"command":"ls"}`,
			call{"bash", `{"command":"ls"}`}),
		Entry("Bash in the background", "Bash", `{"command":"sleep 9","run_in_background":true}`,
			call{"job", `{"action":"start","command":"sleep 9"}`}),
		Entry("BashOutput", "BashOutput", `{"bash_id":"j1"}`,
			call{"job", `{"action":"read","id":"j1"}`}),
		Entry("KillShell", "KillShell", `{"shell_id":"j1"}`,
			call{"job", `{"action":"kill","id":"j1"}`}),
		Entry("KillBash", "KillBash", `{"shell_id":"j2"}`,
			call{"job", `{"action":"kill","id":"j2"}`}),
		Entry("Read", "Read", `{"file_path":"/workspace/home/a.md","offset":3,"limit":10}`,
			call{"read_file", `{"path":"/workspace/home/a.md","offset":3,"limit":10}`}),
		Entry("Write", "Write", `{"file_path":"/workspace/home/a.md","content":"x"}`,
			call{"write_file", `{"path":"/workspace/home/a.md","content":"x"}`}),
		Entry("Edit", "Edit", `{"file_path":"a.md","old_string":"a","new_string":"b","replace_all":true}`,
			call{"edit_file", `{"path":"a.md","old_string":"a","new_string":"b","replace_all":true}`}),
		Entry("MultiEdit", "MultiEdit", `{"file_path":"a.md","edits":[{"old_string":"a","new_string":"b"},{"old_string":"c","new_string":"d","replace_all":true}]}`,
			call{"edit_file", `{"path":"a.md","old_string":"a","new_string":"b"}`},
			call{"edit_file", `{"path":"a.md","old_string":"c","new_string":"d","replace_all":true}`}),
		Entry("Glob", "Glob", `{"pattern":"*.md","path":"docs"}`,
			call{"list_files", `{"pattern":"*.md","path":"docs"}`}),
		Entry("LS", "LS", `{"path":"docs"}`,
			call{"list_files", `{"path":"docs"}`}),
		Entry("Grep, case sensitive", "Grep", `{"pattern":"TODO","path":"src"}`,
			call{"search_files", `{"query":"TODO","regex":true,"path":"src","ignore_case":false}`}),
		Entry("Grep with -i and dropped flags", "Grep", `{"pattern":"todo","-i":true,"-n":true,"output_mode":"content","glob":"*.go"}`,
			call{"search_files", `{"query":"todo","regex":true,"ignore_case":true}`}),
		Entry("WebFetch", "WebFetch", `{"url":"https://example.test","prompt":"summarize"}`,
			call{"web_fetch", `{"url":"https://example.test"}`}),
		Entry("WebSearch", "WebSearch", `{"query":"go","allowed_domains":["go.dev","pkg.go.dev"],"blocked_domains":["x.test"]}`,
			call{"web_search", `{"query":"go","site":"go.dev"}`}),
		Entry("WebSearch without domains", "WebSearch", `{"query":"go"}`,
			call{"web_search", `{"query":"go"}`}),
		Entry("Task", "Task", `{"prompt":"review","subagent_type":"reviewer","description":"Review"}`,
			call{"spawn_agent", `{"task":"review","agent":"reviewer"}`}),
		Entry("Agent", "Agent", `{"prompt":"review"}`,
			call{"spawn_agent", `{"task":"review"}`}),
		Entry("Skill", "Skill", `{"skill":"fmt:style"}`,
			call{"load_skill", `{"name":"fmt:style"}`}),
		Entry("a plugin MCP tool", "mcp__plugin_fmt_srv__lint", `{"a":1}`,
			call{"mcp__plugin_fmt_srv__lint", `{"a":1}`}),
		Entry("an MCP tool", "mcp__github__search", `{"q":"x"}`,
			call{"mcp__github__search", `{"q":"x"}`}),
		Entry("a LocalAI tool by its own name", "generate_image", `{"prompt":"cat"}`,
			call{"generate_image", `{"prompt":"cat"}`}),
	)

	DescribeTable("drops the tools LocalAI does not have",
		func(name string) {
			_, err := toolmap.ToLocal(name, json.RawMessage(`{}`))
			Expect(errors.Is(err, toolmap.ErrNoLocalTool)).To(BeTrue(), "got %v", err)
		},
		Entry(nil, "TodoWrite"), Entry(nil, "NotebookEdit"), Entry(nil, "NotebookRead"),
		Entry(nil, "ExitPlanMode"), Entry(nil, "TodoRead"), Entry(nil, "AskUserQuestion"),
	)

	DescribeTable("fails an input with a field that does not map",
		func(name, input string) {
			_, err := toolmap.ToLocal(name, json.RawMessage(input))
			Expect(errors.Is(err, toolmap.ErrUnmappedField)).To(BeTrue(), "got %v", err)
		},
		Entry("Read with an unknown field", "Read", `{"file_path":"a","pages":"1-3"}`),
		Entry("Bash with an unknown field", "Bash", `{"command":"ls","sandbox":false}`),
		Entry("BashOutput with a filter", "BashOutput", `{"bash_id":"j","filter":"err"}`),
		Entry("LS with ignore", "LS", `{"path":".","ignore":["x"]}`),
		Entry("MultiEdit with an unknown edit field", "MultiEdit", `{"file_path":"a","edits":[{"old_string":"a","new_string":"b","x":1}]}`),
		Entry("Skill with args", "Skill", `{"skill":"s","args":"x"}`),
	)

	It("refuses an input that is not an object", func() {
		_, err := toolmap.ToLocal("Read", json.RawMessage(`[1]`))
		Expect(err).To(HaveOccurred())
		_, err = toolmap.ToLocal("Read", json.RawMessage(`{"file_path":3}`))
		Expect(err).To(HaveOccurred())
	})

	It("accepts an empty input", func() {
		expectCalls(toLocal("LS", ``), call{"list_files", `{}`})
	})
})

var _ = Describe("ToFormat (LocalAI to the format, spec 11.2)", func() {
	DescribeTable("maps each LocalAI tool to one format name",
		func(name, args, wantName, wantArgs string) {
			n, a := toolmap.ToFormat(name, json.RawMessage(args))
			Expect(n).To(Equal(wantName))
			Expect(string(a)).To(MatchJSON(wantArgs))
		},
		Entry(nil, "bash", `{"command":"ls","timeoutSeconds":2}`, "Bash", `{"command":"ls","timeout":2000}`),
		Entry(nil, "job", `{"action":"start","command":"sleep 9","label":"s"}`, "Bash", `{"command":"sleep 9","run_in_background":true}`),
		Entry(nil, "job", `{"action":"read","id":"j1"}`, "BashOutput", `{"bash_id":"j1"}`),
		Entry(nil, "job", `{"action":"kill","id":"j1"}`, "KillShell", `{"shell_id":"j1"}`),
		Entry("a job action with no format tool", "job", `{"action":"list"}`, "job", `{"action":"list"}`),
		Entry(nil, "read_file", `{"path":"a","offset":1,"limit":2}`, "Read", `{"file_path":"a","offset":1,"limit":2}`),
		Entry(nil, "write_file", `{"path":"a","content":"x"}`, "Write", `{"file_path":"a","content":"x"}`),
		Entry(nil, "edit_file", `{"path":"a","old_string":"x","new_string":"y"}`, "Edit", `{"file_path":"a","old_string":"x","new_string":"y"}`),
		Entry(nil, "list_files", `{"path":"d","pattern":"*.md"}`, "Glob", `{"path":"d","pattern":"*.md"}`),
		Entry(nil, "search_files", `{"query":"a.b","regex":true,"path":"d","ignore_case":false}`, "Grep", `{"pattern":"a.b","path":"d","-i":false}`),
		Entry("plain text search", "search_files", `{"query":"a.b"}`, "Grep", `{"pattern":"a\\.b","-i":true}`),
		Entry(nil, "web_fetch", `{"url":"https://example.test"}`, "WebFetch", `{"url":"https://example.test"}`),
		Entry(nil, "web_search", `{"query":"go","site":"go.dev"}`, "WebSearch", `{"query":"go","allowed_domains":["go.dev"]}`),
		Entry(nil, "spawn_agent", `{"task":"t","agent":"a","wait":false}`, "Task", `{"prompt":"t","subagent_type":"a"}`),
		Entry(nil, "load_skill", `{"name":"s"}`, "Skill", `{"skill":"s"}`),
		Entry("another LocalAI tool", "generate_image", `{"prompt":"cat"}`, "generate_image", `{"prompt":"cat"}`),
		Entry("arguments that are not an object", "bash", `"x"`, "Bash", `"x"`),
	)

	DescribeTable("round-trips each mapped tool",
		func(name, args string) {
			fn, fa := toolmap.ToFormat(name, json.RawMessage(args))
			calls, err := toolmap.ToLocal(fn, fa)
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(HaveLen(1))
			Expect(calls[0].Name).To(Equal(name))
			Expect(string(calls[0].Args)).To(MatchJSON(args))
		},
		Entry(nil, "bash", `{"command":"ls","timeoutSeconds":30}`),
		Entry(nil, "job", `{"action":"start","command":"sleep 9"}`),
		Entry(nil, "job", `{"action":"read","id":"j1"}`),
		Entry(nil, "job", `{"action":"kill","id":"j1"}`),
		Entry(nil, "read_file", `{"path":"a","offset":1,"limit":2}`),
		Entry(nil, "write_file", `{"path":"a","content":"x"}`),
		Entry(nil, "edit_file", `{"path":"a","old_string":"x","new_string":"y","replace_all":true}`),
		Entry(nil, "list_files", `{"path":"d","pattern":"*.md"}`),
		Entry(nil, "search_files", `{"query":"a.b","regex":true,"path":"d","ignore_case":true}`),
		Entry(nil, "web_fetch", `{"url":"https://example.test"}`),
		Entry(nil, "web_search", `{"query":"go","site":"go.dev"}`),
		Entry(nil, "spawn_agent", `{"task":"t","agent":"a"}`),
		Entry(nil, "load_skill", `{"name":"s"}`),
	)
})

var _ = Describe("LocalNames", func() {
	It("maps the tool lists of agents and drops what LocalAI lacks", func() {
		mapped, dropped := toolmap.LocalNames([]string{
			"Read", "Edit", "MultiEdit", "Bash(git add:*)", "Grep", "Glob", "LS", "TodoWrite",
			"WebFetch(domain:example.test)", "Task", "Skill", "mcp__plugin_fmt_srv__lint", "NotebookEdit", "TodoWrite", " ",
		})
		Expect(mapped).To(Equal([]string{"read_file", "edit_file", "bash", "job", "search_files", "list_files",
			"web_fetch", "spawn_agent", "load_skill", "mcp__plugin_fmt_srv__lint"}))
		Expect(dropped).To(Equal([]string{"TodoWrite", "NotebookEdit"}))
	})

	It("drops names that are not format tools", func() {
		mapped, dropped := toolmap.LocalNames([]string{"generate_image"})
		Expect(mapped).To(BeEmpty())
		Expect(dropped).To(Equal([]string{"generate_image"}))
	})
})

var _ = Describe("FormatMCPName", func() {
	It("gives the format's name of a plugin connector tool", func() {
		Expect(toolmap.FormatMCPName("fmt", "srv", "lint")).To(Equal("mcp__plugin_fmt_srv__lint"))
		Expect(toolmap.FormatMCPName("my.plugin", "a b", "t/x")).To(Equal("mcp__plugin_my_plugin_a_b__t_x"))
		Expect(toolmap.FormatMCPName("p-1", "s_2", "T3")).To(Equal("mcp__plugin_p-1_s_2__T3"))
	})

	It("passes mcp__ names through the maps", func() {
		n := toolmap.FormatMCPName("fmt", "srv", "lint")
		calls, err := toolmap.ToLocal(n, json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(calls[0].Name).To(Equal(n))
		mapped, _ := toolmap.LocalNames([]string{n})
		Expect(mapped).To(Equal([]string{n}))
	})
})
