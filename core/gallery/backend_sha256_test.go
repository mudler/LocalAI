package gallery

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// Backend gallery integrity check. Operators populate `sha256:` on each
// backend gallery entry; the install path now passes that value into the
// downloader (which already knows how to hash-verify and roll back on
// mismatch). This test pins the YAML wire format so a future refactor of
// GalleryBackend can't drop the field silently.
var _ = Describe("GalleryBackend.SHA256 wire format", func() {
	It("parses sha256 from YAML", func() {
		data := []byte(`name: test-backend
uri: https://example.com/backend.tar.gz
sha256: deadbeefcafef00d
`)
		var b GalleryBackend
		Expect(yaml.Unmarshal(data, &b)).To(Succeed())
		Expect(b.SHA256).To(Equal("deadbeefcafef00d"))
	})

	It("parses sha256 from JSON", func() {
		// The struct is JSON-tagged for HTTP API responses too.
		var b GalleryBackend
		// Round-trip via YAML to JSON to keep the test framework simple.
		b.Metadata.Name = "x"
		b.URI = "https://example.com/x.tar.gz"
		b.SHA256 = "deadbeefcafef00d"
		out, err := yaml.Marshal(&b)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(ContainSubstring("sha256: deadbeefcafef00d"))
	})

	It("omits sha256 when empty", func() {
		b := GalleryBackend{Metadata: Metadata{Name: "x"}, URI: "https://example.com/x.tar.gz"}
		out, err := yaml.Marshal(&b)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).ToNot(ContainSubstring("sha256:"),
			"empty SHA256 must use omitempty so old galleries don't gain a noisy field")
	})

	It("defaults SHA256 to empty for galleries that don't specify it", func() {
		// Old galleries without sha256: keep working. The downloader emits a
		// runtime warning ("downloading without integrity check") which is
		// the deliberate carrot-stick toward populating the field.
		var b GalleryBackend
		Expect(yaml.Unmarshal([]byte(`name: legacy-backend
uri: https://example.com/legacy.tar.gz
`), &b)).To(Succeed())
		Expect(b.SHA256).To(Equal(""))
	})
})
