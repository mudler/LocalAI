package studio

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// ownP is the project the chat of a spec works for.
func ownP(context.Context) string { return "p" }

// twoProjects is what the list action answers before the guard cuts it: the
// chat's own project "p" and another one.
const twoProjects = `{"projects":[{"id":"p","name":"Own","slug":"own","status":"active"},{"id":"other","name":"Secret name","slug":"secret","description":"Secret text","status":"active"}]}`

// stubList is the projects tool of a spec: it answers the two projects for
// list and "ok" for anything else.
func stubList(_ context.Context, args json.RawMessage) (string, error) {
	var in struct{ Action string }
	_ = json.Unmarshal(args, &in)
	if in.Action == "list" {
		return twoProjects, nil
	}
	return "ok", nil
}

var _ = Describe("guardProjects in a run that starts by itself", func() {
	run := func(args string) error {
		tools := guardProjects([]session.Tool{{Name: "projects", Run: stubList}},
			builtins.Chat{Origin: session.OriginTrigger}, false, ownP)
		_, err := tools[0].Run(context.Background(), json.RawMessage(args))
		return err
	}

	It("reads: list, get, agents, connections, triggers, loops, chats and memory run", func() {
		for _, a := range []string{`{"action":"list"}`, `{"action":"get","project":"p"}`, `{"action":"agents","project":"p"}`,
			`{"action":"connections","project":"p"}`, `{"action":"triggers","project":"p"}`, `{"action":"loops","project":"p"}`,
			`{"action":"chats","project":"p"}`, `{"action":"memory","project":"p"}`} {
			Expect(run(a)).To(Succeed(), a)
		}
	})

	It("refuses a write to the memory and a shared project", func() {
		Expect(run(`{"action":"write_memory","project":"p","file":"a.md","content":"x"}`)).To(MatchError(errProjectsUnattend))
		Expect(run(`{"action":"shared_get","project":"s:1"}`)).To(MatchError(errProjectsUnattend))
	})

	It("refuses what changes something", func() {
		Expect(run(`{"action":"archive","project":"p"}`)).To(MatchError(ContainSubstring("nobody watches")))
	})
})

var _ = Describe("guardProjects and the projects shared with the person", func() {
	run := func(chat builtins.Chat, fenced bool, args string) error {
		tools := guardProjects([]session.Tool{{Name: "projects", Run: stubList}},
			chat, fenced, ownP)
		_, err := tools[0].Run(context.Background(), json.RawMessage(args))
		return err
	}
	const shared = `{"action":"shared"}`

	It("lists them in an interactive chat that has no fence", func() {
		Expect(run(builtins.Chat{}, false, shared)).To(Succeed())
	})
	It("does not reveal other people's shares to a fenced chat", func() {
		Expect(run(builtins.Chat{}, true, shared)).To(MatchError(errProjectsFenced))
	})
	It("does not reveal them to a run that starts by itself", func() {
		for _, o := range []string{session.OriginTrigger, session.OriginJob} {
			Expect(run(builtins.Chat{Origin: o}, false, shared)).To(MatchError(errProjectsUnattend), o)
		}
	})
	It("does not offer them to a messaging chat or a sub-agent", func() {
		Expect(run(builtins.Chat{Origin: session.OriginConnector}, false, shared)).To(MatchError(errProjectsMessaging))
		Expect(run(builtins.Chat{SubAgent: true}, false, shared)).To(MatchError(errProjectsMessaging))
	})
})

var _ = Describe("guardProjects in a fenced chat", func() {
	run := func(fenced bool, args string) error {
		tools := guardProjects([]session.Tool{{Name: "projects", Run: stubList}},
			builtins.Chat{}, fenced, ownP)
		_, err := tools[0].Run(context.Background(), json.RawMessage(args))
		return err
	}

	It("reads: list, get, agents, connections, triggers, loops, chats and memory run for its own project", func() {
		for _, a := range []string{`{"action":"list"}`, `{"action":"get","project":"p"}`, `{"action":"agents","project":"p"}`,
			`{"action":"connections","project":"p"}`, `{"action":"triggers","project":"p"}`, `{"action":"loops","project":"p"}`,
			`{"action":"chats","project":"p"}`, `{"action":"memory","project":"p"}`} {
			Expect(run(true, a)).To(Succeed(), a)
		}
	})

	It("refuses the shared projects and a memory write, with the fence as the reason", func() {
		Expect(run(true, `{"action":"shared_get","project":"s:1"}`)).To(MatchError(errProjectsFenced))
		Expect(run(true, `{"action":"write_memory","project":"p","file":"a.md","content":"x"}`)).To(MatchError(errProjectsFenced))
	})

	It("refuses every other action, and an argument list it cannot read", func() {
		for _, a := range []string{"create", "update", "archive", "restore", "delete", "set_chat_project", "pull", "unpull", "nothing"} {
			Expect(run(true, `{"action":"`+a+`","project":"p"}`)).To(MatchError(errProjectsFenced), a)
		}
		Expect(run(true, `not json`)).To(MatchError(errProjectsFenced))
	})

	It("keeps today's rules in a chat that is not fenced", func() {
		for _, a := range []string{"create", "update", "archive", "restore", "memory", "chats", "shared_get"} {
			Expect(run(false, `{"action":"`+a+`","project":"p"}`)).To(Succeed(), a)
		}
		// The model saves a project note with propose_change.
		Expect(run(false, `{"action":"write_memory","project":"p","file":"a.md","content":"x"}`)).To(MatchError(errProjectsMemoryPropose))
		for _, a := range []string{"pull", "unpull", "set_chat_project", "delete"} {
			Expect(run(false, `{"action":"`+a+`","project":"p"}`)).To(MatchError(errProjectsPerson), a)
		}
	})
})

var _ = Describe("guardProjects and the project of the chat", func() {
	detail := []string{"get", "agents", "connections", "triggers", "loops", "chats", "memory"}
	run := func(chat builtins.Chat, fenced bool, own func(context.Context) string, args string) error {
		tools := guardProjects([]session.Tool{{Name: "projects", Run: stubList}}, chat, fenced, own)
		_, err := tools[0].Run(context.Background(), json.RawMessage(args))
		return err
	}
	for _, c := range []struct {
		name   string
		chat   builtins.Chat
		fenced bool
	}{{"a fenced chat", builtins.Chat{}, true}, {"a run that starts by itself", builtins.Chat{Origin: session.OriginTrigger}, false}} {
		It("lets "+c.name+" read the detail of its own project only", func() {
			for _, a := range detail {
				Expect(run(c.chat, c.fenced, ownP, `{"action":"`+a+`","project":"p"}`)).To(Succeed(), a)
				Expect(run(c.chat, c.fenced, ownP, `{"action":"`+a+`","project":"other"}`)).To(MatchError(errProjectsOwnOnly), a)
				Expect(run(c.chat, c.fenced, ownP, `{"action":"`+a+`"}`)).To(MatchError(errProjectsOwnOnly), a+" without a project")
			}
		})
		It("refuses the detail reads of "+c.name+" with no project of its own", func() {
			none := func(context.Context) string { return "" }
			for _, a := range detail {
				Expect(run(c.chat, c.fenced, none, `{"action":"`+a+`","project":"p"}`)).To(MatchError(errProjectsOwnOnly), a)
				Expect(run(c.chat, c.fenced, nil, `{"action":"`+a+`","project":"p"}`)).To(MatchError(errProjectsOwnOnly), a)
			}
		})
		It("shows "+c.name+" its own project in list, and no name or description of another", func() {
			list := func(own func(context.Context) string) string {
				tools := guardProjects([]session.Tool{{Name: "projects", Run: stubList}}, c.chat, c.fenced, own)
				out, err := tools[0].Run(context.Background(), json.RawMessage(`{"action":"list"}`))
				Expect(err).NotTo(HaveOccurred())
				return out
			}
			out := list(ownP)
			Expect(out).To(And(ContainSubstring("Own"), Not(ContainSubstring("Secret")), Not(ContainSubstring("other"))))
			out = list(func(context.Context) string { return "" })
			Expect(out).NotTo(ContainSubstring("Own"))
			Expect(out).NotTo(ContainSubstring("Secret"))
			out = list(nil)
			Expect(out).NotTo(ContainSubstring("Secret"))
		})
	}
	It("shows an interactive chat that is not fenced every project in list", func() {
		tools := guardProjects([]session.Tool{{Name: "projects", Run: stubList}}, builtins.Chat{}, false, nil)
		out, err := tools[0].Run(context.Background(), json.RawMessage(`{"action":"list"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(And(ContainSubstring("Own"), ContainSubstring("Secret name")))
	})
	It("keeps the rules of an interactive chat that is not fenced", func() {
		for _, a := range detail {
			Expect(run(builtins.Chat{}, false, nil, `{"action":"`+a+`","project":"other"}`)).To(Succeed(), a)
		}
	})
})
