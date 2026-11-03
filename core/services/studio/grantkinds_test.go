package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func pageContent(files map[string]string) string {
	b, _ := json.Marshal(map[string]any{"files": files})
	return string(b)
}

var _ = Describe("skill, page and dashboard proposals", func() {
	ctx := context.Background()
	var (
		r  *trigRig
		ag agents.Agent
	)
	BeforeEach(func() {
		r = changeShared(nil).rig(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, studio.TriggerOptions{})
		var err error
		ag, err = r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Maker", Model: "m",
			Builtins: []string{"skills.write", "pages.write", "dashboard.write"}})
		Expect(err).NotTo(HaveOccurred())
	})
	propose := func(kind, scope, target, content string) (contract.ChangeProposal, error) {
		return r.svc.Changes().Propose(ctx, studio.ProposeInput{User: alice, Agent: ag.ID, Kind: kind, Scope: scope, Target: target, Content: content, Summary: "s"})
	}
	write := func(rel, text string) {
		_, err := r.sh.files.WriteHome(ctx, alice, rel, text, "seed", 1<<20)
		Expect(err).NotTo(HaveOccurred())
	}

	Describe("a page", func() {
		good := map[string]string{"index.html": "<h1>Hi</h1>", "page.json": `{"title":"Hi","icon":"home","widget":{"size":"small"}}`, "data/items.json": "[]"}

		It("is written as one change set after the owner confirms, and one undo removes it", func() {
			p, err := propose("page", "user", "hello", pageContent(good))
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Target).To(Equal("pages/hello"))
			_, ok := readHome(r, "pages/hello/index.html")
			Expect(ok).To(BeFalse())
			done, err := r.svc.Changes().Confirm(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			for rel, text := range good {
				got, ok := readHome(r, "pages/hello/"+rel)
				Expect(ok).To(BeTrue(), rel)
				Expect(got).To(Equal(text))
			}
			Expect(done.Rev).To(BeNumerically(">", 0))
			_, err = r.svc.Changes().Revert(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			for rel := range good {
				_, ok := readHome(r, "pages/hello/"+rel)
				Expect(ok).To(BeFalse(), rel+" survived the undo")
			}
		})

		DescribeTable("refuses a bad page",
			func(target, scope string, files map[string]string) {
				_, err := propose("page", scope, target, pageContent(files))
				Expect(err).To(MatchError(studio.ErrBadChange))
				list, _ := r.svc.Changes().List(ctx, alice)
				Expect(list).To(BeEmpty())
			},
			Entry("a name with upper case", "Hello", "user", good),
			Entry("a name with a slash", "a/b", "user", good),
			Entry("a name that climbs", "..", "user", good),
			Entry("no name", "", "user", good),
			Entry("the agent scope", "hello", "agent", good),
			Entry("the channel scope", "hello", "channel", good),
			Entry("no index.html", "hello", "user", map[string]string{"page.json": "{}"}),
			Entry("an empty index.html", "hello", "user", map[string]string{"index.html": "  "}),
			Entry("a parent path", "hello", "user", map[string]string{"index.html": "x", "../evil.html": "x"}),
			Entry("a nested parent path", "hello", "user", map[string]string{"index.html": "x", "a/../../evil": "x"}),
			Entry("an absolute path", "hello", "user", map[string]string{"index.html": "x", "/etc/passwd": "x"}),
			Entry("a backslash path", "hello", "user", map[string]string{"index.html": "x", `a\b`: "x"}),
			Entry("an empty segment", "hello", "user", map[string]string{"index.html": "x", "a//b": "x"}),
			Entry("a dot segment", "hello", "user", map[string]string{"index.html": "x", "./a": "x"}),
			Entry("a file that is also a folder", "hello", "user", map[string]string{"index.html": "x", "a": "x", "a/b": "x"}),
			Entry("two spellings of one file", "hello", "user", map[string]string{"index.html": "x", "A.js": "x", "a.js": "x"}),
			Entry("a binary file", "hello", "user", map[string]string{"index.html": "x", "a.bin": "a\x00b"}),
			Entry("page.json that is not an object", "hello", "user", map[string]string{"index.html": "x", "page.json": "[1]"}),
			Entry("page.json with a bad widget size", "hello", "user", map[string]string{"index.html": "x", "page.json": `{"widget":{"size":"huge"}}`}),
			Entry("page.json with a long title", "hello", "user", map[string]string{"index.html": "x", "page.json": `{"title":"` + strings.Repeat("t", 121) + `"}`}),
			Entry("page.json with a bad icon", "hello", "user", map[string]string{"index.html": "x", "page.json": `{"icon":"Bad Icon"}`}),
		)

		It("refuses content that is not a files object, too many files and more than 256 KiB", func() {
			for name, content := range map[string]string{
				"plain html": "<h1>hi</h1>",
				"no files":   `{"files":{}}`,
				"extra key":  `{"files":{"index.html":"x"},"run":"rm"}`,
				"two docs":   `{"files":{"index.html":"x"}} {"files":{}}`,
			} {
				_, err := propose("page", "user", "hello", content)
				Expect(err).To(MatchError(studio.ErrBadChange), name)
			}
			many := map[string]string{"index.html": "x"}
			for i := range 32 {
				many[fmt.Sprintf("f%d.json", i)] = "{}"
			}
			_, err := propose("page", "user", "hello", pageContent(many))
			Expect(err).To(MatchError(studio.ErrBadChange))
			_, err = propose("page", "user", "hello", pageContent(map[string]string{"index.html": strings.Repeat("a", contract.MaxPageChangeBytes)}))
			Expect(err).To(MatchError(studio.ErrBadChange), "the JSON wrapper pushes it over the limit")
			_, err = propose("page", "user", "hello", pageContent(map[string]string{"index.html": strings.Repeat("a", contract.MaxPageChangeBytes-1024)}))
			Expect(err).NotTo(HaveOccurred())
		})

		It("is stale when a file of the page changed after the proposal", func() {
			write("pages/hello/index.html", "<h1>old</h1>")
			p, err := propose("page", "user", "hello", pageContent(good))
			Expect(err).NotTo(HaveOccurred())
			Expect(p.BaseHash).NotTo(BeEmpty())
			write("pages/hello/index.html", "<h1>hand made</h1>")
			_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
			Expect(err).To(MatchError(studio.ErrTargetChanged))
			got, _ := readHome(r, "pages/hello/index.html")
			Expect(got).To(Equal("<h1>hand made</h1>"))
		})

		It("refuses the owner's edit when it breaks the rules", func() {
			p, err := propose("page", "user", "hello", pageContent(good))
			Expect(err).NotTo(HaveOccurred())
			_, err = r.svc.Changes().ConfirmEdited(ctx, alice, p.ID, pageContent(map[string]string{"page.json": "{}"}))
			Expect(err).To(MatchError(studio.ErrBadChange))
		})
	})

	Describe("the dashboard", func() {
		ok := `{"widgets":[{"id":"c","type":"chats","title":"Chats"}]}`

		It("writes LocalAI/home.json after the owner confirms and undoes it", func() {
			p, err := propose("dashboard", "user", "", ok)
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Target).To(Equal("LocalAI/home.json"))
			_, found := readHome(r, "LocalAI/home.json")
			Expect(found).To(BeFalse())
			_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			text, _ := readHome(r, "LocalAI/home.json")
			Expect(text).To(Equal(ok))
			_, err = r.svc.Changes().Revert(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			_, found = readHome(r, "LocalAI/home.json")
			Expect(found).To(BeFalse())
		})

		DescribeTable("refuses a file that breaks the schema",
			func(scope, content string) {
				_, err := propose("dashboard", scope, "", content)
				Expect(err).To(MatchError(studio.ErrBadChange))
			},
			Entry("not JSON", "user", "widgets"),
			Entry("not an object", "user", "[]"),
			Entry("an unknown widget type", "user", `{"widgets":[{"id":"c","type":"rocket","title":"x"}]}`),
			Entry("a page widget with a bad page name", "user", `{"widgets":[{"id":"p","type":"page","page":"Bad Name"}]}`),
			Entry("too many page widgets", "user", `{"widgets":[`+strings.TrimSuffix(strings.Repeat(`{"id":"p1","type":"page","page":"news"},`, 4), ",")+`]}`),
			Entry("the agent scope", "agent", ok),
			Entry("the channel scope", "channel", ok),
		)

		It("is checked again when the owner edits it", func() {
			p, err := propose("dashboard", "user", "", ok)
			Expect(err).NotTo(HaveOccurred())
			_, err = r.svc.Changes().ConfirmEdited(ctx, alice, p.ID, `{"widgets":[{"id":"p","type":"page","page":"Bad Name"}]}`)
			Expect(err).To(MatchError(studio.ErrBadChange))
		})
	})

	Describe("a skill", func() {
		Describe("of the personal layer", func() {
			It("is written to .skills when the owner confirms", func() {
				p, err := propose("skill", "user", "daily-note", skillText)
				Expect(err).NotTo(HaveOccurred())
				Expect(p.Target).To(Equal(".skills/daily-note/SKILL.md"))
				_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
				Expect(err).NotTo(HaveOccurred())
				text, _ := readHome(r, ".skills/daily-note/SKILL.md")
				Expect(text).To(Equal(skillText))
			})

			It("is refused when the owner locked the existing skill", func() {
				write(".skills/daily-note/SKILL.md", "---\nname: daily-note\ndescription: Mine.\nlocked: true\n---\nMy steps.\n")
				_, err := propose("skill", "user", "daily-note", skillText)
				Expect(err).To(MatchError(studio.ErrBadChange))
				Expect(err.Error()).To(ContainSubstring("locked"))
			})

			It("is refused at confirm when the owner locks it after the proposal", func() {
				write(".skills/daily-note/SKILL.md", "---\nname: daily-note\ndescription: Mine.\n---\nMy steps.\n")
				p, err := propose("skill", "user", "daily-note", skillText)
				Expect(err).NotTo(HaveOccurred())
				// The owner marks the skill as locked, which changes the file, so the
				// base hash moves as well: either answer keeps the file as it is.
				write(".skills/daily-note/SKILL.md", "---\nname: daily-note\ndescription: Mine.\nlocked: true\n---\nMy steps.\n")
				_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
				Expect(err).To(HaveOccurred())
				text, _ := readHome(r, ".skills/daily-note/SKILL.md")
				Expect(text).To(ContainSubstring("My steps."))
			})

			It("may change a skill that is not locked", func() {
				write(".skills/daily-note/SKILL.md", "---\nname: daily-note\ndescription: Mine.\nlocked: false\n---\nOld.\n")
				_, err := propose("skill", "user", "daily-note", skillText)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("of the agent layer", func() {
			It("is written under the folder of the agent", func() {
				p, err := propose("skill", "agent", "daily-note", skillText)
				Expect(err).NotTo(HaveOccurred())
				Expect(p.Target).To(Equal("LocalAI/agents/" + ag.ID + "/skills/daily-note/SKILL.md"))
				_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
				Expect(err).NotTo(HaveOccurred())
			})

			It("cannot replace a personal skill the owner locked", func() {
				write(".skills/daily-note/SKILL.md", "---\nname: daily-note\ndescription: Mine.\nlocked: \"true\"\n---\nMy steps.\n")
				_, err := propose("skill", "agent", "daily-note", skillText)
				Expect(err).To(MatchError(studio.ErrBadChange))
				Expect(err.Error()).To(ContainSubstring("locked"))
			})
		})

		DescribeTable("refuses a bad skill",
			func(scope, target, content string) {
				_, err := propose("skill", scope, target, content)
				Expect(err).To(MatchError(studio.ErrBadChange))
			},
			Entry("the channel scope", "channel", "daily-note", skillText),
			Entry("a name with upper case", "user", "Daily", strings.Replace(skillText, "daily-note", "Daily", 2)),
			Entry("a path as the name", "user", "../x", skillText),
			Entry("no name", "user", "", skillText),
			Entry("no frontmatter", "user", "daily-note", "just text"),
			Entry("no description", "user", "daily-note", "---\nname: daily-note\n---\nbody\n"),
			Entry("a name that differs from the target", "user", "other-note", skillText),
		)

		It("is not offered for a shared or built-in layer: the only places are the personal and the agent layer", func() {
			for _, scope := range []string{"shared", "builtin", "team"} {
				_, err := propose("skill", scope, "daily-note", skillText)
				Expect(err).To(MatchError(studio.ErrBadChange), scope)
			}
		})
	})
})
