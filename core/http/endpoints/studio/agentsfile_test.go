package studio

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent files", func() {
	scoped := []byte(`{"version":2,"kind":"localai-agent","agent":{"name":"Scoped","folder":{"path":"/Projects/Acme","mode":"read"}}}`)

	It("an importer that knows only version 1 refuses a version 2 file", func() {
		// readAgentFileUpTo(raw, 1) is the check of a server from before
		// version 2: it must not take a scoped agent as one without a folder.
		_, err := readAgentFileUpTo(scoped, 1)
		Expect(err).To(MatchError(ContainSubstring("version")))
	})

	It("reads the folder of a version 2 file", func() {
		def, err := readAgentFile(scoped)
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Folder).NotTo(BeNil())
		Expect(def.Folder.Path).To(Equal("/Projects/Acme"))
		Expect(def.Folder.Mode).To(Equal("read"))
	})

	It("still reads version 1 and a bare definition", func() {
		def, err := readAgentFile([]byte(`{"version":1,"kind":"localai-agent","agent":{"name":"Old"}}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Name).To(Equal("Old"))
		Expect(def.Folder).To(BeNil())
		def, err = readAgentFile([]byte(`{"name":"Bare"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Name).To(Equal("Bare"))
	})

	It("refuses a version it does not know", func() {
		for _, raw := range []string{
			`{"version":5,"kind":"localai-agent","agent":{"name":"x"}}`,
			`{"version":0,"kind":"localai-agent","agent":{"name":"x"}}`,
		} {
			_, err := readAgentFile([]byte(raw))
			Expect(err).To(HaveOccurred(), raw)
		}
	})

	It("exports version 2 for one folder and version 3 for several", func() {
		Expect(agentFileVersion(0, false)).To(Equal(1))
		Expect(agentFileVersion(1, false)).To(Equal(2))
		Expect(agentFileVersion(2, false)).To(Equal(3))
		Expect(agentFileVersion(0, true)).To(Equal(4))
		Expect(agentFileVersion(2, true)).To(Equal(4))
	})

	It("reads the tool switches of a version 4 file, which an older importer refuses", func() {
		raw := []byte(`{"version":4,"kind":"localai-agent","agent":{"name":"Careful","builtins_off":["triggers","web"],"builtins_on":["saved_agents"]}}`)
		_, err := readAgentFileUpTo(raw, 3)
		Expect(err).To(MatchError(ContainSubstring("version")))
		def, err := readAgentFile(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(def.BuiltinsOff).To(Equal([]string{"triggers", "web"}))
		Expect(def.BuiltinsOn).To(Equal([]string{"saved_agents"}))
	})

	It("reads the folders of a version 3 file, and a version 2 reader refuses it", func() {
		multi := []byte(`{"version":3,"kind":"localai-agent","agent":{"name":"M","folders":[{"path":"/A","mode":"write"},{"path":"/B","mode":"read"}]}}`)
		def, err := readAgentFile(multi)
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Folders).To(HaveLen(2))
		Expect(def.Folders[1].Path).To(Equal("/B"))
		_, err = readAgentFileUpTo(multi, 2)
		Expect(err).To(MatchError(ContainSubstring("version")))
	})
})
