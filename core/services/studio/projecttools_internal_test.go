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

var _ = Describe("guardProjects in a run that starts by itself", func() {
	run := func(args string) error {
		tools := guardProjects([]session.Tool{{Name: "projects", Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}},
			builtins.Chat{Origin: session.OriginTrigger}, false, ownP)
		_, err := tools[0].Run(context.Background(), json.RawMessage(args))
		return err
	}

	It("reads: list, get, agents, connections, triggers and loops run", func() {
		for _, a := range []string{`{"action":"list"}`, `{"action":"get","project":"p"}`, `{"action":"agents","project":"p"}`,
			`{"action":"connections","project":"p"}`, `{"action":"triggers","project":"p"}`, `{"action":"loops","project":"p"}`} {
			Expect(run(a)).To(Succeed(), a)
		}
	})

	It("refuses what changes something", func() {
		Expect(run(`{"action":"archive","project":"p"}`)).To(MatchError(ContainSubstring("nobody watches")))
	})
})

var _ = Describe("guardProjects and the projects shared with the person", func() {
	run := func(chat builtins.Chat, fenced bool, args string) error {
		tools := guardProjects([]session.Tool{{Name: "projects", Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}},
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
		tools := guardProjects([]session.Tool{{Name: "projects", Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}},
			builtins.Chat{}, fenced, ownP)
		_, err := tools[0].Run(context.Background(), json.RawMessage(args))
		return err
	}

	It("reads: list, get, agents, connections, triggers and loops run", func() {
		for _, a := range []string{`{"action":"list"}`, `{"action":"get","project":"p"}`, `{"action":"agents","project":"p"}`,
			`{"action":"connections","project":"p"}`, `{"action":"triggers","project":"p"}`, `{"action":"loops","project":"p"}`} {
			Expect(run(true, a)).To(Succeed(), a)
		}
	})

	It("refuses every other action, and an argument list it cannot read", func() {
		for _, a := range []string{"create", "update", "archive", "restore", "delete", "set_chat_project", "pull", "unpull", "nothing"} {
			Expect(run(true, `{"action":"`+a+`","project":"p"}`)).To(MatchError(errProjectsFenced), a)
		}
		Expect(run(true, `not json`)).To(MatchError(errProjectsFenced))
	})

	It("keeps today's rules in a chat that is not fenced", func() {
		for _, a := range []string{"create", "update", "archive", "restore"} {
			Expect(run(false, `{"action":"`+a+`","project":"p"}`)).To(Succeed(), a)
		}
		for _, a := range []string{"pull", "unpull", "set_chat_project", "delete"} {
			Expect(run(false, `{"action":"`+a+`","project":"p"}`)).To(MatchError(errProjectsPerson), a)
		}
	})
})

var _ = Describe("guardProjects and the project of the chat", func() {
	detail := []string{"agents", "connections", "triggers", "loops"}
	run := func(chat builtins.Chat, fenced bool, own func(context.Context) string, args string) error {
		tools := guardProjects([]session.Tool{{Name: "projects", Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}}, chat, fenced, own)
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
		It("refuses the detail reads of "+c.name+" with no project of its own, and keeps list and get", func() {
			none := func(context.Context) string { return "" }
			for _, a := range detail {
				Expect(run(c.chat, c.fenced, none, `{"action":"`+a+`","project":"p"}`)).To(MatchError(errProjectsOwnOnly), a)
				Expect(run(c.chat, c.fenced, nil, `{"action":"`+a+`","project":"p"}`)).To(MatchError(errProjectsOwnOnly), a)
			}
			Expect(run(c.chat, c.fenced, none, `{"action":"list"}`)).To(Succeed())
			Expect(run(c.chat, c.fenced, none, `{"action":"get","project":"other"}`)).To(Succeed())
		})
	}
	It("keeps the rules of an interactive chat that is not fenced", func() {
		for _, a := range detail {
			Expect(run(builtins.Chat{}, false, nil, `{"action":"`+a+`","project":"other"}`)).To(Succeed(), a)
		}
	})
})
