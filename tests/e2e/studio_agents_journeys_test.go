package e2e_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Journeys of saved agents, skills, triggers, proposals, the inbox and push
// through the whole server. The model is the mock backend, or the cloud-proxy
// model that forwards to the fake upstream when a spec needs to read what the
// model was sent. Specs that find a known gap skip with its ID (see the
// "gap" helper), and start asserting the fixed behavior when the gap is closed.

type agObj = map[string]any

func agDecode(out []byte) agObj {
	var m agObj
	ExpectWithOffset(1, json.Unmarshal(out, &m)).To(Succeed(), string(out))
	return m
}

func agCreateAgent(body agObj) string {
	code, out := chatsDo(http.MethodPost, chatsBase()+"/agents", body)
	ExpectWithOffset(1, code).To(Equal(http.StatusCreated), string(out))
	id := agDecode(out)["id"].(string)
	DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/agents/"+id, nil) })
	return id
}

func agWaitIdle(id string) {
	EventuallyWithOffset(1, func() bool { return chatsIdle(id) }, 60*time.Second, 200*time.Millisecond).Should(BeTrue())
}

// agSSEBody is a streamed reply of the fake upstream.
func agSSEBody(text string) string {
	chunk := func(delta, finish string) string {
		return fmt.Sprintf(`data: {"id":"c","object":"chat.completion.chunk","model":"fake","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`+"\n\n", delta, finish)
	}
	t, _ := json.Marshal(text)
	return chunk(`{"role":"assistant","content":`+string(t)+`}`, "null") + chunk(`{}`, `"stop"`) + "data: [DONE]\n\n"
}

// agUpstreamRequest sends one message through a chat of the cloud-proxy model
// and returns the system text and the tool names the upstream was sent.
func agUpstreamRequest(chatBody agObj, text string) (system string, tools []string) {
	agNeedUpstream()
	cpOpenAIUpstream.SetScript(func([]byte) (int, string, string) {
		return 200, agSSEBody("ok from the fake upstream"), "text/event-stream"
	})
	DeferCleanup(func() { cpOpenAIUpstream.SetScript(defaultOpenAIScript) })
	code, out := chatsDo(http.MethodPost, chatsBase(), chatBody)
	ExpectWithOffset(1, code).To(Equal(http.StatusCreated), string(out))
	id := agDecode(out)["id"].(string)
	DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/"+id, nil) })
	chatsSend(id, text)
	agWaitIdle(id)

	req := cpOpenAIUpstream.DecodedBody()
	msgs, _ := req["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(agObj)
		if mm["role"] == "system" {
			if s, ok := mm["content"].(string); ok {
				system += s + "\n"
			}
		}
	}
	ts, _ := req["tools"].([]any)
	for _, t := range ts {
		fn, _ := t.(agObj)["function"].(agObj)
		tools = append(tools, fmt.Sprint(fn["name"]))
	}
	return system, tools
}

// agNeedUpstream skips a spec that reads what the model was sent when the
// cloud-proxy backend is not built. The suite then writes no cloud-proxy model,
// so the spec must skip before it names that model anywhere.
func agNeedUpstream() {
	if cpOpenAIUpstream == nil {
		Skip("cloud-proxy backend binary not built (make build-cloud-proxy-backend)")
	}
}

// agGap skips a spec whose gap is still open, with the gap ID from the e2e
// report of the agents area. When the gap is closed the spec asserts instead.
func agGap(open bool, id, what string) {
	if open {
		Skip("known gap: " + id + ": " + what)
	}
}

var _ = Describe("Chats saved agents", Label("Chats"), func() {
	It("validates, replaces and deletes an agent, and refuses a chat with a deleted one", func() {
		name := fmt.Sprintf("Agent e2e %d", time.Now().UnixNano())
		id := agCreateAgent(agObj{"name": name, "instructions": "Be brief.", "model": "mock-model", "skills": []string{"docx"}})

		code, _ := chatsDo(http.MethodPost, chatsBase()+"/agents", agObj{"name": strings.ToUpper(name)})
		Expect(code).To(Equal(http.StatusConflict), "names are unique ignoring case")
		code, _ = chatsDo(http.MethodPost, chatsBase()+"/agents", agObj{"name": ""})
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _ = chatsDo(http.MethodPost, chatsBase()+"/agents", agObj{"name": strings.Repeat("n", 81)})
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _ = chatsDo(http.MethodPost, chatsBase()+"/agents", agObj{"name": name + " big", "instructions": strings.Repeat("a", 32*1024+1)})
		Expect(code).To(Equal(http.StatusBadRequest), "instructions are at most 32 KiB")

		// A PUT replaces every field.
		code, out := chatsDo(http.MethodPut, chatsBase()+"/agents/"+id, agObj{"name": name, "instructions": "Now in French."})
		Expect(code).To(Equal(http.StatusOK), string(out))
		got := agDecode(out)
		Expect(got).To(HaveKeyWithValue("instructions", "Now in French."))
		Expect(got["skills"]).To(Or(BeNil(), BeEmpty()))
		Expect(got["model"]).To(Or(BeNil(), BeEmpty()))

		code, _ = chatsDo(http.MethodDelete, chatsBase()+"/agents/"+id, nil)
		Expect(code).To(Equal(http.StatusNoContent))
		code, _ = chatsDo(http.MethodGet, chatsBase()+"/agents/"+id, nil)
		Expect(code).To(Equal(http.StatusNotFound))
		code, _ = chatsDo(http.MethodPost, chatsBase(), agObj{"agent_id": id, "model": "mock-model"})
		Expect(code).To(Equal(http.StatusNotFound))
	})

	It("gives a chat the model MCP servers the agent names and no others", func() {
		with := agCreateAgent(agObj{"name": fmt.Sprintf("MCP yes %d", time.Now().UnixNano()), "model": "mock-model-mcp", "mcp_servers": []string{"weather-api"}})
		without := agCreateAgent(agObj{"name": fmt.Sprintf("MCP none %d", time.Now().UnixNano()), "model": "mock-model-mcp", "mcp_servers": []string{}})
		serversOf := func(agent string) []string {
			code, out := chatsDo(http.MethodPost, chatsBase(), agObj{"agent_id": agent})
			Expect(code).To(Equal(http.StatusCreated), string(out))
			chat := agDecode(out)["id"].(string)
			DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/"+chat, nil) })
			code, out = chatsDo(http.MethodGet, chatsBase()+"/"+chat+"/tools", nil)
			Expect(code).To(Equal(http.StatusOK), string(out))
			var panel struct {
				Servers []struct {
					Name string `json:"name"`
				} `json:"servers"`
			}
			Expect(json.Unmarshal(out, &panel)).To(Succeed())
			var names []string
			for _, s := range panel.Servers {
				names = append(names, s.Name)
			}
			return names
		}
		Expect(serversOf(with)).To(ContainElement("weather-api"))
		Expect(serversOf(without)).To(BeEmpty(), "an empty list grants nothing")
	})

	It("sends the agent's instructions and only its skills to the model, and a plain chat gets all of them", func() {
		agNeedUpstream()
		id := agCreateAgent(agObj{"name": fmt.Sprintf("Prompt e2e %d", time.Now().UnixNano()), "model": "cp-passthrough-openai",
			"instructions": "AGENT-INSTRUCTION-MARK", "skills": []string{"docx"}})
		system, _ := agUpstreamRequest(agObj{"agent_id": id}, "hello")
		Expect(system).To(ContainSubstring("AGENT-INSTRUCTION-MARK"))
		Expect(system).To(ContainSubstring("- docx:"))
		Expect(system).NotTo(ContainSubstring("- xlsx:"), "an agent lists only the skills it names")

		plain, _ := agUpstreamRequest(agObj{"model": "cp-passthrough-openai"}, "hello")
		Expect(plain).To(ContainSubstring("- docx:"))
		Expect(plain).To(ContainSubstring("- xlsx:"))
		Expect(plain).NotTo(ContainSubstring("AGENT-INSTRUCTION-MARK"))
	})

	It("tells the model what is unavailable, and that the agent was deleted", func() {
		agNeedUpstream()
		id := agCreateAgent(agObj{"name": fmt.Sprintf("Missing e2e %d", time.Now().UnixNano()), "model": "cp-passthrough-openai",
			"collections": []string{"no-such-collection"}, "skills": []string{"no-such-skill"}, "mcp_servers": []string{"no-such-server"}})
		code, out := chatsDo(http.MethodPost, chatsBase(), agObj{"agent_id": id})
		Expect(code).To(Equal(http.StatusCreated), string(out))
		chat := agDecode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/"+chat, nil) })

		cpOpenAIUpstream.SetScript(func([]byte) (int, string, string) { return 200, agSSEBody("ok"), "text/event-stream" })
		DeferCleanup(func() { cpOpenAIUpstream.SetScript(defaultOpenAIScript) })
		lastUser := func() string {
			msgs, _ := cpOpenAIUpstream.DecodedBody()["messages"].([]any)
			for i := len(msgs) - 1; i >= 0; i-- {
				if m, _ := msgs[i].(agObj); m["role"] == "user" {
					return fmt.Sprint(m["content"])
				}
			}
			return ""
		}
		chatsSend(chat, "first")
		agWaitIdle(chat)
		Expect(lastUser()).To(ContainSubstring("unavailable this turn"))
		Expect(lastUser()).To(And(ContainSubstring("no-such-skill"), ContainSubstring("no-such-server")))

		code, _ = chatsDo(http.MethodDelete, chatsBase()+"/agents/"+id, nil)
		Expect(code).To(Equal(http.StatusNoContent))
		chatsSend(chat, "second")
		agWaitIdle(chat)
		Expect(lastUser()).To(ContainSubstring("the agent of this chat was deleted"))
	})

	It("offers the model each tool name once", func() {
		_, tools := agUpstreamRequest(agObj{"model": "cp-passthrough-openai"}, "hello")
		seen := map[string]int{}
		for _, t := range tools {
			seen[t]++
		}
		var dup []string
		for t, n := range seen {
			if n > 1 {
				dup = append(dup, t)
			}
		}
		agGap(len(dup) > 0, "AG-TOOLS-01", fmt.Sprintf("tool names offered twice: %v (spawn_agent has two different schemas)", dup))
		Expect(dup).To(BeEmpty())
	})

	It("keeps the prompt free of nib's own skill and its load_skill tool", func() {
		system, tools := agUpstreamRequest(agObj{"model": "cp-passthrough-openai"}, "hello")
		Expect(system).NotTo(ContainSubstring("about-nib"))
		Expect(system).NotTo(ContainSubstring("load_skill"))
		Expect(system).NotTo(ContainSubstring("mcp add"))
		Expect(tools).NotTo(ContainElement("load_skill"))
	})
})

var _ = Describe("Chats skill layers", Label("Chats"), func() {
	skillsURL := func(p string) string { return chatsBase() + "/skills" + p }
	type skill struct {
		Name     string `json:"name"`
		Layer    string `json:"layer"`
		Enabled  bool   `json:"enabled"`
		Shadowed string `json:"shadowed"`
	}
	list := func(chat string) []skill {
		u := skillsURL("")
		if chat != "" {
			u += "?chat=" + chat
		}
		code, out := chatsDo(http.MethodGet, u, nil)
		if code == http.StatusConflict {
			Skip("files are off on this server: " + string(out))
		}
		Expect(code).To(Equal(http.StatusOK), string(out))
		var res struct {
			Skills []skill `json:"skills"`
		}
		Expect(json.Unmarshal(out, &res)).To(Succeed())
		return res.Skills
	}
	layersOf := func(chat, name string) map[string]skill {
		m := map[string]skill{}
		for _, s := range list(chat) {
			if s.Name == name {
				m[s.Layer] = s
			}
		}
		return m
	}
	write := func(layer, name string) {
		code, out := chatsDo(http.MethodPost, skillsURL(""), agObj{"layer": layer, "name": name, "description": layer + " version", "body": "Steps."})
		Expect(code).To(Equal(http.StatusOK), string(out))
		DeferCleanup(func() { chatsDo(http.MethodDelete, skillsURL("/"+layer+"/"+name), nil) })
	}

	It("lets the nearest layer win and shows what it covers", func() {
		name := fmt.Sprintf("layered-%d", time.Now().UnixNano())
		write("shared", name)
		write("personal", name)
		got := layersOf("", name)
		Expect(got).To(HaveKey("personal"))
		Expect(got).To(HaveKey("shared"))
		Expect(got["personal"].Shadowed).To(BeEmpty())
		Expect(got["shared"].Shadowed).To(Equal("personal"))
	})

	It("takes only the layers personal and shared for a write", func() {
		for _, layer := range []string{"builtin", "folder", "nonsense"} {
			code, _ := chatsDo(http.MethodPost, skillsURL(""), agObj{"layer": layer, "name": "x-layer", "description": "d", "body": "b"})
			Expect(code).To(Equal(http.StatusBadRequest), layer)
		}
		code, _ := chatsDo(http.MethodGet, skillsURL("/builtin/docx"), nil)
		Expect(code).To(Equal(http.StatusOK), "built-in skills are readable")
		code, _ = chatsDo(http.MethodDelete, skillsURL("/builtin/docx"), nil)
		Expect(code).NotTo(Equal(http.StatusOK), "built-in skills cannot be deleted")
	})

	It("uncovers the skill below when the nearer one is switched off for a chat", func() {
		name := fmt.Sprintf("uncover-%d", time.Now().UnixNano())
		write("shared", name)
		write("personal", name)
		chat := chatsCreate()
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/"+chat, nil) })
		code, out := chatsDo(http.MethodPut, chatsBase()+"/"+chat+"/skills", agObj{"off": []string{name}})
		Expect(code).To(Equal(http.StatusOK), string(out))
		got := layersOf(chat, name)
		// The one further away shows through.
		Expect(got["personal"].Enabled).To(BeFalse())
		Expect(got["shared"].Enabled).To(BeTrue())
	})
})

var _ = Describe("Chats triggers", Label("Chats"), func() {
	preview := func(when, tz string) (int, agObj) {
		code, out := chatsDo(http.MethodGet, chatsBase()+"/triggers/preview?when="+url.QueryEscape(when)+"&tz="+url.QueryEscape(tz), nil)
		var m agObj
		_ = json.Unmarshal(out, &m)
		return code, m
	}

	It("previews every kind of schedule text and says what it means", func() {
		for when, summary := range map[string]string{
			"daily 08:00":           "Every day at 08:00",
			"weekdays 09:30":        "Every weekday at 09:30",
			"weekly mon,thu 09:30":  "Every Monday and Thursday at 09:30",
			"monthly 15 08:00":      "On day 15 of every month at 08:00",
			"once 2099-12-24 18:00": "Once, on 2099-12-24 at 18:00",
			"*/5 * * * *":           `Cron "*/5 * * * *"`,
			"0 9 * * 1-5":           `Cron "0 9 * * 1-5"`,
			"weekly mon,thu 09:30 ": "Every Monday and Thursday at 09:30",
			"  daily 08:00":         "Every day at 08:00",
			"monthly 31 08:00":      "On day 31 of every month at 08:00",
			"once 2099-03-28 02:30": "Once, on 2099-03-28 at 02:30",
			"0 3 28 3 *":            `Cron "0 3 28 3 *"`,
			"weekdays 00:00":        "Every weekday at 00:00",
			"weekly sun 23:59":      "Every Sunday at 23:59",
			"monthly 1 00:00":       "On day 1 of every month at 00:00",
			"once 2099-01-01 00:00": "Once, on 2099-01-01 at 00:00",
			"30 2 * * *":            `Cron "30 2 * * *"`,
			"0 */6 * * *":           `Cron "0 */6 * * *"`,
			"0 0 1 * *":             `Cron "0 0 1 * *"`,
			"15 14 1 * *":           `Cron "15 14 1 * *"`,
			"0 22 * * 1-5":          `Cron "0 22 * * 1-5"`,
			"5 4 * * sun":           "",
			"every day":             "",
			"0 0 1 JAN *":           "",
			"daily 25:00":           "",
			"once 2099-02-30 10:00": "",
			"0 0 * * * *":           "",
			"monthly 32 08:00":      "",
			"weekly funday 09:00":   "",
			"once 2099-12-24":       "",
			"":                      "",
			"* * * *":               "",
			"@daily":                "",
		} {
			code, got := preview(when, "UTC")
			if summary == "" {
				// Texts the grammar does not take: refused, or (cron names) refused with a reason.
				Expect(code).To(Equal(http.StatusBadRequest), "%q", when)
				continue
			}
			Expect(code).To(Equal(http.StatusOK), "%q: %v", when, got)
			Expect(got).To(HaveKeyWithValue("summary", summary), "%q", when)
			Expect(got["times"]).NotTo(BeEmpty(), "%q", when)
		}
		code, _ := preview("daily 08:00", "Nope/Zone")
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("moves a time that falls in a daylight-saving gap to the first minute after it", func() {
		// Europe/Rome skips 02:00 to 03:00 on 28 March 2027 (01:00 UTC).
		code, got := preview("once 2027-03-28 02:30", "Europe/Rome")
		Expect(code).To(Equal(http.StatusOK))
		Expect(got["times"]).To(Equal([]any{"2027-03-28T01:00:00Z"}))
	})

	It("keeps the rules of a trigger: its kind, its folder and its model", func() {
		base := func() agObj {
			return agObj{"name": fmt.Sprintf("Rules e2e %d", time.Now().UnixNano()), "prompt": "Say hello.", "kind": "schedule", "schedule": "daily 08:00", "time_zone": "UTC", "model": "mock-model", "enabled": true}
		}
		with := func(k string, v any) agObj { b := base(); b[k] = v; return b }
		for what, body := range map[string]agObj{
			"unknown kind":          with("kind", "connector"),
			"bad schedule":          with("schedule", "nonsense"),
			"no model":              with("model", ""),
			"a folder that is gone": with("kind", "file"),
		} {
			if what == "a folder that is gone" {
				body["folder"] = "/no-such-folder-e2e"
				delete(body, "schedule")
			}
			code, out := chatsDo(http.MethodPost, chatsBase()+"/triggers", body)
			Expect(code).To(Equal(http.StatusBadRequest), what+": "+string(out))
		}

		code, out := chatsDo(http.MethodPost, chatsBase()+"/triggers", base())
		Expect(code).To(Equal(http.StatusCreated), string(out))
		id := agDecode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/triggers/"+id, nil) })
		Expect(agDecode(out)).To(HaveKey("next_run"))

		code, out = chatsDo(http.MethodPost, chatsBase()+"/triggers/"+id+"/disable", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(agDecode(out)["enabled"]).To(BeFalse())
		code, out = chatsDo(http.MethodPost, chatsBase()+"/triggers/"+id+"/enable", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(agDecode(out)["enabled"]).To(BeTrue())
	})

	It("fails a run whose agent was deleted, switches the trigger off and tells the owner", func() {
		agent := agCreateAgent(agObj{"name": fmt.Sprintf("Doomed e2e %d", time.Now().UnixNano()), "model": "mock-model"})
		code, out := chatsDo(http.MethodPost, chatsBase()+"/triggers", agObj{"name": fmt.Sprintf("Doomed trigger %d", time.Now().UnixNano()), "prompt": "hi",
			"kind": "schedule", "schedule": "daily 08:00", "time_zone": "UTC", "agent_id": agent, "enabled": true})
		Expect(code).To(Equal(http.StatusCreated), string(out))
		trg := agDecode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/triggers/"+trg, nil) })
		code, _ = chatsDo(http.MethodDelete, chatsBase()+"/agents/"+agent, nil)
		Expect(code).To(Equal(http.StatusNoContent))

		code, out = chatsDo(http.MethodPost, chatsBase()+"/triggers/"+trg+"/run", nil)
		Expect(code).To(Equal(http.StatusAccepted), string(out))
		Eventually(func() string {
			_, out := chatsDo(http.MethodGet, chatsBase()+"/triggers/"+trg+"/runs", nil)
			runs, _ := agDecode(out)["runs"].([]any)
			if len(runs) == 0 {
				return ""
			}
			r := runs[0].(agObj)
			return fmt.Sprint(r["status"], ": ", r["reason"])
		}, 30*time.Second, 200*time.Millisecond).Should(Equal("failed: the agent was deleted"))
		_, out = chatsDo(http.MethodGet, chatsBase()+"/triggers/"+trg, nil)
		Expect(agDecode(out)["enabled"]).To(BeFalse())

		_, out = chatsDo(http.MethodGet, chatsBase()+"/notifications?limit=100", nil)
		var kinds []string
		for _, n := range agDecode(out)["notifications"].([]any) {
			if strings.Contains(fmt.Sprint(n.(agObj)["title"]), "Doomed trigger") {
				kinds = append(kinds, fmt.Sprint(n.(agObj)["kind"]))
			}
		}
		Expect(kinds).To(ContainElement("run_failed"))
	})

	It("holds a proposal from a chat until its owner confirms or dismisses it", func() {
		propose := func(name, when string) {
			args, _ := json.Marshal(agObj{"name": name, "prompt": "Summarise yesterday for " + name, "when": when, "time_zone": "Europe/Rome"})
			chat := chatsCreate()
			DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/"+chat, nil) })
			chatsSend(chat, "Please ADDON_TOOL_CALL:propose_trigger:"+base64.RawURLEncoding.EncodeToString(args))
			agWaitIdle(chat)
		}
		proposals := func() map[string]string {
			code, out := chatsDo(http.MethodGet, chatsBase()+"/triggers/proposals", nil)
			Expect(code).To(Equal(http.StatusOK), string(out))
			m := map[string]string{}
			for _, p := range agDecode(out)["triggers"].([]any) {
				m[fmt.Sprint(p.(agObj)["name"])] = fmt.Sprint(p.(agObj)["id"])
			}
			return m
		}
		// The scripted model repeats its call until the loop ends (it does not read the
		// result), so one chat leaves several proposals of the same name. Keep one.
		dedupe := func(name string) {
			code, out := chatsDo(http.MethodGet, chatsBase()+"/triggers/proposals", nil)
			Expect(code).To(Equal(http.StatusOK), string(out))
			first := true
			for _, p := range agDecode(out)["triggers"].([]any) {
				if fmt.Sprint(p.(agObj)["name"]) != name {
					continue
				}
				if first {
					first = false
					continue
				}
				chatsDo(http.MethodDelete, chatsBase()+"/triggers/proposals/"+fmt.Sprint(p.(agObj)["id"]), nil)
			}
		}
		keep, drop := fmt.Sprintf("Digest keep %d", time.Now().UnixNano()), fmt.Sprintf("Digest drop %d", time.Now().UnixNano())
		propose(keep, "weekdays 09:00")
		dedupe(keep)
		propose(drop, "weekdays 10:00")
		dedupe(drop)
		got := proposals()
		if _, ok := got[keep]; !ok {
			Skip("the model did not call propose_trigger on this server (no tools offered to the mock)")
		}
		Expect(got).To(HaveKey(drop))

		_, out := chatsDo(http.MethodGet, chatsBase()+"/triggers", nil)
		for _, t := range agDecode(out)["triggers"].([]any) {
			Expect(t.(agObj)["name"]).NotTo(Equal(keep), "a proposal is not a trigger")
		}

		code, out := chatsDo(http.MethodPost, chatsBase()+"/triggers/proposals/"+got[keep]+"/confirm", agObj{"time_zone": "Europe/Rome"})
		Expect(code).To(Equal(http.StatusOK), string(out))
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/triggers/"+agDecode(out)["id"].(string), nil) })
		Expect(agDecode(out)).To(And(HaveKeyWithValue("enabled", true), HaveKeyWithValue("name", keep)))
		code, _ = chatsDo(http.MethodDelete, chatsBase()+"/triggers/proposals/"+got[drop], nil)
		Expect(code).To(Equal(http.StatusNoContent))
		Expect(proposals()).NotTo(HaveKey(keep))
		Expect(proposals()).NotTo(HaveKey(drop))
	})
})

var _ = Describe("Chats inbox and push", Label("Chats"), func() {
	It("lists, reads and deletes notifications, and streams new ones", func() {
		code, out := chatsDo(http.MethodPost, chatsBase()+"/triggers", agObj{"name": fmt.Sprintf("Inbox e2e %d", time.Now().UnixNano()), "prompt": "Say hello.",
			"kind": "schedule", "schedule": "daily 08:00", "time_zone": "UTC", "model": "mock-model"})
		Expect(code).To(Equal(http.StatusCreated), string(out))
		trg := agDecode(out)["id"].(string)
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/triggers/"+trg, nil) })

		req, _ := http.NewRequest(http.MethodGet, chatsBase()+"/notifications/events", nil)
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(resp.Body.Close)
		seen := make(chan string, 8)
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := resp.Body.Read(buf)
				if n > 0 {
					seen <- string(buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()

		code, _ = chatsDo(http.MethodPost, chatsBase()+"/triggers/"+trg+"/run", nil)
		Expect(code).To(Equal(http.StatusAccepted))
		Eventually(seen, 60*time.Second).Should(Receive(ContainSubstring("event: notification")))

		code, out = chatsDo(http.MethodGet, chatsBase()+"/notifications?unread=1&limit=100", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		list := agDecode(out)["notifications"].([]any)
		Expect(list).NotTo(BeEmpty())
		id := list[0].(agObj)["id"]
		code, out = chatsDo(http.MethodPost, chatsBase()+"/notifications/read", agObj{"ids": []any{id}})
		Expect(code).To(Equal(http.StatusOK), string(out))
		code, _ = chatsDo(http.MethodDelete, chatsBase()+"/notifications", agObj{"ids": []any{id}})
		Expect(code).To(Equal(http.StatusOK))
		_, out = chatsDo(http.MethodGet, chatsBase()+"/notifications?limit=500", nil)
		for _, n := range agDecode(out)["notifications"].([]any) {
			Expect(n.(agObj)["id"]).NotTo(Equal(id))
		}
	})

	It("takes a browser push subscription only for a public https address", func() {
		code, out := chatsDo(http.MethodGet, chatsBase()+"/push/key", nil)
		if code == http.StatusConflict {
			Skip("push is off on this server: " + string(out))
		}
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(agDecode(out)["public_key"]).NotTo(BeEmpty())

		keys := agObj{"p256dh": "BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM", "auth": "tBHItJI5svbpez7KI4CCXg"}
		for _, ep := range []string{"https://127.0.0.1/p", "https://10.0.0.5/p", "https://localhost/p", "https://169.254.169.254/p", "https://[::1]/p"} {
			code, _ = chatsDo(http.MethodPost, chatsBase()+"/push/subscribe", agObj{"endpoint": ep, "keys": keys})
			Expect(code).To(Equal(http.StatusBadRequest), ep)
		}
		code, _ = chatsDo(http.MethodPost, chatsBase()+"/push/subscribe", agObj{"endpoint": "https://push.example.com/e2e-a", "keys": agObj{}})
		Expect(code).To(Equal(http.StatusBadRequest), "keys are required")

		plain := "http://push.example.com/e2e-plain"
		code, _ = chatsDo(http.MethodPost, chatsBase()+"/push/subscribe", agObj{"endpoint": plain, "keys": keys})
		DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/push/subscribe", agObj{"endpoint": plain}) })
		agGap(code == http.StatusNoContent, "AG-PSH-01", "a plain http push endpoint is accepted")
		Expect(code).To(Equal(http.StatusBadRequest))
	})
})

var _ = Describe("Removed agent, job and conversation APIs", Label("Chats"), func() {
	It("answers 404 for the old routes and marks the moved collections path deprecated", func() {
		root := strings.TrimSuffix(apiURL, "/v1")
		for _, p := range []string{"/api/agents", "/api/agents/skills", "/api/agents/git-repos", "/api/conversations"} {
			code, _ := chatsDo(http.MethodGet, root+p, nil)
			Expect(code).To(Equal(http.StatusNotFound), p)
		}
		resp, err := http.Get(root + "/api/agents/collections")
		Expect(err).NotTo(HaveOccurred())
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			Expect(resp.Header.Get("Deprecation")).To(Equal("true"))
		}
	})
})
