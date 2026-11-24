package studio_test

import (
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the card of a file change", func() {
	It("names the change set and the path of a write, so the chat can show it with Undo", func() {
		f := newFiles()
		svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "save" {
				return []studiotest.ToolStep{
					{Tool: "write_file", Args: `{"path":"/workspace/home/persona.json","content":"{\"name\":\"Ada\"}"}`},
					{Tool: "list_files", Args: `{"path":"/"}`},
					{Tool: "edit_file", Args: `{"path":"/persona.json","old_string":"Ada","new_string":"Grace"}`},
					{Say: "Saved."},
				}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) { d.Files = f })
		id := create(svc, alice)
		sent(svc, alice, id, "save")
		idle(svc, alice, id)

		tools := toolEntries(svc, alice, id)
		Expect(tools).To(HaveLen(3), "%+v", tools)
		write, list, edit := tools[0], tools[1], tools[2]
		Expect(write.Card).NotTo(BeNil(), "%+v", write)
		Expect(write.Card.Kind).To(Equal(files.ChangeCardKind))
		rev, path, ok := strings.Cut(write.Card.ID, ":")
		Expect(ok).To(BeTrue(), write.Card.ID)
		Expect(path).To(Equal("/persona.json"))
		n, err := strconv.ParseInt(rev, 10, 64)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeNumerically(">", 0))
		Expect(list.Card).To(BeNil(), "a read changes nothing")
		Expect(edit.Card).NotTo(BeNil(), "%+v", edit)
		Expect(edit.Card.Kind).To(Equal(files.ChangeCardKind))
		erev, epath, _ := strings.Cut(edit.Card.ID, ":")
		Expect(epath).To(Equal("/persona.json"))
		Expect(erev).NotTo(Equal(rev), "an edit is a change set of its own, undone on its own")
	})
})
