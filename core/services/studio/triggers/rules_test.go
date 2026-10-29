package triggers_test

import (
	"fmt"
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

func newWorkspace() workspace.Store {
	return openWorkspace(nil)
}

func grant(s workspace.Store, principal, p string, m workspace.Mode) {
	_, err := s.Mkdir(wstest.Ctx(), wstest.AdminActor, p)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.Grant(wstest.Ctx(), wstest.AdminActor, principal, p, m)).To(Succeed())
}

var _ = Describe("Folder rules", func() {
	It("reads the rules outermost first", func() {
		s := newWorkspace()
		grant(s, wstest.Alice.Principal, "/users/alice", workspace.ModeRead)
		wstest.MustPut(s, wstest.AdminActor, "/users/alice/AGENTS.md", "Answer in Italian.")
		wstest.MustPut(s, wstest.AdminActor, "/users/alice/Contracts/AGENTS.md", "Never change a signed file.\n")
		wstest.MustPut(s, wstest.AdminActor, "/users/alice/Contracts/2026/note.txt", "x")
		got, err := triggers.Rules(wstest.Ctx(), s, wstest.Alice, "/users/alice/Contracts/2026")
		Expect(err).NotTo(HaveOccurred())
		want := "# Folder rules: /users/alice\n\nAnswer in Italian.\n\n# Folder rules: /users/alice/Contracts\n\nNever change a signed file."
		Expect(got).To(Equal(want))
	})

	It("leaves out files the actor cannot read or that are missing", func() {
		s := newWorkspace()
		wstest.MustPut(s, wstest.AdminActor, "/AGENTS.md", "root rules, private")
		wstest.MustPut(s, wstest.AdminActor, "/users/alice/AGENTS.md", "alice rules")
		wstest.MustPut(s, wstest.AdminActor, "/users/bob/AGENTS.md", "bob rules")
		_, err := s.Mkdir(wstest.Ctx(), wstest.AdminActor, "/users/alice/Inbox/AGENTS.md") // a folder named like the rules file
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Grant(wstest.Ctx(), wstest.AdminActor, wstest.Alice.Principal, "/users/alice", workspace.ModeRead)).To(Succeed())
		got, err := triggers.Rules(wstest.Ctx(), s, wstest.Alice, "/users/alice/Inbox")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("# Folder rules: /users/alice\n\nalice rules"))
		none, err := triggers.Rules(wstest.Ctx(), s, wstest.Alice, "/users/bob")
		Expect(err).NotTo(HaveOccurred(), "a folder of another user")
		Expect(none).To(BeEmpty(), "a folder of another user")
		none, err = triggers.Rules(wstest.Ctx(), s, wstest.Alice, "/users/alice/nothing/here")
		Expect(err).NotTo(HaveOccurred(), "a folder that does not exist still has the rules above it")
		Expect(none).To(ContainSubstring("alice rules"), "a folder that does not exist still has the rules above it")
	})

	It("reads the rules of the top folder", func() {
		s := newWorkspace()
		wstest.MustPut(s, wstest.AdminActor, "/AGENTS.md", "top")
		got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("# Folder rules: /\n\ntop"))
	})

	It("refuses a bad path", func() {
		s := newWorkspace()
		for _, p := range []string{"", "users", "/a/../b", "/a//b", "/a/"} {
			_, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, p)
			Expect(err).To(HaveOccurred(), "Rules(%q) did not fail", p)
		}
	})

	It("cuts the rules at the limit and keeps the innermost", func() {
		s := newWorkspace()
		big := strings.Repeat("o", 20<<10)
		wstest.MustPut(s, wstest.AdminActor, "/a/AGENTS.md", big)
		wstest.MustPut(s, wstest.AdminActor, "/a/b/AGENTS.md", strings.Repeat("m", 10<<10))
		wstest.MustPut(s, wstest.AdminActor, "/a/b/c/AGENTS.md", strings.Repeat("i", 10<<10))
		got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a/b/c")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("were cut to fit 32 KiB"), "no note about the cut")
		Expect(got).NotTo(ContainSubstring("# Folder rules: /a\n"), "the outer rules were kept")
		Expect(got).NotTo(ContainSubstring("ooo"), "the outer rules were kept")
		Expect(got).To(ContainSubstring("# Folder rules: /a/b\n"), "the inner rules were dropped")
		Expect(got).To(ContainSubstring("# Folder rules: /a/b/c\n"), "the inner rules were dropped")
		Expect(len(got)).To(BeNumerically("<=", triggers.MaxRulesBytes+200))
	})

	It("cuts one rules file over the limit at its end", func() {
		s := newWorkspace()
		// 3-byte characters: a cut inside one must not leave half of it.
		wstest.MustPut(s, wstest.AdminActor, "/a/AGENTS.md", strings.Repeat("€", 20000))
		got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("were cut to fit 32 KiB"), "the cut of one long file: %.80q", got)
		Expect(got).To(ContainSubstring("€€€"), "the cut of one long file: %.80q", got)
		Expect(strings.ContainsRune(got, '\uFFFD')).To(BeFalse(), "a character was cut in half")
		Expect(len(got)).To(BeNumerically("<=", triggers.MaxRulesBytes+200))
	})

	It("keeps invalid bytes out", func() {
		s := newWorkspace()
		wstest.MustPut(s, wstest.AdminActor, "/a/AGENTS.md", "ok \xff\xfe end")
		got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveSuffix("end"))
		Expect(utf8.ValidString(got)).To(BeTrue())
	})

	It("leaves out an empty rules file", func() {
		s := newWorkspace()
		wstest.MustPut(s, wstest.AdminActor, "/a/AGENTS.md", "outer rules")
		wstest.MustPut(s, wstest.AdminActor, "/a/b/AGENTS.md", "")
		wstest.MustPut(s, wstest.AdminActor, "/a/b/c/AGENTS.md", " \n\t\n")
		got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a/b/c")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("# Folder rules: /a\n\nouter rules"))
		wstest.MustPut(s, wstest.AdminActor, "/a/AGENTS.md", "\n")
		got, err = triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a/b/c")
		Expect(err).NotTo(HaveOccurred(), "only empty files")
		Expect(got).To(BeEmpty(), "only empty files")
	})

	It("cuts an innermost file over the limit when an outer one is small", func() {
		s := newWorkspace()
		wstest.MustPut(s, wstest.AdminActor, "/a/AGENTS.md", "small outer rules")
		wstest.MustPut(s, wstest.AdminActor, "/a/b/AGENTS.md", strings.Repeat("i", triggers.MaxRulesBytes+500))
		got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a/b")
		Expect(err).NotTo(HaveOccurred())
		note := fmt.Sprintf("were cut to fit %d KiB", triggers.MaxRulesBytes>>10)
		Expect(got).To(ContainSubstring(note), "the cut of the innermost file: %.200q", got)
		Expect(got).To(ContainSubstring("# Folder rules: /a/b\n"), "the cut of the innermost file: %.200q", got)
		Expect(got).To(ContainSubstring("iii"), "the cut of the innermost file: %.200q", got)
		Expect(got).NotTo(ContainSubstring("small outer rules"), "the outer file was kept next to a cut innermost file")
		body := got[strings.Index(got, "# Folder rules: /a/b"):]
		Expect(len(body)).To(BeNumerically("<=", triggers.MaxRulesBytes), "bytes of rules")
	})
})
