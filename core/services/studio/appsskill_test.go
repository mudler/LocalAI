package studio_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var fenceRE = regexp.MustCompile("(?s)`/workspace/home/pages/reading-tracker/([a-z.]+)`:\n\n```[a-z]*\n(.*?)\n```")

var _ = Describe("the localai-apps skill in a chat", func() {
	It("is offered for a request to make an app, and a model that follows it writes a Page", func() {
		ctx := context.Background()
		skill, err := fs.ReadFile(builtinskills.FS(), "localai-apps/SKILL.md")
		Expect(err).NotTo(HaveOccurred())
		parts := map[string]string{}
		for _, m := range fenceRE.FindAllStringSubmatch(string(skill), -1) {
			parts[m[1]] = m[2]
		}
		Expect(parts).To(HaveKey("page.json"))
		Expect(parts).To(HaveKey("index.html"))

		f := newFiles()
		Expect(f.SyncBuiltin(ctx, builtinskills.FS())).To(Succeed())
		args := func(path, content string) string {
			b, err := json.Marshal(map[string]string{"path": path, "content": content})
			Expect(err).NotTo(HaveOccurred())
			return string(b)
		}
		svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text != "create an app to track my reading" {
				return []studiotest.ToolStep{{Say: "ok"}}
			}
			return []studiotest.ToolStep{
				{Tool: "write_file", Args: args("/workspace/home/pages/reading-tracker/index.html", parts["index.html"])},
				{Tool: "write_file", Args: args("/workspace/home/pages/reading-tracker/page.json", parts["page.json"])},
				{Say: "Done. It is under Pages in the sidebar."},
			}
		}, func(d *studio.Deps) { d.Files = f })
		id := create(svc, alice)
		sent(svc, alice, id, "create an app to track my reading")
		idle(svc, alice, id)

		prompt := h.TurnPrompt(id)
		Expect(prompt).To(ContainSubstring("- localai-apps: Use when the user asks for an app, a tool, a tracker"), "the index:\n%s", prompt)
		Expect(prompt).To(ContainSubstring("/localai-apps"), "the index gives no path to the skill:\n%s", prompt)

		for name, want := range map[string]string{"index.html": parts["index.html"], "page.json": parts["page.json"]} {
			abs, err := f.Abs(alice, "/pages/reading-tracker/"+name)
			Expect(err).NotTo(HaveOccurred())
			rd, _, err := f.Store().Open(ctx, f.Actor(alice), abs)
			Expect(err).NotTo(HaveOccurred(), name)
			got, err := io.ReadAll(rd)
			_ = rd.Close()
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(string(got))).To(Equal(strings.TrimSpace(want)), name)
		}
	})
})
