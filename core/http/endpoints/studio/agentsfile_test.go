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
			`{"version":3,"kind":"localai-agent","agent":{"name":"x"}}`,
			`{"version":0,"kind":"localai-agent","agent":{"name":"x"}}`,
		} {
			_, err := readAgentFile([]byte(raw))
			Expect(err).To(HaveOccurred(), raw)
		}
	})

	It("exports version 2 only for an agent with a folder", func() {
		Expect(agentFileVersion(false)).To(Equal(1))
		Expect(agentFileVersion(true)).To(Equal(2))
	})
})
