package studio_test

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func writeHome(f *files.Service, who, rel, body string) {
	Expect(f.EnsureHome(context.Background(), who)).To(Succeed())
	abs, err := f.Abs(who, rel)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.Store().Put(context.Background(), f.Actor(who), abs, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("persona and user files in the turn prompt", func() {
	It("adds nothing when the user has neither file", func() {
		svc, h, _ := newSkillChat()
		id := create(svc, alice)
		sent(svc, alice, id, "hello")
		idle(svc, alice, id)
		Expect(h.TurnPrompt(id)).NotTo(ContainSubstring("Persona"))
		Expect(h.TurnPrompt(id)).NotTo(ContainSubstring("About the user"))
	})

	It("puts the persona after the chat's own prompt, and applies a change at the next turn", func() {
		svc, h, f := newSkillChat()
		writeSkill(f, alice, "/.skills/review/SKILL.md", "review", "check a change")
		writeHome(f, alice, "/persona.json", `{"name":"Ada","system_prompt":"Answer in one line."}`)
		writeHome(f, alice, "/user.json", `{"name":"Marta","timezone":"Europe/Rome"}`)
		id := create(svc, alice)
		sp := "CHAT PROMPT"
		_, err := svc.Update(context.Background(), alice, id, studio.UpdateInput{Settings: &session.ChatSettings{SystemPrompt: sp}})
		Expect(err).NotTo(HaveOccurred())
		sent(svc, alice, id, "one")
		idle(svc, alice, id)
		p := h.TurnPrompt(id)
		Expect(p).To(ContainSubstring("Answer in one line."))
		Expect(p).To(ContainSubstring("Europe/Rome"))
		Expect(strings.Index(p, sp)).To(BeNumerically("<", strings.Index(p, "Answer in one line.")), p)
		Expect(strings.Index(p, "Answer in one line.")).To(BeNumerically("<", strings.Index(p, "check a change")), p)

		writeHome(f, alice, "/persona.json", `{"name":"Bea","system_prompt":"Use bullet points."}`)
		sent(svc, alice, id, "two")
		idle(svc, alice, id)
		p = h.TurnPrompt(id)
		Expect(p).To(ContainSubstring("Use bullet points."))
		Expect(p).NotTo(ContainSubstring("Answer in one line."))
	})

	It("keeps one user's persona out of another user's chat", func() {
		svc, h, f := newSkillChat()
		writeHome(f, alice, "/persona.json", `{"name":"Ada","system_prompt":"ALICE ONLY"}`)
		id := create(svc, "bob")
		sent(svc, "bob", id, "hi")
		idle(svc, "bob", id)
		Expect(h.TurnPrompt(id)).NotTo(ContainSubstring("ALICE ONLY"))
	})
})
