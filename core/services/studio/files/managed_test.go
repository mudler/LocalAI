package files_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
)

var _ = Describe("Managed", func() {
	It("marks the folders LocalAI creates in a home, by what each one holds", func() {
		Expect(files.Managed("/LocalAI")).To(Equal(files.ManagedSettings))
		Expect(files.Managed("/LocalAI/persona.json")).To(Equal(files.ManagedSettings))
		Expect(files.Managed("/Chats")).To(Equal(files.ManagedChats))
		Expect(files.Managed("/Memory")).To(Equal(files.ManagedMemory))
		Expect(files.Managed("/pages")).To(Equal(files.ManagedPages))
		Expect(files.Managed("/.skills")).To(Equal(files.ManagedSkills))
		Expect(files.Managed("/Projects")).To(Equal(files.ManagedProjects))
	})

	It("marks the Projects folder only by its exact path, and never as managed for sharing", func() {
		Expect(files.Managed("/Projects/alpha")).To(BeEmpty())
		Expect(files.Managed("/Projects/alpha/files")).To(BeEmpty())
		for _, p := range []string{"/projects", "/Notes/Projects"} {
			Expect(files.Managed(p)).To(BeEmpty(), p)
		}
		// The label does not change which paths sharing and moves refuse.
		Expect(workfiles.Managed("/Projects")).To(BeFalse())
		Expect(workfiles.Managed("/Projects/alpha")).To(BeFalse())
	})

	It("marks the folder itself and not what is inside it, apart from the settings", func() {
		Expect(files.Managed("/Memory/index.md")).To(BeEmpty())
		Expect(files.Managed("/pages/notes")).To(BeEmpty())
		Expect(files.Managed("/.skills/summarize")).To(BeEmpty())
		Expect(files.Managed("/Chats/one")).To(BeEmpty())
	})

	It("leaves the user's own paths alone, also when the name is close", func() {
		for _, p := range []string{"/", "/Notes", "/memory", "/Pages", "/skills", "/chats", "/Notes/Memory", "/Notes/.skills", "/localai"} {
			Expect(files.Managed(p)).To(BeEmpty(), p)
		}
	})
})
