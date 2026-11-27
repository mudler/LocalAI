package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/skills"
)

var _ = Describe("the skills list of a chat", func() {
	It("maps the plugin fields of a skill of the plugin layer, and leaves them out of the others", func() {
		c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/chats/skills?chat=c1", nil), httptest.NewRecorder())
		list := []skills.Skill{
			{Name: "fmt:lint", Description: "Lint a file", Layer: skills.LayerPlugin,
				GuestPath: "/workspace/shared-plugins/ab/skills/lint/SKILL.md",
				Plugin:    "fmt@tools", PluginTitle: "fmt", PluginInstall: "in-1",
				NoModel: true, NoMenu: true, ArgumentHint: "<file>"},
			{Name: "notes", Layer: skills.LayerShared, GuestPath: "/workspace/shared-skills/notes/SKILL.md"},
		}
		out := (&Handlers{}).skillList(c, fileCall{}, list, nil)
		b, err := json.Marshal(out)
		Expect(err).NotTo(HaveOccurred())
		var got struct {
			Skills []map[string]any `json:"skills"`
		}
		Expect(json.Unmarshal(b, &got)).To(Succeed())
		Expect(got.Skills).To(HaveLen(2))
		p := got.Skills[0]
		Expect(p).To(SatisfyAll(
			HaveKeyWithValue("id", "plugin:fmt:lint"),
			HaveKeyWithValue("name", "fmt:lint"),
			HaveKeyWithValue("layer", "plugin"),
			HaveKeyWithValue("path", "/workspace/shared-plugins/ab/skills/lint/SKILL.md"),
			HaveKeyWithValue("plugin", "fmt@tools"),
			HaveKeyWithValue("plugin_title", "fmt"),
			HaveKeyWithValue("plugin_install", "in-1"),
			HaveKeyWithValue("no_model", true),
			HaveKeyWithValue("no_menu", true),
			HaveKeyWithValue("argument_hint", "<file>"),
			HaveKeyWithValue("editable", false),
			HaveKeyWithValue("enabled", true),
		))
		for _, k := range []string{"plugin", "plugin_title", "plugin_install", "no_model", "no_menu", "argument_hint"} {
			Expect(got.Skills[1]).NotTo(HaveKey(k), "a skill of another layer has no %s", k)
		}
	})
})
