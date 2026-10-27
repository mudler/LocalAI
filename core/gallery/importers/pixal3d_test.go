package importers

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pixal3D importer", func() {
	It("requires explicit selection and only imports the supported multiview bundle", func() {
		imp := &Pixal3DImporter{}
		details := Details{URI: "https://huggingface.co/raven38/pixal3d-q8_0-v1", Preferences: json.RawMessage(`{}`)}
		Expect(imp.Match(details)).To(BeFalse())
		details.Preferences = json.RawMessage(`{"backend":"pixal3dcpp"}`)
		Expect(imp.Match(details)).To(BeTrue())
		cfg, err := imp.Import(details)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Files).To(HaveLen(10))
		Expect(cfg.ConfigFile).To(ContainSubstring("model: pixal3d-q8_0-v1"))
		details.URI = "https://huggingface.co/microsoft/TRELLIS.2-4B"
		_, err = imp.Import(details)
		Expect(err).To(HaveOccurred())
	})
})
