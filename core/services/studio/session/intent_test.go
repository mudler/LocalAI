package session_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("calls whose outcome is not known", func() {
	user := session.Entry{Role: session.RoleUser, Text: "deploy it"}
	intent := func(tool, args string) session.Entry {
		return session.Entry{Role: session.RoleToolIntent, Text: tool, Note: args}
	}
	result := func(tool string) session.Entry {
		return session.Entry{Role: session.RoleTool, Text: tool + ": ok\ndone"}
	}

	It("finds an intent that has no result after it", func() {
		got := session.UnresolvedIntents([]session.Entry{user, intent("deploy", `{"env":"prod"}`)})
		Expect(got).To(Equal([]session.Intent{{Tool: "deploy", Args: `{"env":"prod"}`}}))
	})

	It("matches each result to one intent of the same tool", func() {
		es := []session.Entry{user, intent("send_message", "a"), result("send_message"), intent("send_message", "b"), intent("deploy", "c"), result("deploy")}
		Expect(session.UnresolvedIntents(es)).To(Equal([]session.Intent{{Tool: "send_message", Args: "b"}}))
	})

	It("looks only at the turns since the last message of the person, and since a clear", func() {
		es := []session.Entry{user, intent("deploy", "old"), {Role: session.RoleUser, Text: "again"}, intent("write_x", "new")}
		Expect(session.UnresolvedIntents(es)).To(Equal([]session.Intent{{Tool: "write_x", Args: "new"}}))
		es = []session.Entry{user, intent("deploy", "old"), {Role: session.RoleCleared}}
		Expect(session.UnresolvedIntents(es)).To(BeEmpty())
	})

	It("says nothing for a transcript with no intent, and a result of another tool resolves nothing", func() {
		Expect(session.UnresolvedIntents([]session.Entry{user, result("read_file")})).To(BeEmpty())
		es := []session.Entry{user, intent("deploy", ""), result("read_file")}
		Expect(session.UnresolvedIntents(es)).To(HaveLen(1))
	})

	It("is internal: not a message, and not shown", func() {
		Expect(session.DisplayOnly(session.RoleToolIntent)).To(BeTrue())
		Expect(session.ModelRole(session.RoleToolIntent)).To(BeFalse())
	})

	It("lists the tools that only read, and nothing that writes", func() {
		for _, n := range []string{"read", "grep", "read_file", "search_collection", "web_fetch", "search_chats"} {
			Expect(session.ReadOnlyTool(n)).To(BeTrue(), n)
		}
		for _, n := range []string{"bash", "write", "edit", "write_file", "delete_file", "send_message", "spawn_agent", "propose_change", "update_trigger"} {
			Expect(session.ReadOnlyTool(n)).To(BeFalse(), n)
		}
	})
})
