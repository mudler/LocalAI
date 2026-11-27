package skills

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("the plugin layer", func() {
	const pkg = "/plugins/abc123"
	var (
		st    workspace.Store
		roots []Root
	)
	pluginRoot := func(entries ...PluginSkill) Root {
		return Root{Layer: LayerPlugin, Plugin: "fmt@tools", PluginTitle: "fmt", PluginInstall: "in1",
			Path: pkg, Guest: "/workspace/shared-plugins/abc123", PluginSkills: entries}
	}
	BeforeEach(func() {
		st = newStore()
		grant(st, "alice", "/users/alice", workspace.ModeWrite)
		grant(st, "alice", "/skills", workspace.ModeRead)
		grant(st, "alice", "/addon-skills", workspace.ModeRead)
		grant(st, "alice", "/builtin-skills", workspace.ModeRead)
		put(st, "/skills/style/SKILL.md", skillText("style", "shared style"))
		put(st, "/addon-skills/office/xlsx/SKILL.md", skillText("xlsx", "office xlsx"))
		put(st, "/builtin-skills/docx/SKILL.md", skillText("docx", "built-in docx"))
		roots = []Root{
			{Layer: LayerBuiltin, Path: "/builtin-skills", Guest: "/workspace/shared-builtin-skills"},
			{Layer: LayerAddon, Addon: "office", AddonTitle: "Office", Path: "/addon-skills/office", Guest: "/workspace/shared-addon-skills/office"},
			{Layer: LayerShared, Path: "/skills", Guest: "/workspace/shared-skills"},
		}
	})
	resolve := func(o Options, extra ...Root) []Skill {
		list, _, err := Resolve(context.Background(), st, alice, append(append([]Root{}, roots...), extra...), o)
		Expect(err).NotTo(HaveOccurred())
		return list
	}

	It("lists the skills of a plugin under qualified names, between the shared and the add-on layers", func() {
		list := resolve(Options{}, pluginRoot(
			PluginSkill{Name: "lint", Description: "Lint the code.", Dir: "skills/lint"},
			PluginSkill{Name: "docx", Description: "Another docx.", Dir: "skills/docx"},
		))
		var order []string
		for _, sk := range list {
			order = append(order, string(sk.Layer)+"/"+sk.Name)
		}
		Expect(order).To(Equal([]string{"shared/style", "plugin/fmt:docx", "plugin/fmt:lint", "addon/xlsx", "builtin/docx"}))
		lint := list[2]
		Expect(lint.Plugin).To(Equal("fmt@tools"))
		Expect(lint.PluginTitle).To(Equal("fmt"))
		Expect(lint.PluginInstall).To(Equal("in1"))
		Expect(lint.Path).To(Equal(pkg + "/skills/lint"))
		Expect(lint.GuestPath).To(Equal("/workspace/shared-plugins/abc123/skills/lint/SKILL.md"))
		Expect(lint.PluginRoot).To(Equal("/workspace/shared-plugins/abc123"))
		for _, sk := range list {
			Expect(sk.Shadowed).To(BeEmpty(), "a plugin skill shadows nothing and is shadowed by nothing: %s", sk.Name)
		}
	})

	It("accepts a qualified name only in the plugin layer", func() {
		Expect(ValidNameIn(LayerPlugin, "fmt:lint")).To(BeTrue())
		Expect(ValidNameIn(LayerPlugin, "fmt:db:migrate")).To(BeFalse())
		Expect(ValidNameIn(LayerPlugin, "lint")).To(BeFalse())
		Expect(ValidNameIn(LayerShared, "fmt:lint")).To(BeFalse())
		Expect(ValidNameIn(LayerShared, "lint")).To(BeTrue())
		Expect(ValidName("fmt:lint")).To(BeFalse())

		list := resolve(Options{}, pluginRoot(PluginSkill{Name: "Bad Name", Description: "x", Dir: "skills/bad"}))
		var bad Skill
		for _, sk := range list {
			if sk.Layer == LayerPlugin {
				bad = sk
			}
		}
		Expect(bad.Invalid).To(Equal(ErrBadName.Error()))
	})

	It("refuses a plugin skill folder that leaves the package", func() {
		list := resolve(Options{}, pluginRoot(PluginSkill{Name: "x", Description: "x", Dir: "../other"}))
		for _, sk := range list {
			if sk.Layer == LayerPlugin {
				Expect(sk.Invalid).NotTo(BeEmpty())
			}
		}
	})

	It("lets the Off option switch one plugin skill off by its layer ID", func() {
		list := resolve(Options{Off: func(sk Skill) bool { return string(sk.Layer)+":"+sk.Name == "plugin:fmt:lint" }},
			pluginRoot(PluginSkill{Name: "lint", Description: "Lint.", Dir: "skills/lint"},
				PluginSkill{Name: "fix", Description: "Fix.", Dir: "skills/fix"}))
		names := Names(list)
		Expect(names).To(ContainElement("fmt:fix"))
		Expect(names).NotTo(ContainElement("fmt:lint"))
	})

	It("keeps a skill the model may not start out of the index, and in the usable list", func() {
		list := resolve(Options{}, pluginRoot(
			PluginSkill{Name: "deploy", Description: "Deploy.", Dir: "skills/deploy", NoModel: true},
			PluginSkill{Name: "lint", Description: "Lint.", Dir: "skills/lint", NoMenu: true, Requires: []string{RequireExec}, ArgumentHint: "[path]"},
		))
		idx := Index(list)
		Expect(idx).NotTo(ContainSubstring("fmt:deploy"))
		Expect(idx).To(ContainSubstring("- fmt:lint: Lint."))
		sk, ok := Find(list, "fmt:deploy")
		Expect(ok).To(BeTrue())
		Expect(sk.NoModel).To(BeTrue())
		lint, _ := Find(list, "fmt:lint")
		Expect(lint.NoMenu).To(BeTrue())
		Expect(lint.Requires).To(Equal([]string{RequireExec}))
		Expect(lint.ArgumentHint).To(Equal("[path]"))
	})

	It("gives a skill with no description a short stand-in, so it still shows", func() {
		list := resolve(Options{}, pluginRoot(PluginSkill{Name: "lint", Dir: "skills/lint"}))
		sk, ok := Find(list, "fmt:lint")
		Expect(ok).To(BeTrue())
		Expect(strings.TrimSpace(sk.Description)).NotTo(BeEmpty())
	})
})
